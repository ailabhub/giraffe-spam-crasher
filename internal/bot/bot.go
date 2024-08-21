package bot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ailabhub/giraffe-spam-crasher/internal/ai"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/go-redis/v9"
)

type Bot struct {
	api               *tgbotapi.BotAPI
	redis             *redis.Client
	logger            *slog.Logger
	aiprovider        ai.Provider
	config            *Config
	adminCache        map[int64]AdminRights
	cacheMutex        sync.RWMutex
	stopChan          chan struct{}
	whitelistChannels map[int64]bool
	// New field for statistics
	statsKeys map[string]string
}

type Config struct {
	Prompt            string
	Threshold         float64
	NewUserThreshold  int
	WhitelistChannels []int64
	LogChannels       map[int64]int64
}

func New(logger *slog.Logger, rdb *redis.Client, aiprovider ai.Provider, config *Config) (*Bot, error) {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}

	// Convert WhitelistChannels slice to map for efficient lookup
	whitelistMap := make(map[int64]bool)
	for _, channelID := range config.WhitelistChannels {
		whitelistMap[channelID] = true
	}

	bot := &Bot{
		api:               api,
		redis:             rdb,
		logger:            logger,
		aiprovider:        aiprovider,
		config:            config,
		adminCache:        make(map[int64]AdminRights),
		stopChan:          make(chan struct{}),
		whitelistChannels: whitelistMap,
		statsKeys: map[string]string{
			"spamCount":      "stats:spam_count:",
			"checkedCount":   "stats:checked_count:",
			"cacheHitCount":  "stats:cache_hit_count:",
			"aiCheckedCount": "stats:ai_checked_count:",
		},
	}

	return bot, nil
}

