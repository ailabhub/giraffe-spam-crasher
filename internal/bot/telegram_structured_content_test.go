package bot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	tgbotapi "github.com/OvyFlash/telegram-bot-api"
)

func TestFromTGToInternalMessageChecklist(t *testing.T) {
	const updateJSON = `{
		"update_id": 1,
		"message": {
			"message_id": 534632,
			"from": {"id": 8638980543, "is_bot": false, "first_name": "Laura", "last_name": "Edwards", "is_premium": true},
			"date": 1786605454,
			"chat": {"id": -1001098030726, "type": "supergroup", "title": "ctodailychat", "username": "ctodailychat"},
			"checklist": {
				"title": "Платим 2000₽ за прохождение короткого опроса",
				"tasks": [
					{"id": 1, "text": "Пользуетесь услугами мобильной связи? Нам важно узнать ваше мнение!"},
					{
						"id": 2,
						"text": "Пройти опрос можно на официальном сайте",
						"text_entities": [{"type": "text_link", "offset": 23, "length": 18, "url": "https://mts.rest/"}]
					}
				]
			}
		}
	}`

	var update tgbotapi.Update
	if err := json.Unmarshal([]byte(updateJSON), &update); err != nil {
		t.Fatalf("unmarshal Telegram update: %v", err)
	}
	if update.Message == nil || update.Message.Checklist == nil {
		t.Fatalf("checklist was lost while decoding update: %#v", update.Message)
	}

	message, err := (&Bot{}).fromTGToInternalMessage(context.Background(), update.Message)
	if err != nil {
		t.Fatalf("convert Telegram message: %v", err)
	}

	for _, expected := range []string{
		"CHECKLIST:",
		"TITLE: Платим 2000₽ за прохождение короткого опроса",
		"TASK 1: Пользуетесь услугами мобильной связи? Нам важно узнать ваше мнение!",
		"TASK 2: Пройти опрос можно на официальном сайте",
		"TASK_2_LINKS:\nhttps://mts.rest/",
	} {
		if !strings.Contains(message.Text, expected) {
			t.Errorf("message text does not contain %q:\n%s", expected, message.Text)
		}
	}
	if message.IsEmpty() {
		t.Fatal("checklist message must be sent to the spam classifier")
	}
}

func TestFromTGToInternalMessageRichMessage(t *testing.T) {
	const updateJSON = `{
		"update_id": 2,
		"message": {
			"message_id": 42,
			"from": {"id": 123, "is_bot": false, "first_name": "Spammer"},
			"date": 1786605454,
			"chat": {"id": -1001098030726, "type": "supergroup", "title": "ctodailychat"},
			"rich_message": {
				"blocks": [
					{"type": "heading", "text": "Важное предложение", "size": 2},
					{
						"type": "paragraph",
						"text": ["Пройти опрос", " ", {"type": "url", "text": "на сайте", "url": "https://mts.rest/"}]
					},
					{
						"type": "list",
						"items": [{"label": "1.", "blocks": [{"type": "paragraph", "text": "Получить 2000₽"}]}]
					},
					{
						"type": "details",
						"summary": "Условия",
						"blocks": [{"type": "paragraph", "text": "Только сегодня"}]
					}
				]
			}
		}
	}`

	var update tgbotapi.Update
	if err := json.Unmarshal([]byte(updateJSON), &update); err != nil {
		t.Fatalf("unmarshal Telegram update: %v", err)
	}
	if update.Message == nil || update.Message.RichMessage == nil {
		t.Fatalf("rich message was lost while decoding update: %#v", update.Message)
	}

	message, err := (&Bot{}).fromTGToInternalMessage(context.Background(), update.Message)
	if err != nil {
		t.Fatalf("convert Telegram message: %v", err)
	}

	for _, expected := range []string{
		"RICH_MESSAGE:",
		"Важное предложение",
		"Пройти опрос на сайте -> https://mts.rest/",
		"1. Получить 2000₽",
		"Условия",
		"Только сегодня",
	} {
		if !strings.Contains(message.Text, expected) {
			t.Errorf("message text does not contain %q:\n%s", expected, message.Text)
		}
	}
	if message.IsEmpty() {
		t.Fatal("rich message must be sent to the spam classifier")
	}
}

func TestFromTGToInternalMessageChecklistTasksAdded(t *testing.T) {
	const updateJSON = `{
		"update_id": 3,
		"message": {
			"message_id": 43,
			"from": {"id": 124, "is_bot": false, "first_name": "Spammer"},
			"date": 1786605454,
			"chat": {"id": -1001098030726, "type": "supergroup", "title": "ctodailychat"},
			"checklist_tasks_added": {
				"tasks": [{
					"id": 3,
					"text": "Забрать приз",
					"text_entities": [{"type": "text_link", "offset": 0, "length": 12, "url": "https://example.invalid/"}]
				}]
			}
		}
	}`

	var update tgbotapi.Update
	if err := json.Unmarshal([]byte(updateJSON), &update); err != nil {
		t.Fatalf("unmarshal Telegram update: %v", err)
	}

	message, err := (&Bot{}).fromTGToInternalMessage(context.Background(), update.Message)
	if err != nil {
		t.Fatalf("convert Telegram message: %v", err)
	}

	for _, expected := range []string{
		"CHECKLIST_TASKS_ADDED:",
		"TASK 1: Забрать приз",
		"TASK_1_LINKS:\nhttps://example.invalid/",
	} {
		if !strings.Contains(message.Text, expected) {
			t.Errorf("message text does not contain %q:\n%s", expected, message.Text)
		}
	}
	if message.IsEmpty() {
		t.Fatal("new checklist tasks must be sent to the spam classifier")
	}
}

func TestRichMessageThumbnail(t *testing.T) {
	const richMessageJSON = `{
		"blocks": [{
			"type": "collage",
			"blocks": [{
				"type": "photo",
				"photo": [
					{"file_id": "small", "file_unique_id": "a", "width": 90, "height": 90},
					{"file_id": "medium", "file_unique_id": "b", "width": 320, "height": 320},
					{"file_id": "large", "file_unique_id": "c", "width": 1280, "height": 1280}
				]
			}]
		}]
	}`

	var richMessage tgbotapi.RichMessage
	if err := json.Unmarshal([]byte(richMessageJSON), &richMessage); err != nil {
		t.Fatalf("unmarshal rich message: %v", err)
	}

	thumbnail := richMessageThumbnail(&richMessage)
	if thumbnail == nil {
		t.Fatal("expected rich message photo")
	}
	if thumbnail.FileID != "medium" {
		t.Fatalf("expected medium photo, got %q", thumbnail.FileID)
	}
}
