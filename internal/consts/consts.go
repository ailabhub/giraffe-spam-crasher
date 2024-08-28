package consts

import (
	"time"
)

const (
	SpamCacheTTL = 24 * 31 * time.Hour

	RedisSpamCacheKey = "spam:"
)

type StatKey string

func (s StatKey) String() string {
	return string(s)
}

const (
	StatKeySpamCount      StatKey = "spamCount"
	StatKeyCheckedCount   StatKey = "checkedCount"
	StatKeyCacheHitCount  StatKey = "cacheHitCount"
	StatKeyAiCheckedCount StatKey = "aiCheckedCount"
)

var (
	StatsKeys = map[StatKey]string{
		StatKeySpamCount:      "stats:spam_count:",
		StatKeyCheckedCount:   "stats:checked_count:",
		StatKeyCacheHitCount:  "stats:cache_hit_count:",
		StatKeyAiCheckedCount: "stats:ai_checked_count:",
	}
)
