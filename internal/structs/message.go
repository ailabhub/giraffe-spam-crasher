package structs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type Message struct {
	// Text of the message
	Text string
	// Images attached to the message
	Images []Image
}

func (m *Message) HasImages() bool {
	return len(m.Images) > 0
}

func (m *Message) HasText() bool {
	return m.Text != ""
}

func (m *Message) ToAnthropicMessage(prompt string) (AnthropicMessage, error) {
	if m.Text != "" {
		prompt = strings.ReplaceAll(prompt, "{{CHANNEL_CONTENT}}", m.Text)
	}

	content := []AnthropicContent{
		{
			Type: "text",
			Text: prompt,
		},
	}
	for _, img := range m.Images {
		resizedImage, err := img.Resize(300, 300)
		if err != nil {
			return AnthropicMessage{}, fmt.Errorf("error resizing image: %w", err)
		}
		content = append(content, resizedImage.ToAnthropicContent())
	}

	return AnthropicMessage{
		Role:    "user",
		Content: content,
	}, nil
}

func (m *Message) Hashable() bool {
	return m.Text != ""
}

func (m *Message) Hash() string {
	hash := sha256.Sum256([]byte(m.Text))
	return hex.EncodeToString(hash[:])
}
