package structs

import (
	"crypto/sha256"
	"encoding/hex"
)

type Message struct {
	// Text of the message
	Text *string
	// Images attached to the message
	Image *Image
}

func (m *Message) HasImage() bool {
	return m.Image != nil
}

func (m *Message) HasText() bool {
	return m.Text != nil
}

func (m *Message) ToAnthropicMessage(prompt string) (AnthropicMessage, error) {
	content := []AnthropicContent{
		{
			Type: "text",
			Text: &prompt,
		},
	}
	if m.HasText() {
		content = append(content, AnthropicContent{
			Type: "text",
			Text: m.Text,
		})
	}

	if m.HasImage() {
		content = append(content, m.Image.ToAnthropicContent())
	}

	return AnthropicMessage{
		Role:    "user",
		Content: content,
	}, nil
}

func (m *Message) Hash() string {
	var hash string
	if m.HasText() {
		sum256 := sha256.Sum256([]byte(*m.Text))
		hash = hex.EncodeToString(sum256[:])
	}

	if m.HasImage() {
		sum256 := sha256.Sum256(*m.Image)
		imageHash := hex.EncodeToString(sum256[:])
		hash += imageHash
	}

	return hash
}