func (b *Bot) Start() { //nolint:gocyclo,gocognit
	b.logger.Info("Authorized on account", "username", b.api.Self.UserName)
	b.logger.Info("Config", "threshold", b.config.Threshold, "newUserThreshold", b.config.NewUserThreshold, "whitelistChannels", b.config.WhitelistChannels)
	b.logger.Info("Starting bot")

	// Start the cache clearing goroutine
	go b.clearAdminCacheRoutine()

	// Start the statistics reporting goroutine
	go b.runDailyStatsReporting()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)
	me, err := b.api.GetMe()
	if err != nil {
		b.logger.Error("Failed to get bot info", "error", err)
		return
	}

	for update := range updates {
		if update.Message == nil {
			continue
		}
		if update.Message.ReplyToMessage != nil { // Ignore replies
			continue
		}
		if update.Message.From.ID == me.ID { // Ignore self
			continue
		}

		ctx := context.Background()
		userID := fmt.Sprintf("user%d", update.Message.From.ID)
		channelID := update.Message.Chat.ID

		// Check admin rights for this chat
		adminRights := b.checkAdminRights(channelID, me.ID)
		b.logger.Debug("Bot admin status for chat", "chatID", channelID, "isAdmin", adminRights)

		// Only process messages of type "message"
		if update.Message.Text != "" {
			uid, _ := strconv.Atoi(strings.TrimPrefix(userID, "user"))
			if int64(uid) == channelID && !b.whitelistChannels[channelID] {
				b.logger.Debug("Skipping self message", "userID", uid, "channelID", channelID)
				replyMsg := tgbotapi.NewMessage(channelID, "Sorry, it doesn't work this way. Add me to your channel as an admin.")
				replyMsg.ReplyToMessageID = update.Message.MessageID
				_, err := b.api.Send(replyMsg)
				if err != nil {
					b.logger.Error("Failed to send reply message", "error", err)
				}
				continue
			}

			// Check if the channel is whitelisted
			if len(b.whitelistChannels) > 0 && !b.whitelistChannels[channelID] {
				b.logger.Debug("Skipping non-whitelisted channel", "channelID", channelID)
				continue
			}

			key := fmt.Sprintf("%s:%d", strings.TrimPrefix(userID, "user"), channelID)
			count, err := b.redis.Get(ctx, key).Int()
			if err != nil && err != redis.Nil {
				b.logger.Error("Error retrieving count from Redis", "error", err)
				continue
			}
			// b.logger.Debug("User message count", "userID", uid, "channelID", channelID, "count", count)

			if count >= b.config.NewUserThreshold {
				// b.logger.Debug("Skipping old user", "userID", uid, "channelID", channelID, "count", count)
				continue
			}

			// Increment checked messages count
			b.incrementStat(channelID, "checkedCount")

			// Hash the message
			messageHash := b.hashMessage(update.Message.Text)
			b.logger.Debug("Message hash", "userID", uid, "channelID", channelID, "hash", messageHash)

			// Check if the message hash is in the Redis cache
			isSpam, err := b.isSpamMessage(ctx, messageHash)
			if err != nil {
				b.logger.Error("Error checking spam cache", "error", err)
				continue
			}

			if isSpam {
				// Increment cache hit count
				b.incrementStat(channelID, "cacheHitCount")
				// Immediately delete the message if it's in the spam cache
				if adminRights.CanDeleteMessages {
					deleteMsg := tgbotapi.NewDeleteMessage(channelID, update.Message.MessageID)
					_, err := b.api.Request(deleteMsg)
					if err != nil {
						b.logger.Error("Failed to delete cached spam message", "error", err, "messageID", update.Message.MessageID)
					} else {
						b.logger.Info("Deleted cached spam message", "messageID", update.Message.MessageID, "userID", uid, "channelID", channelID)
					}
				}
				continue
			}

			// Check for spam
			processed, err := b.checkForSpamWithRetry(update.Message.Text, 3, 100*time.Millisecond)
			if err != nil {
				b.logger.Error("Error checking for spam after retries", "error", err)
				continue
			}

			// Increment AI checked count
			b.incrementStat(channelID, "aiCheckedCount")

			b.logger.Debug("Spam check result",
				"userID", uid,
				"channelID", channelID,
				"spamScore", processed.SpamScore,
				"reasoning", processed.Reasoning)

			if processed.SpamScore <= b.config.Threshold {
				// Increment the count for the user
				_, err = b.redis.Incr(ctx, key).Result()
				if err != nil {
					b.logger.Error("Error incrementing count in Redis", "error", err)
				}
				if logChannelID, exists := b.config.LogChannels[channelID]; exists {
					forwardMsg := tgbotapi.NewForward(logChannelID, channelID, update.Message.MessageID)
					_, err := b.api.Send(forwardMsg)
					if err != nil {
						b.logger.Error("Failed to forward spam message to log channel", "error", err, "messageID", update.Message.MessageID, "logChannelID", logChannelID)
					} else {
						b.logger.Info("Forwarded non-spam message to log channel", "messageID", update.Message.MessageID, "userID", uid, "channelID", channelID, "logChannelID", logChannelID, "spamScore", processed.SpamScore)
					}

					// Send additional information to the log channel
					logMessage := fmt.Sprintf("✅ New user check:\nUser ID: %d\nChannel ID: %d\nSpam Score: %.2f / %.2f \nReasoning: %s", uid, channelID, processed.SpamScore, b.config.Threshold, processed.Reasoning)
					logMsg := tgbotapi.NewMessage(logChannelID, logMessage)
					_, err = b.api.Send(logMsg)
					if err != nil {
						b.logger.Error("Failed to send log message to log channel", "error", err, "logChannelID", logChannelID)
					}
				}
				continue
			}

			// Increment spam count
			b.incrementStat(channelID, "spamCount")

			// Add the message hash to the Redis spam cache
			if err := b.addSpamMessage(ctx, messageHash); err != nil {
				b.logger.Error("Failed to add spam message to cache", "error", err)
			}

			b.handleSpamMessage(update.Message, channelID, int64(uid), adminRights, processed.SpamScore)
		}
	}
}

func (b *Bot) hashMessage(message string) string {
	hash := sha256.Sum256([]byte(message))
	return hex.EncodeToString(hash[:])
}

