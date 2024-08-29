package ai

import (
	"context"
	"fmt"

	"github.com/google/generative-ai-go/genai"
	"golang.org/x/time/rate"
	"google.golang.org/api/option"
)

type GeminiProvider struct {
	client      *genai.Client
	model       *genai.GenerativeModel
	rateLimiter *rate.Limiter
}

func NewGeminiProvider(apiKey, model string, rateLimit float64) (*GeminiProvider, error) {
	ctx := context.Background()
	var limiter *rate.Limiter
	if rateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(rateLimit), 1)
	} else {
		limiter = rate.NewLimiter(rate.Inf, 0) // No rate limit
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}

	mod := client.GenerativeModel(model)

	// Set default parameters
	mod.SetTemperature(0)
	mod.SetTopK(64)
	mod.SetTopP(0.95)
	mod.SetMaxOutputTokens(8192)
	mod.ResponseMIMEType = "text/plain"

	// For now safety settings didn't work — gemini returns 400 error if any of them are set
	mod.SafetySettings = []*genai.SafetySetting{
		// {
		// 	Category:  genai.HarmCategoryUnspecified,
		// 	Threshold: genai.HarmBlockOnlyHigh,
		// },
		// {
		// 	Category:  genai.HarmCategoryDangerous,
		// 	Threshold: genai.HarmBlockOnlyHigh,
		// },
		// {
		// 	Category:  genai.HarmCategoryDangerousContent,
		// 	Threshold: genai.HarmBlockOnlyHigh,
		// },
		// {
		// 	Category:  genai.HarmCategoryDerogatory,
		// 	Threshold: genai.HarmBlockOnlyHigh,
		// },
		// {
		// 	Category:  genai.HarmCategoryHarassment,
		// 	Threshold: genai.HarmBlockOnlyHigh,
		// },
		// {
		// 	Category:  genai.HarmCategoryHateSpeech,
		// 	Threshold: genai.HarmBlockOnlyHigh,
		// },
		// {
		// 	Category:  genai.HarmCategoryMedical,
		// 	Threshold: genai.HarmBlockOnlyHigh,
		// },
		// {
		// 	Category:  genai.HarmCategorySexual,
		// 	Threshold: genai.HarmBlockLowAndAbove,
		// },
		// {
		// 	Category:  genai.HarmCategorySexuallyExplicit,
		// 	Threshold: genai.HarmBlockOnlyHigh,
		// },
		// {
		// 	Category:  genai.HarmCategoryToxicity,
		// 	Threshold: genai.HarmBlockOnlyHigh,
		// },
		// {
		// 	Category:  genai.HarmCategoryViolence,
		// 	Threshold: genai.HarmBlockOnlyHigh,
		// },
	}
	// model.SafetySettings = Adjust safety settings
	// See https://ai.google.dev/gemini-api/docs/safety-settings

	return &GeminiProvider{
		client:      client,
		model:       mod,
		rateLimiter: limiter,
	}, nil
}

func (p *GeminiProvider) ProcessMessage(ctx context.Context, message string) (string, error) {
	session := p.model.StartChat()
	resp, err := session.SendMessage(ctx, genai.Text(message))
	if err != nil {
		return "", fmt.Errorf("error sending message: %w", err)
	}

	response := ""
	for _, part := range resp.Candidates[0].Content.Parts {
		fmt.Printf("%v\n", part)
		response = response + fmt.Sprintf("%v", part)
	}
	return response, nil
}

func (p *GeminiProvider) Close() error {
	return p.client.Close()
}
