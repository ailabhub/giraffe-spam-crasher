package processor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ailabhub/giraffe-spam-crasher/internal/consts"
	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
	"github.com/avast/retry-go"
	"github.com/redis/go-redis/v9"
)

type SpamProcessor struct {
	claudeProvider AIProvider
	redis          *redis.Client
}

func NewSpamProcessor(
	claudeProvider AIProvider,
	redis *redis.Client,
) *SpamProcessor {
	return &SpamProcessor{
		claudeProvider: claudeProvider,
		redis:          redis,
	}
}

func (s *SpamProcessor) CheckForSpam(ctx context.Context, message structs.Message) (structs.SpamCheckResult, error) {
	var (
		spamScore structs.SpamCheckResult
		err       error
	)
	if message.Hash() != "" {
		spamScore, err = s.getFromCache(ctx, message)
		if err != nil {
			slog.Error("get from cache", "error", err)
		}
	}

	if spamScore.FromCache {
		return spamScore, nil
	}

	err = retry.Do(
		func() error {
			spamScore, err = s.getSpamScoreForMessage(ctx, message)
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

	cachedData, err := json.Marshal(spamScore)
	if err != nil {
		return structs.SpamCheckResult{}, fmt.Errorf("json.Marshal: %w", err)
	}

	if message.Hash() != "" {
		cacheKey := consts.RedisSpamCacheKey + message.Hash()
		s.redis.Set(ctx, cacheKey, cachedData, consts.SpamCacheTTL)
	}

	return spamScore, nil
}

func (s *SpamProcessor) getFromCache(ctx context.Context, message structs.Message) (structs.SpamCheckResult, error) {
	cacheKey := consts.RedisSpamCacheKey + message.Hash()
	cachedResult, err := s.redis.Get(ctx, cacheKey).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			return structs.SpamCheckResult{}, fmt.Errorf("redis.Get: %w", err)
		}
	}

	// Cache hit: Unmarshal and return the cached result
	if err == nil {
		var result structs.SpamCheckResult
		err = json.Unmarshal([]byte(cachedResult), &result)
		if err != nil {
			return structs.SpamCheckResult{}, fmt.Errorf("json.Unmarshal: %w", err)
		}
		result.FromCache = true

		return result, nil
	}

	return structs.SpamCheckResult{}, nil
}
