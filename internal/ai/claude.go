package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"time"

	"github.com/disintegration/imaging"
	"golang.org/x/time/rate"
)

type AnthropicProvider struct {
	client      *http.Client
	apiKey      string
	model       string
	rateLimiter *rate.Limiter
	prompt      string
}

func NewAnthropicProvider(apiKey, model string, rateLimit float64, prompt string) *AnthropicProvider {
	var limiter *rate.Limiter
	if rateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(rateLimit), 1)
	} else {
		limiter = rate.NewLimiter(rate.Inf, 0) // No rate limit
	}
	return &AnthropicProvider{
		client:      &http.Client{Timeout: 30 * time.Second},
		apiKey:      apiKey,
		model:       model,
		rateLimiter: limiter,
		prompt:      prompt,
	}
}

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

func (p *AnthropicProvider) ProcessMessage(ctx context.Context, message string) (string, error) {
	err := p.rateLimiter.Wait(ctx)
	if err != nil {
		return "", fmt.Errorf("rate limit error: %w", err)
	}

	requestBody, err := json.Marshal(AnthropicRequest{
		Model: p.model,
		Messages: []AnthropicMessage{
			{
				Role: "user",
				Content: []AnthropicContent{
					{
						Type: "text",
						Text: message,
					},
				},
			},
		},
		MaxTokens: 1000,
	})
	if err != nil {
		return "", fmt.Errorf("error marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(requestBody))
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var anthropicResp AnthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return "", fmt.Errorf("error decoding response: %w", err)
	}

	if len(anthropicResp.Content) == 0 || len(anthropicResp.Content[0].Text) == 0 {
		return "", fmt.Errorf("empty response from Anthropic API")
	}

	return anthropicResp.Content[0].Text, nil
}

func (p *AnthropicProvider) ProcessImage(ctx context.Context, imageData []byte) (string, error) {
	err := p.rateLimiter.Wait(ctx)
	if err != nil {
		return "", fmt.Errorf("rate limit error: %w", err)
	}

	// Resize the image
	resizedImage, err := resizeImage(imageData, 200, 200)
	if err != nil {
		return "", fmt.Errorf("error resizing image: %w", err)
	}

	// Encode the resized image data to base64
	base64Image := base64.StdEncoding.EncodeToString(resizedImage)

	// Determine media type
	mediaType := http.DetectContentType(imageData)

	// Prepare the request
	requestBody, err := json.Marshal(AnthropicRequest{
		Model: p.model,
		Messages: []AnthropicMessage{
			{
				Role: "user",
				Content: []AnthropicContent{
					{
						Type: "text",
						Text: p.prompt,
					},
					{
						Type: "image",
						Source: &AnthropicImageSource{
							Type:      "base64",
							MediaType: mediaType,
							Data:      base64Image,
						},
					},
				},
			},
		},
		MaxTokens: 1024,
	})
	if err != nil {
		return "", fmt.Errorf("error marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(requestBody))
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var anthropicResp AnthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return "", fmt.Errorf("error decoding response: %w", err)
	}

	if len(anthropicResp.Content) == 0 || len(anthropicResp.Content[0].Text) == 0 {
		return "", fmt.Errorf("empty response from Anthropic API")
	}

	return anthropicResp.Content[0].Text, nil
}

func resizeImage(imageData []byte, width, height int) ([]byte, error) {
	// Decode the image
	img, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return nil, fmt.Errorf("error decoding image: %w", err)
	}

	// Resize the image
	resized := imaging.Resize(img, width, height, imaging.Lanczos)

	// Encode the resized image to JPEG
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, resized, &jpeg.Options{Quality: 85})
	if err != nil {
		return nil, fmt.Errorf("error encoding resized image: %w", err)
	}

	return buf.Bytes(), nil
}
