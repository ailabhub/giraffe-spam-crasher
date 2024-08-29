package bot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/ailabhub/giraffe-spam-crasher/internal/consts"
	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/redis/go-redis/v9"
)

type SpamProcessor interface {
	CheckForSpam(ctx context.Context, message structs.Message) (structs.SpamCheckResult, error)
}

type Bot struct {
	api               *tgbotapi.BotAPI
	redis             *redis.Client
	spamProcessor     SpamProcessor
	config            *Config
	adminCache        map[int64]AdminRights
	cacheMutex        sync.RWMutex
	stopChan          chan struct{}
	whitelistChannels map[int64]bool
}

type Config struct {
	Threshold         float64
	NewUserThreshold  int
	WhitelistChannels []int64
	LogChannels       map[int64]int64
}

func New(rdb *redis.Client, spamProcessor SpamProcessor, config *Config) (*Bot, error) {
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
		spamProcessor:     spamProcessor,
		config:            config,
		adminCache:        make(map[int64]AdminRights),
		stopChan:          make(chan struct{}),
		whitelistChannels: whitelistMap,
	}

	return bot, nil
}
func (b *Bot) Start() { //nolint:gocyclo,gocognit
	slog.Info("Authorized on account", "username", b.api.Self.UserName)
	slog.Info("Config", "threshold", b.config.Threshold, "newUserThreshold", b.config.NewUserThreshold, "whitelistChannels", b.config.WhitelistChannels)
	slog.Info("Starting bot")

	// Start the cache clearing goroutine
	go b.clearAdminCacheRoutine()

	// Start the statistics reporting goroutine
	go b.runDailyStatsReporting()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := b.api.GetUpdatesChan(u)
	me, err := b.api.GetMe()
	if err != nil {
		slog.Error("Failed to get bot info", "error", err)
		return
	}

	for update := range updates {
		b.handleUpdate(update, &me)
	}
}

func (b *Bot) handleUpdate(update tgbotapi.Update, me *tgbotapi.User) {
	if update.Message == nil || update.Message.ReplyToMessage != nil || update.Message.From.ID == me.ID {
		return
	}

	slog.Info("Received message", "messageID", update.Message.MessageID, "userID", update.Message.From.ID, "channelID", update.Message.Chat.ID, "text", update.Message.Text)

	channelID := update.Message.Chat.ID
	ctx := context.Background()

	if b.isSelfMessage(update.Message, channelID) {
		b.sendSelfMessageWarning(update.Message)
		return
	}

	if !b.isWhitelistedChannel(channelID) {
		slog.Debug("Skipping non-whitelisted channel", "channelID", channelID)
		return
	}

	if b.isNewUser(ctx, update.Message) {
		b.incrementStat(channelID, consts.StatKeyCheckedCount)
		b.processTelegramMessage(ctx, update.Message)
	}
}

func (b *Bot) isSelfMessage(message *tgbotapi.Message, channelID int64) bool {
	userID := message.From.ID
	return userID == channelID && !b.whitelistChannels[channelID]
}

func (b *Bot) sendSelfMessageWarning(message *tgbotapi.Message) {
	replyMsg := tgbotapi.NewMessage(message.Chat.ID, "Sorry, it doesn't work this way. Add me to your channel as an admin.")
	replyMsg.ReplyToMessageID = message.MessageID
	if _, err := b.api.Send(replyMsg); err != nil {
		slog.Error("Failed to send reply message", "error", err)
	}
}

func (b *Bot) isWhitelistedChannel(channelID int64) bool {
	return len(b.whitelistChannels) == 0 || b.whitelistChannels[channelID]
}

func (b *Bot) isNewUser(ctx context.Context, message *tgbotapi.Message) bool {
	channelID := message.Chat.ID
	key := fmt.Sprintf("%d:%d", message.From.ID, channelID)
	count, err := b.redis.Get(ctx, key).Int()
	if err != nil && !errors.Is(err, redis.Nil) {
		slog.Error("Error retrieving count from Redis", "error", err)
		return false
	}
	return count < b.config.NewUserThreshold
}

