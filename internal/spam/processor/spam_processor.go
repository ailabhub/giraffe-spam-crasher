package processor

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/avast/retry-go"

	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
)

type SpamScoring interface {
	GetSpamScoreForMessage(ctx context.Context, message string) (structs.SpamCheckResult, error)
}

type SpamProcessor struct {
	SpamScoring SpamScoring
}

func NewSpamProcessor(spamScoring SpamScoring) *SpamProcessor {
	return &SpamProcessor{
		SpamScoring: spamScoring,
	}
}

func (s *SpamProcessor) CheckForSpam(ctx context.Context, _ int64, message string) (structs.SpamCheckResult, error) {
	var (
		spamScore structs.SpamCheckResult
		err       error
	)

	err = retry.Do(
		func() error {
			spamScore, err = s.SpamScoring.GetSpamScoreForMessage(ctx, message)
			if err != nil {
				return fmt.Errorf("s.SpamScoring.GetSpamScoreForMessage: %w", err)
			}

			return nil
		},
		retry.OnRetry(func(i uint, err error) {
			slog.Warn("Spam check failed, retrying", "attempt", i, "error", err)
		}),
		retry.Attempts(3),
	)
	if err != nil {
		return structs.SpamCheckResult{}, fmt.Errorf("retry.Do: %w", err)
	}

	return spamScore, nil
}
