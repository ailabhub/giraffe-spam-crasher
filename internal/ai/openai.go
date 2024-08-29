package ai

import (
	"context"
	"fmt"

	"github.com/sashabaranov/go-openai"
	"golang.org/x/time/rate"
)

type OpenAIProvider struct {
	client      *openai.Client
	model       string
	rateLimiter *rate.Limiter
}

func NewOpenAIProvider(apiKey, model string, rateLimit float64) *OpenAIProvider {
	var limiter *rate.Limiter
	if rateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(rateLimit), 1)
	} else {
		limiter = rate.NewLimiter(rate.Inf, 0) // No rate limit
	}
	return &OpenAIProvider{
		client:      openai.NewClient(apiKey),
		model:       model,
		rateLimiter: limiter,
	}
}

func (p *OpenAIProvider) ProcessMessage(ctx context.Context, message string) (string, error) {
	err := p.rateLimiter.Wait(ctx) // Wait for rate limit
	if err != nil {
		return "", fmt.Errorf("rate limit error: %w", err)
	}

	resp, err := p.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: p.model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: message,
				},
			},
			Temperature: 0,
		},
	)

	if err != nil {
		return "", fmt.Errorf("OpenAI API error: %w", err)
	}

	return resp.Choices[0].Message.Content, nil
}