func (b *Bot) processTelegramMessage(ctx context.Context, telegramMessage *tgbotapi.Message) {
	var processed structs.SpamCheckResult

	channelID := telegramMessage.Chat.ID

	message, err := b.fromTGToInternalMessage(ctx, telegramMessage)
	if err != nil {
		slog.Error("Error converting message", "error", err)
		return
	}

	processed, err = b.spamProcessor.CheckForSpam(ctx, message)
	if err != nil {
		slog.Error("Error checking for spam after retries", "error", err)
		return
	}

	if processed.FromCache {
		b.incrementStat(channelID, consts.StatKeyCacheHitCount)
	} else {
		b.incrementStat(channelID, consts.StatKeyAiCheckedCount)
	}

	slog.Debug("Spam check result", "userID", telegramMessage.From.ID, "channelID", telegramMessage, "spamScore", processed.SpamScore, "reasoning", processed.Reasoning)

	if processed.SpamScore <= b.config.Threshold {
		b.incrementUserMessageCount(telegramMessage)
		b.forwardMessageToLogChannel(telegramMessage, processed, channelID, processed.SpamScore, false)
	} else {
		b.incrementStat(channelID, "spamCount")
		b.handleSpamMessage(telegramMessage, channelID, telegramMessage.From.ID, b.checkAdminRights(channelID, b.api.Self.ID), processed.SpamScore)
	}
}