func (b *Bot) isSpamMessage(ctx context.Context, hash string) (bool, error) {
	exists, err := b.redis.Exists(ctx, "spam:"+hash).Result()
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}

func (b *Bot) addSpamMessage(ctx context.Context, hash string) error {
	// Store the hash with an expiration time (e.g., 24 hours)
	return b.redis.Set(ctx, "spam:"+hash, 1, 24*31*time.Hour).Err()
}

func (b *Bot) handleSpamMessage(message *tgbotapi.Message, channelID, userID int64, adminRights AdminRights, spamScore float64) {
	// Forward the message to the log channel
	if logChannelID, exists := b.config.LogChannels[channelID]; exists {
		forwardMsg := tgbotapi.NewForward(logChannelID, channelID, message.MessageID)
		_, err := b.api.Send(forwardMsg)
		if err != nil {
			b.logger.Error("Failed to forward spam message to log channel", "error", err, "messageID", message.MessageID, "logChannelID", logChannelID)
		} else {
			b.logger.Info("Forwarded spam message to log channel", "messageID", message.MessageID, "userID", userID, "channelID", channelID, "logChannelID", logChannelID)
		}
	}

	action := "👻 Spam detected and logged"
	if adminRights.CanDeleteMessages {
		action = "🤡 Spam detected and deleted"
		deleteMsg := tgbotapi.NewDeleteMessage(channelID, message.MessageID)
		_, err := b.api.Request(deleteMsg)
		if err != nil {
			b.logger.Error("Failed to delete spam message", "error", err, "messageID", message.MessageID)
		} else {
			b.logger.Info("Deleted spam message", "messageID", message.MessageID, "userID", userID, "channelID", channelID)
		}
	}

	if adminRights.CanRestrictMembers {
		action += "\n👩‍⚖️User banned"
		restrictConfig := tgbotapi.RestrictChatMemberConfig{
			ChatMemberConfig: tgbotapi.ChatMemberConfig{
				ChatID: channelID,
				UserID: userID,
			},
		}
		_, err := b.api.Request(restrictConfig)
		if err != nil {
			b.logger.Error("Failed to restrict user", "error", err, "userID", userID, "channelID", channelID)
		} else {
			b.logger.Info("Restricted user", "userID", userID, "channelID", channelID)
		}
	}

	if logChannelID, exists := b.config.LogChannels[channelID]; exists {
		// Send additional information to the log channel
		logMessage := fmt.Sprintf(action+"\nUser ID: %d\nChannel ID: %d\nSpam Score: %.2f/%.2f", userID, channelID, spamScore, b.config.Threshold)
		logMsg := tgbotapi.NewMessage(logChannelID, logMessage)
		_, err := b.api.Send(logMsg)
		if err != nil {
			b.logger.Error("Failed to send log message to log channel", "error", err, "logChannelID", logChannelID)
		}
	}
}

type AdminRights struct {
	CanDeleteMessages  bool
	CanRestrictMembers bool
}

func (b *Bot) checkAdminRights(chatID int64, botID int64) AdminRights {
	b.cacheMutex.Lock()
	defer b.cacheMutex.Unlock()

	// Check if the admin rights are already in the cache
	if adminRights, exists := b.adminCache[chatID]; exists {
		return adminRights
	}

	// If not in cache, fetch the admin rights
	adminRights := AdminRights{}

	me, err := b.api.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: chatID,
			UserID: botID,
		},
	})

	if err != nil {
		b.logger.Error("Error getting chat member", "error", err, "chatID", chatID, "botID", botID)
		return adminRights
	}

	adminRights.CanDeleteMessages = me.CanDeleteMessages
	adminRights.CanRestrictMembers = me.CanRestrictMembers

	// Update the cache
	b.adminCache[chatID] = adminRights

	return adminRights
}

func (b *Bot) clearAdminCacheRoutine() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.clearAdminCache()
		case <-b.stopChan:
			return
		}
	}
}

func (b *Bot) clearAdminCache() {
	b.cacheMutex.Lock()
	defer b.cacheMutex.Unlock()
	b.adminCache = make(map[int64]AdminRights)
}

