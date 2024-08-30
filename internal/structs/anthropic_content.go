package structs

// New structs to match the JSON structure
type AnthropicContent struct {
	Type   string                `json:"type"`
	Source *AnthropicImageSource `json:"source,omitempty"`
	Text   string                `json:"text,omitempty"`
}

type AnthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type AnthropicMessage struct {
	Role    string             `json:"role"`
	Content []AnthropicContent `json:"content"`
}

type AnthropicRequest struct {
	Model     string             `json:"model"`
	Messages  []AnthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`
}

type AnthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}