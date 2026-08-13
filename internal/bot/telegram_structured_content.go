package bot

import (
	"encoding/json"
	"fmt"
	"strings"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

func structuredMessageText(message *tgbotapi.Message) []string {
	if message == nil {
		return nil
	}

	var parts []string
	if checklist := checklistSummary(message.Checklist); checklist != "" {
		parts = append(parts, checklist)
	}
	if addedTasks := checklistTasksAddedSummary(message.ChecklistTasksAdded); addedTasks != "" {
		parts = append(parts, addedTasks)
	}
	if richMessage := richMessageSummary(message.RichMessage); richMessage != "" {
		parts = append(parts, richMessage)
	}
	return parts
}

func checklistTasksAddedSummary(added *tgbotapi.ChecklistTasksAdded) string {
	if added == nil || len(added.Tasks) == 0 {
		return ""
	}

	parts := []string{"CHECKLIST_TASKS_ADDED:"}
	for index, task := range added.Tasks {
		if text := strings.TrimSpace(task.Text); text != "" {
			parts = append(parts, fmt.Sprintf("TASK %d: %s", index+1, text))
		}
		appendEntityLinks(&parts, fmt.Sprintf("TASK_%d_LINKS", index+1), task.TextEntities)
	}
	if len(parts) == 1 {
		return ""
	}
	return strings.Join(parts, "\n")
}

func checklistSummary(checklist *tgbotapi.Checklist) string {
	if checklist == nil {
		return ""
	}

	parts := []string{"CHECKLIST:"}
	if title := strings.TrimSpace(checklist.Title); title != "" {
		parts = append(parts, "TITLE: "+title)
	}
	appendEntityLinks(&parts, "TITLE_LINKS", checklist.TitleEntities)

	for index, task := range checklist.Tasks {
		if text := strings.TrimSpace(task.Text); text != "" {
			parts = append(parts, fmt.Sprintf("TASK %d: %s", index+1, text))
		}
		appendEntityLinks(&parts, fmt.Sprintf("TASK_%d_LINKS", index+1), task.TextEntities)
	}

	if len(parts) == 1 {
		return ""
	}
	return strings.Join(parts, "\n")
}

func externalReplyChecklistSummary(reply *tgbotapi.ExternalReplyInfo) string {
	if reply == nil {
		return ""
	}
	return checklistSummary(reply.Checklist)
}

func richMessageSummary(message *tgbotapi.RichMessage) string {
	root := normalizedRichMessage(message)
	if root == nil {
		return ""
	}

	var lines []string
	appendRichBlocks(&lines, root["blocks"])
	if len(lines) == 0 {
		return ""
	}
	return "RICH_MESSAGE:\n" + strings.Join(lines, "\n")
}

func normalizedRichMessage(message *tgbotapi.RichMessage) map[string]any {
	if message == nil {
		return nil
	}

	data, err := json.Marshal(message)
	if err != nil {
		return nil
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil
	}
	return root
}

func appendRichBlocks(lines *[]string, value any) {
	blocks, ok := value.([]any)
	if !ok {
		return
	}
	for _, block := range blocks {
		appendRichBlock(lines, block)
	}
}

func appendRichBlock(lines *[]string, value any) {
	block, ok := value.(map[string]any)
	if !ok {
		return
	}

	blockType, _ := block["type"].(string)
	switch blockType {
	case "paragraph", "heading", "pre", "footer", "thinking":
		appendRichLine(lines, richText(block["text"]))
	case "mathematical_expression":
		appendRichLine(lines, stringValue(block["expression"]))
	case "list":
		appendRichList(lines, block["items"])
	case "blockquote":
		appendRichBlocks(lines, block["blocks"])
		appendRichLine(lines, richText(block["credit"]))
	case "pullquote":
		appendRichLine(lines, richText(block["text"]))
		appendRichLine(lines, richText(block["credit"]))
	case "collage", "slideshow":
		appendRichBlocks(lines, block["blocks"])
		appendRichCaption(lines, block["caption"])
	case "table":
		appendRichTable(lines, block["cells"])
		appendRichLine(lines, richText(block["caption"]))
	case "details":
		appendRichLine(lines, richText(block["summary"]))
		appendRichBlocks(lines, block["blocks"])
	case "map":
		appendRichMap(lines, block["location"])
		appendRichCaption(lines, block["caption"])
	case "animation", "audio", "photo", "video", "voice_note":
		appendRichLine(lines, "MEDIA: "+blockType)
		appendRichCaption(lines, block["caption"])
	case "divider", "anchor":
		return
	default:
		// Preserve useful content from block types added by future Bot API versions.
		appendRichLine(lines, richText(block["text"]))
		appendRichLine(lines, richText(block["summary"]))
		appendRichBlocks(lines, block["blocks"])
		appendRichCaption(lines, block["caption"])
	}
}

func appendRichList(lines *[]string, value any) {
	items, ok := value.([]any)
	if !ok {
		return
	}

	for _, value := range items {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}

		var itemLines []string
		appendRichBlocks(&itemLines, item["blocks"])
		if len(itemLines) == 0 {
			continue
		}

		label := strings.TrimSpace(stringValue(item["label"]))
		if label == "" {
			label = "-"
		}
		*lines = append(*lines, label+" "+strings.Join(itemLines, " "))
	}
}

