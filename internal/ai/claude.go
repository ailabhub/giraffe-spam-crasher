package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
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

func (p *AnthropicProvider) ProcessMessage(ctx context.Context, message structs.Message) (string, error) {
	err := p.rateLimiter.Wait(ctx)
	if err != nil {
		return "", fmt.Errorf("rate limit error: %w", err)
	}

	anthropicMessage, err := message.ToAnthropicMessage(p.prompt)
	if err != nil {
		return "", fmt.Errorf("error converting message to Anthropic format: %w", err)
	}

	requestBody, err := json.Marshal(
		structs.AnthropicRequest{
			Model:     p.model,
			Messages:  []structs.AnthropicMessage{anthropicMessage},
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

	var anthropicResp structs.AnthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return "", fmt.Errorf("error decoding response: %w", err)
	}

	if len(anthropicResp.Content) == 0 || len(anthropicResp.Content[0].Text) == 0 {
		return "", fmt.Errorf("empty response from Anthropic API")
	}

	return anthropicResp.Content[0].Text, nil
}
