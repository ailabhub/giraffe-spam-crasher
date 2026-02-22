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
	Model        string                 `json:"model"`
	Messages     []AnthropicMessage     `json:"messages"`
	MaxTokens    int                    `json:"max_tokens"`
	Temperature  float64                `json:"temperature"`
	OutputConfig *AnthropicOutputConfig `json:"output_config,omitempty"`
}

type AnthropicOutputConfig struct {
	Format AnthropicOutputFormat `json:"format"`
}

type AnthropicOutputFormat struct {
	Type   string         `json:"type"`
	Schema map[string]any `json:"schema"`
}

type AnthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	AntropicResponseUsage AntropicResponseUsage `json:"usage"`
}

type AntropicResponseUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}
