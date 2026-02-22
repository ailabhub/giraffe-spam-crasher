package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
)

type AIProvider interface {
	ProcessMessage(ctx context.Context, message *structs.Message) (string, error)
}

var ErrInvalidClassificationFormat = errors.New("invalid classification format")

func (s *SpamProcessor) getSpamScoreForMessage(ctx context.Context, message *structs.Message) (structs.SpamCheckResult, error) {
	response, err := s.claudeProvider.ProcessMessage(ctx, message)
	if err != nil {
		return structs.SpamCheckResult{}, fmt.Errorf("API error: %w", err)
	}

	classification, err := parseSpamClassification(response)
	if err != nil {
		return structs.SpamCheckResult{}, fmt.Errorf("%w: failed to parse JSON classification: %v", ErrInvalidClassificationFormat, err)
	}

	return classification, nil
}

func parseSpamClassification(response string) (structs.SpamCheckResult, error) {
	var classification structs.SpamCheckResult
	trimmed := strings.TrimSpace(response)

	// Fast path: model returned plain JSON.
	if err := json.Unmarshal([]byte(trimmed), &classification); err == nil {
		return validateSpamClassification(classification)
	}

	// Fallback: model returned prose + JSON. Extract JSON objects and pick one with spam_score.
	candidates := extractJSONObjectCandidates(trimmed)
	for _, candidate := range candidates {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(candidate), &payload); err != nil {
			continue
		}
		if _, ok := payload["spam_score"]; !ok {
			continue
		}
		if err := json.Unmarshal([]byte(candidate), &classification); err != nil {
			continue
		}
		return validateSpamClassification(classification)
	}

	return structs.SpamCheckResult{}, fmt.Errorf("no valid JSON object with spam_score found")
}

func validateSpamClassification(classification structs.SpamCheckResult) (structs.SpamCheckResult, error) {
	if math.IsNaN(classification.SpamScore) || math.IsInf(classification.SpamScore, 0) {
		return structs.SpamCheckResult{}, fmt.Errorf("spam_score is not a finite number")
	}
	if classification.SpamScore < 0 || classification.SpamScore > 1 {
		return structs.SpamCheckResult{}, fmt.Errorf("spam_score out of range [0,1]: %v", classification.SpamScore)
	}
	return classification, nil
}

func extractJSONObjectCandidates(s string) []string {
	var out []string

	start := -1
	depth := 0
	inString := false
	escape := false

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if inString {
			if escape {
				escape = false
				continue
			}
			if ch == '\\' {
				escape = true
				continue
			}
			if ch == '"' {
				inString = false
			}
			continue
		}

		switch ch {
		case '"':
			inString = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				out = append(out, s[start:i+1])
				start = -1
			}
		}
	}

	return out
}