func (b *Bot) downloadImage(url string) ([]byte, error) {
	// nolint
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func (b *Bot) incrementUserMessageCount(message *tgbotapi.Message) {
	ctx := context.Background()
	channelID := message.Chat.ID
	key := fmt.Sprintf("%d:%d", message.From.ID, channelID)
	if _, err := b.redis.Incr(ctx, key).Result(); err != nil {
		slog.Error("Error incrementing count in Redis", "error", err)
	}
}

func (b *Bot) forwardMessageToLogChannel(message *tgbotapi.Message, processed structs.SpamCheckResult, channelID int64, spamScore float64, isSpam bool) {
	if logChannelID, exists := b.config.LogChannels[channelID]; exists {
		forwardMsg := tgbotapi.NewForward(logChannelID, channelID, message.MessageID)
		if _, err := b.api.Send(forwardMsg); err != nil {
			slog.Error("Failed to forward message to log channel", "error", err, "messageID", message.MessageID, "logChannelID", logChannelID)
		}

		action := "✅ New user check"
		if isSpam {
			action = "🤡 Spam detected and deleted"
		}

		logMessage := fmt.Sprintf("%s:\nUser ID: %d\nChannel ID: %d\nSpam Score: %.2f / %.2f \nReasoning: \n%s", action, message.From.ID, channelID, spamScore, b.config.Threshold, processed.Reasoning)

		logMsg := tgbotapi.NewMessage(logChannelID, logMessage)
		if _, err := b.api.Send(logMsg); err != nil {
			slog.Error("Failed to send log message to log channel", "error", err, "logChannelID", logChannelID)
		}
	}
}

func (b *Bot) handleSpamMessage(message *tgbotapi.Message, channelID, userID int64, adminRights AdminRights, spamScore float64) {
	// Forward the message to the log channel
	if logChannelID, exists := b.config.LogChannels[channelID]; exists {
		forwardMsg := tgbotapi.NewForward(logChannelID, channelID, message.MessageID)
		_, err := b.api.Send(forwardMsg)
		if err != nil {
			slog.Error("Failed to forward spam message to log channel", "error", err, "messageID", message.MessageID, "logChannelID", logChannelID)
		} else {
			slog.Info("Forwarded spam message to log channel", "messageID", message.MessageID, "userID", userID, "channelID", channelID, "logChannelID", logChannelID)
		}
	}

	action := "👻 Spam detected and logged"
	if adminRights.CanDeleteMessages {
		action = "🤡 Spam detected and deleted"
		deleteMsg := tgbotapi.NewDeleteMessage(channelID, message.MessageID)
		_, err := b.api.Request(deleteMsg)
		if err != nil {
			slog.Error("Failed to delete spam message", "error", err, "messageID", message.MessageID)
		} else {
			slog.Info("Deleted spam message", "messageID", message.MessageID, "userID", userID, "channelID", channelID)
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
			slog.Error("Failed to restrict user", "error", err, "userID", userID, "channelID", channelID)
		} else {
			slog.Info("Restricted user", "userID", userID, "channelID", channelID)
		}
	}

	if logChannelID, exists := b.config.LogChannels[channelID]; exists {
		// Send additional information to the log channel
		logMessage := fmt.Sprintf(action+"\nUser ID: %d\nChannel ID: %d\nSpam Score: %.2f/%.2f", userID, channelID, spamScore, b.config.Threshold)
		logMsg := tgbotapi.NewMessage(logChannelID, logMessage)
		_, err := b.api.Send(logMsg)
		if err != nil {
			slog.Error("Failed to send log message to log channel", "error", err, "logChannelID", logChannelID)
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
		slog.Error("Error getting chat member", "error", err, "chatID", chatID, "botID", botID)
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

func (b *Bot) incrementStat(channelID int64, statType consts.StatKey) {
	ctx := context.Background()
	key := fmt.Sprintf("%s%d", consts.StatsKeys[statType], channelID)
	err := b.redis.Incr(ctx, key).Err()
	if err != nil {
		slog.Error("Failed to increment stat", "error", err, "statType", statType, "channelID", channelID)
	}
}

func (b *Bot) getStats(channelID int64) map[consts.StatKey]int64 {
	ctx := context.Background()
	stats := make(map[consts.StatKey]int64)

	for statType, keyPrefix := range consts.StatsKeys {
		key := fmt.Sprintf("%s%d", keyPrefix, channelID)
		count, err := b.redis.Get(ctx, key).Int64()
		if err != nil && !errors.Is(err, redis.Nil) {
			slog.Error("Failed to get stat", "error", err, "statType", statType, "channelID", channelID)
			continue
		}
		stats[statType] = count
	}

	return stats
}

func (b *Bot) resetStats(channelID int64) {
	ctx := context.Background()
	for _, keyPrefix := range consts.StatsKeys {
		key := fmt.Sprintf("%s%d", keyPrefix, channelID)
		err := b.redis.Del(ctx, key).Err()
		if err != nil {
			slog.Error("Failed to reset stat", "error", err, "key", key, "channelID", channelID)
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

		slog.Info("Scheduled next daily stats report", "next", next)

		time.Sleep(time.Until(next))

		slog.Info("Running daily stats reporting")
		b.sendDailyStats()

		// Sleep for a short duration to prevent multiple executions
		time.Sleep(time.Minute)
	}
}

func (b *Bot) sendDailyStats() {
	for channelID, logChannelID := range b.config.LogChannels {
		stats := b.getStats(channelID)
		if stats[consts.StatKeyCheckedCount] == 0 {
			continue
		}

		message := fmt.Sprintf("📊 Daily Stats for Channel %d\n\n", channelID)
		spamCount := stats[consts.StatKeySpamCount] + stats[consts.StatKeyCacheHitCount]
		message += fmt.Sprintf("✉️ Checked: %d \n🚫 Spam: %d (%.1f%%)\n",
			stats[consts.StatKeyCheckedCount],
			spamCount,
			float64(spamCount)/float64(stats[consts.StatKeyCheckedCount])*100)
		message += fmt.Sprintf("🎯 Cache Hits: %d \n🤖 AI Checks: %d\n",
			stats[consts.StatKeyCacheHitCount],
			stats[consts.StatKeyAiCheckedCount])

		msg := tgbotapi.NewMessage(logChannelID, message)
		_, err := b.api.Send(msg)
		if err != nil {
			slog.Error("Failed to send daily stats", "error", err, "channelID", channelID, "logChannelID", logChannelID)
		}

		// Reset stats after sending
		b.resetStats(channelID)
	}
}

func (b *Bot) fromTGToInternalMessage(ctx context.Context, tgMessage *tgbotapi.Message) (structs.Message, error) {
	message := structs.Message{
		Text: tgMessage.Text,
	}

	if len(tgMessage.Photo) > 0 {
		// телега дает 3 размера фотографии, от низкого до высокого качества, берем самое высокое качество и ресайзим вручную
		// TODO? возможно стоит не ресайзить, а брать оригинал низкого качества
		imageData, err := b.downloadTelegramImage(ctx, tgMessage.Photo[len(tgMessage.Photo)-1])
		if err != nil {
			return structs.Message{}, fmt.Errorf("error downloading image: %w", err)
		}

		message.Text = tgMessage.Caption
		message.Images = append(message.Images, imageData)
	}

	return message, nil
}

func (b *Bot) downloadTelegramImage(_ context.Context, photo tgbotapi.PhotoSize) ([]byte, error) {
	fileConfig := tgbotapi.FileConfig{FileID: photo.FileID}
	file, err := b.api.GetFile(fileConfig)
	if err != nil {
		return nil, fmt.Errorf("error getting file info: %w", err)
	}

	imageURL := file.Link(b.api.Token)
	imageData, err := b.downloadImage(imageURL)
	if err != nil {
		return nil, fmt.Errorf("error downloading image: %w", err)
	}

	return imageData, nil
}
