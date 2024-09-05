package processor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
)

type AIProvider interface {
	ProcessMessage(ctx context.Context, message *structs.Message) (string, error)
}

func (s *SpamProcessor) getSpamScoreForMessage(ctx context.Context, message *structs.Message) (structs.SpamCheckResult, error) {
	response, err := s.claudeProvider.ProcessMessage(ctx, message)
	if err != nil {
		return structs.SpamCheckResult{}, fmt.Errorf("API error: %w", err)
	}

	var classification structs.SpamCheckResult
	err = json.Unmarshal([]byte(response), &classification)
	if err != nil {
		return structs.SpamCheckResult{}, fmt.Errorf("failed to parse JSON classification: %w", err)
	}

	return classification, nil
}
