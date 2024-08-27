package spam_processor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ailabhub/giraffe-spam-crasher/internal/consts"
	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
	"github.com/redis/go-redis/v9"
)

type SpamProcessorCache struct {
	SpamProcessor SpamProcessor
	redis         *redis.Client
}

func NewSpamProcessorCache(
	base SpamProcessor,
	redis *redis.Client,
) SpamProcessorCache {
	return SpamProcessorCache{
		SpamProcessor: base,
		redis:         redis,
	}
}

func (s *SpamProcessorCache) CheckForSpam(ctx context.Context, channelID int64, message string) (structs.SpamCheckResult, error) {
	hash := s.hashMessage(message)

	cacheKey := consts.RedisSpamCacheKey + hash
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
		err = s.incrementCacheHit(ctx, channelID)
		if err != nil {
			// TODO add log
		}

		return result, nil
	}

	// Cache miss: Call the underlying SpamProcessor
	spamCheckResult, err := s.SpamProcessor.CheckForSpam(ctx, channelID, message)
	if err != nil {
		return spamCheckResult, fmt.Errorf("s.SpamProcessor.CheckForSpam: %w", err)
	}

	cachedData, err := json.Marshal(spamCheckResult)
	if err != nil {
		return structs.SpamCheckResult{}, fmt.Errorf("json.Marshal: %w", err)
	}

	s.redis.Set(ctx, cacheKey, cachedData, consts.SpamCacheTTL)

	return spamCheckResult, nil
}

func (s *SpamProcessorCache) hashMessage(message string) string {
	hash := sha256.Sum256([]byte(message))
	return hex.EncodeToString(hash[:])
}

func (s *SpamProcessorCache) incrementCacheHit(ctx context.Context, channelID int64) error {
	key := fmt.Sprintf("%s%d", consts.StatsKeys[consts.StatKeyCacheHitCount], channelID)
	err := s.redis.Incr(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("redis.Incr: %w", err)
	}

	return nil
}
