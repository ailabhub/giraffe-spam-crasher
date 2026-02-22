package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"log/slog"

	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
	"golang.org/x/time/rate"
)

type AnthropicProvider struct {
	client      *http.Client
	apiKey      string
	model       string
	rateLimiter *rate.Limiter
	prompt      string
	logger      *slog.Logger
}

func NewAnthropicProvider(apiKey, model string, rateLimit float64, prompt string, logger *slog.Logger) *AnthropicProvider {
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
		logger:      logger,
	}
}

func (p *AnthropicProvider) ProcessMessage(ctx context.Context, message *structs.Message) (string, error) {
	err := p.rateLimiter.Wait(ctx)
	if err != nil {
		return "", fmt.Errorf("rate limit error: %w", err)
	}

	anthropicMessage, err := message.ToAnthropicMessage(p.prompt)
	if err != nil {
		return "", fmt.Errorf("error converting message to Anthropic format: %w", err)
	}

	request := structs.AnthropicRequest{
		Model:       p.model,
		Messages:    []structs.AnthropicMessage{anthropicMessage},
		MaxTokens:   1000,
		Temperature: 0,
		OutputConfig: &structs.AnthropicOutputConfig{
			Format: structs.AnthropicOutputFormat{
				Type: "json_schema",
				Schema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"spam_score": map[string]any{
							"type":    "number",
							"minimum": 0,
							"maximum": 1,
						},
						"reasoning": map[string]any{
							"type": "string",
						},
					},
					"required":             []string{"spam_score", "reasoning"},
					"additionalProperties": false,
				},
			},
		},
	}

	responseText, statusCode, rawBody, err := p.sendMessagesRequest(ctx, request)
	if err != nil {
		// Backward-compatible fallback for models/accounts where output_config is unsupported.
		if statusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(rawBody), "output_config") {
			p.logger.Warn("Structured output unsupported, retrying without output_config", "model", p.model)
			request.OutputConfig = nil
			responseText, _, _, err = p.sendMessagesRequest(ctx, request)
		}
	}
	if err != nil {
		return "", err
	}

	return responseText, nil
}

func (p *AnthropicProvider) sendMessagesRequest(ctx context.Context, reqBody structs.AnthropicRequest) (responseText string, statusCode int, rawBody string, err error) {
	requestBody, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, "", fmt.Errorf("error marshaling request: %w", err)
	}

	p.logger.Debug("Raw Anthropic API request", "body", string(requestBody))

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(requestBody))
	if err != nil {
		return "", 0, "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", 0, "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	rawRespBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, "", fmt.Errorf("error reading response body: %w", err)
	}

	rawBody = string(rawRespBody)
	p.logger.Debug("Raw Anthropic API response", "status", resp.Status, "body", rawBody)

	if resp.StatusCode != http.StatusOK {
		return "", resp.StatusCode, rawBody, fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, rawBody)
	}

	var anthropicResp structs.AnthropicResponse
	if err := json.NewDecoder(bytes.NewReader(rawRespBody)).Decode(&anthropicResp); err != nil {
		return "", resp.StatusCode, rawBody, fmt.Errorf("error decoding response: %w", err)
	}

	if len(anthropicResp.Content) == 0 || len(anthropicResp.Content[0].Text) == 0 {
		return "", resp.StatusCode, rawBody, fmt.Errorf("empty response from Anthropic API")
	}

	return anthropicResp.Content[0].Text, resp.StatusCode, rawBody, nil
}