func (b *Bot) Stop() {
	close(b.stopChan)
	b.redis.Close()
}

func (b *Bot) checkForSpamWithRetry(text string, maxRetries int, retryDelay time.Duration) (*ai.Result, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		processed, err := ai.ProcessRecord(text, b.config.Prompt, b.aiprovider)
		if err == nil {
			return &processed, nil
		}
		lastErr = err
		b.logger.Warn("Spam check failed, retrying", "attempt", i+1, "error", err)
		time.Sleep(retryDelay)
	}
	return nil, fmt.Errorf("failed to check for spam after %d attempts: %w", maxRetries, lastErr)
}

// New functions for statistics tracking

func (b *Bot) incrementStat(channelID int64, statType string) {
	ctx := context.Background()
	key := fmt.Sprintf("%s%d", b.statsKeys[statType], channelID)
	err := b.redis.Incr(ctx, key).Err()
	if err != nil {
		b.logger.Error("Failed to increment stat", "error", err, "statType", statType, "channelID", channelID)
	}
}

func (b *Bot) getStats(channelID int64) map[string]int64 {
	ctx := context.Background()
	stats := make(map[string]int64)

	for statType, keyPrefix := range b.statsKeys {
		key := fmt.Sprintf("%s%d", keyPrefix, channelID)
		count, err := b.redis.Get(ctx, key).Int64()
		if err != nil && err != redis.Nil {
			b.logger.Error("Failed to get stat", "error", err, "statType", statType, "channelID", channelID)
			continue
		}
		stats[statType] = count
	}

	return stats
}

func (b *Bot) resetStats(channelID int64) {
	ctx := context.Background()
	for _, keyPrefix := range b.statsKeys {
		key := fmt.Sprintf("%s%d", keyPrefix, channelID)
		err := b.redis.Del(ctx, key).Err()
		if err != nil {
			b.logger.Error("Failed to reset stat", "error", err, "key", key, "channelID", channelID)
		}
	}
}

func (b *Bot) runDailyStatsReporting() {
	// ticker := time.NewTicker(1 * time.Hour)
	// defer ticker.Stop()

	// for {
	// 	select {
	// 	case <-ticker.C:
	// 		b.sendDailyStats()
	// 	case <-b.stopChan:
	// 		return
	// 	}
	// }
	const targetHour = 14 // 1 PM UTC
	for {
		now := time.Now().UTC()
		next := time.Date(now.Year(), now.Month(), now.Day(), targetHour, 0, 0, 0, time.UTC)

		if now.Hour() >= targetHour {
			next = next.Add(24 * time.Hour)
		}

		b.logger.Info("Scheduled next daily stats report", "next", next)

		time.Sleep(time.Until(next))

		b.logger.Info("Running daily stats reporting")
		b.sendDailyStats()

		// Sleep for a short duration to prevent multiple executions
		time.Sleep(time.Minute)
	}
}

func (b *Bot) sendDailyStats() {
	for channelID, logChannelID := range b.config.LogChannels {
		stats := b.getStats(channelID)
		if stats["checkedCount"] == 0 {
			continue
		}

		message := fmt.Sprintf("📊 Daily Stats for Channel %d\n\n", channelID)
		spamCount := stats["spamCount"] + stats["cacheHitCount"]
		message += fmt.Sprintf("✉️ Checked: %d \n🚫 Spam: %d (%.1f%%)\n",
			stats["checkedCount"],
			spamCount,
			float64(spamCount)/float64(stats["checkedCount"])*100)
		message += fmt.Sprintf("🎯 Cache Hits: %d \n🤖 AI Checks: %d\n",
			stats["cacheHitCount"],
			stats["aiCheckedCount"])

		msg := tgbotapi.NewMessage(logChannelID, message)
		_, err := b.api.Send(msg)
		if err != nil {
			b.logger.Error("Failed to send daily stats", "error", err, "channelID", channelID, "logChannelID", logChannelID)
		}

		// Reset stats after sending
		b.resetStats(channelID)
	}
}
