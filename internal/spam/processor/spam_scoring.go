package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
)

type AIProvider interface {
	ProcessMessage(ctx context.Context, message structs.Message) (string, error)
}

var (
	reasoningRegex = regexp.MustCompile(`<reasoning>([\s\S]*?)</reasoning>`)
	jsonRegex      = regexp.MustCompile(`<json>([\s\S]*?)</json>`)
)

func (s *SpamProcessor) getSpamScoreForMessage(ctx context.Context, message structs.Message) (structs.SpamCheckResult, error) {
	response, err := s.claudeProvider.ProcessMessage(ctx, message)
	if err != nil {
		return structs.SpamCheckResult{}, fmt.Errorf("API error: %w", err)
	}

	reasoningMatch := reasoningRegex.FindStringSubmatch(response)

	var reasoning string
	if len(reasoningMatch) > 1 {
		reasoning = reasoningMatch[1]
	} else {
		return structs.SpamCheckResult{}, fmt.Errorf("could not extract reasoning from response")
	}

	jsonMatch := jsonRegex.FindStringSubmatch(response)

	var classification structs.SpamClassification
	if len(jsonMatch) > 1 {
		err = json.Unmarshal([]byte(jsonMatch[1]), &classification)
		if err != nil {
			return structs.SpamCheckResult{}, fmt.Errorf("failed to parse JSON classification: %w", err)
		}
	} else {
		return structs.SpamCheckResult{}, fmt.Errorf("could not extract JSON classification from response")
	}

	return structs.SpamCheckResult{
		Reasoning: reasoning,
		SpamScore: classification.SpamScore,
	}, nil
}