func appendRichTable(lines *[]string, value any) {
	rows, ok := value.([]any)
	if !ok {
		return
	}

	for _, value := range rows {
		cells, ok := value.([]any)
		if !ok {
			continue
		}

		var cellTexts []string
		for _, value := range cells {
			cell, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if text := strings.TrimSpace(richText(cell["text"])); text != "" {
				cellTexts = append(cellTexts, text)
			}
		}
		if len(cellTexts) > 0 {
			*lines = append(*lines, strings.Join(cellTexts, " | "))
		}
	}
}

func appendRichCaption(lines *[]string, value any) {
	caption, ok := value.(map[string]any)
	if !ok {
		return
	}
	appendRichLine(lines, richText(caption["text"]))
	appendRichLine(lines, richText(caption["credit"]))
}

func appendRichMap(lines *[]string, value any) {
	location, ok := value.(map[string]any)
	if !ok {
		return
	}
	latitude, latitudeOK := location["latitude"].(float64)
	longitude, longitudeOK := location["longitude"].(float64)
	if latitudeOK && longitudeOK {
		appendRichLine(lines, fmt.Sprintf("LOCATION: %g,%g", latitude, longitude))
	}
}

func richText(value any) string {
	switch value := value.(type) {
	case nil:
		return ""
	case string:
		return value
	case []any:
		var builder strings.Builder
		for _, part := range value {
			builder.WriteString(richText(part))
		}
		return builder.String()
	case map[string]any:
		visibleText := richText(value["text"])
		typeName, _ := value["type"].(string)
		switch typeName {
		case "url":
			return textWithTarget(visibleText, stringValue(value["url"]))
		case "email_address":
			return textWithTarget(visibleText, stringValue(value["email_address"]))
		case "phone_number":
			return textWithTarget(visibleText, stringValue(value["phone_number"]))
		case "bank_card_number":
			return textWithTarget(visibleText, stringValue(value["bank_card_number"]))
		case "mention":
			username := strings.TrimPrefix(stringValue(value["username"]), "@")
			if username != "" {
				return textWithTarget(visibleText, "@"+username)
			}
		case "custom_emoji":
			return stringValue(value["alternative_text"])
		case "mathematical_expression":
			return stringValue(value["expression"])
		}
		return visibleText
	default:
		return ""
	}
}

func textWithTarget(text, target string) string {
	text = strings.TrimSpace(text)
	target = strings.TrimSpace(target)
	if text == "" {
		return target
	}
	if target == "" || strings.Contains(text, target) {
		return text
	}
	return text + " -> " + target
}

func appendRichLine(lines *[]string, line string) {
	if line = strings.TrimSpace(line); line != "" {
		*lines = append(*lines, line)
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func richMessageThumbnail(message *tgbotapi.RichMessage) *tgbotapi.PhotoSize {
	root := normalizedRichMessage(message)
	if root == nil {
		return nil
	}
	return findRichPhoto(root["blocks"])
}

func findRichPhoto(value any) *tgbotapi.PhotoSize {
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			if photo := findRichPhoto(child); photo != nil {
				return photo
			}
		}
	case map[string]any:
		if photos, ok := value["photo"].([]any); ok && len(photos) > 0 {
			if photo := photoSizeFromRichValue(photos[len(photos)/2]); photo != nil {
				return photo
			}
		}
		if thumbnail := photoSizeFromRichValue(value["thumbnail"]); thumbnail != nil {
			return thumbnail
		}
		for _, key := range []string{"blocks", "items", "animation", "audio", "video", "voice_note"} {
			if photo := findRichPhoto(value[key]); photo != nil {
				return photo
			}
		}
	}
	return nil
}

func photoSizeFromRichValue(value any) *tgbotapi.PhotoSize {
	photo, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	fileID, _ := photo["file_id"].(string)
	if fileID == "" {
		return nil
	}
	return &tgbotapi.PhotoSize{FileID: fileID}
}
