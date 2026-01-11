package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/OvyFlash/telegram-bot-api/v6"
	"github.com/ailabhub/giraffe-spam-crasher/internal/consts"
	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
	"github.com/redis/go-redis/v9"
)

type SpamProcessor interface {
	CheckForSpam(ctx context.Context, message *structs.Message, useCache bool) (structs.SpamCheckResult, error)
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
	logger            *slog.Logger
}

type Config struct {
	Threshold         float64
	NewUserThreshold  int
	WhitelistChannels []int64
	LogChannels       map[int64]int64
	InstantBan        bool
	SettingByChannel  map[int64]Setting
}

type Setting struct {
	BanUserThreshold int
}

func New(rdb *redis.Client, spamProcessor SpamProcessor, config *Config, logger *slog.Logger) (*Bot, error) {
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
		logger:            logger,
	}

	return bot, nil
}
func (b *Bot) Start() { //nolint:gocyclo,gocognit
	b.logger.Info("Authorized on account", "username", b.api.Self.UserName)
	b.logger.Info("Config", "threshold", b.config.Threshold, "newUserThreshold", b.config.NewUserThreshold, "whitelistChannels", b.config.WhitelistChannels, "instantBan", b.config.InstantBan)
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
		go b.handleUpdate(update, &me)
	}
}

func (b *Bot) handleUpdate(update tgbotapi.Update, me *tgbotapi.User) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error("Recovered from panic in handleUpdate",
				"error", r,
				"stack", string(debug.Stack()))
		}
	}()

	if update.Message != nil {
		b.handleNewMessage(update.Message, me)
	}
	if update.EditedMessage != nil {
		b.handleEditedMessage(update.EditedMessage, me)
	}
}

func (b *Bot) handleNewMessage(tgMessage *tgbotapi.Message, me *tgbotapi.User) {
	if tgMessage.From == nil || tgMessage.From.ID == me.ID { // || tgMessage.ReplyToMessage != nil
		return
	}

	ctx := context.Background()

	message, err := b.fromTGToInternalMessage(ctx, tgMessage)
	if err != nil {
		b.logger.Error("Error converting message", "error", err)
		return
	}

	if message.IsEmpty() {
		jsonMessage, err := json.Marshal(message)
		if err != nil {
			b.logger.Error("json.Marshal", "error", err)
		}
		b.logger.Warn("Message has no text or image, skipping spam check", "message", string(jsonMessage))
		return
	}

	if b.isSelfMessage(&message) {
		b.sendSelfMessageWarning(tgMessage)
		return
	}

	if !b.isWhitelistedChannel(message.ChannelID) {
		b.logger.Debug("Skipping non-whitelisted channel", "channelID", message.ChannelID)
		return
	}

	privateMessage := message.UserID == message.ChannelID
	isNewUser := true
	if !privateMessage {
		isNewUser = b.isNewUser(ctx, &message)
		if !isNewUser {
			return
		}
	}

	if !privateMessage {
		b.incrementStat(message.ChannelID, consts.StatKeyCheckedCount)
	}

	processed, err := b.processTelegramMessage(ctx, &message, privateMessage, false, !privateMessage)
	if err != nil {
		return
	}

	if !privateMessage && isNewUser && processed.SpamScore <= b.config.Threshold {
		b.watchMessageForEdits(ctx, &message)
	}
}

func (b *Bot) handleEditedMessage(tgMessage *tgbotapi.Message, me *tgbotapi.User) {
	if tgMessage.From == nil || tgMessage.From.ID == me.ID { // || tgMessage.ReplyToMessage != nil
		return
	}

	ctx := context.Background()

	message, err := b.fromTGToInternalMessage(ctx, tgMessage)
	if err != nil {
		b.logger.Error("Error converting message", "error", err)
		return
	}

	if message.IsEmpty() {
		jsonMessage, err := json.Marshal(message)
		if err != nil {
			b.logger.Error("json.Marshal", "error", err)
		}
		b.logger.Warn("Message has no text or image, skipping spam check", "message", string(jsonMessage))
		return
	}

	if b.isSelfMessage(&message) {
		return
	}

	if !b.isWhitelistedChannel(message.ChannelID) {
		b.logger.Debug("Skipping non-whitelisted channel", "channelID", message.ChannelID)
		return
	}

	privateMessage := message.UserID == message.ChannelID
	if privateMessage {
		return
	}

	watched, err := b.isMessageWatched(ctx, message.ChannelID, message.MessageID)
	if err != nil {
		b.logger.Error("Error checking edit watch", "error", err, "channelID", message.ChannelID, "messageID", message.MessageID)
		return
	}
	if !watched {
		return
	}

	_, err = b.processTelegramMessage(ctx, &message, false, true, false)
	if err != nil {
		return
	}
}

func (b *Bot) isSelfMessage(message *structs.Message) bool {
	userID := message.UserID
	return userID == message.ChannelID && !b.whitelistChannels[message.ChannelID]
}

func (b *Bot) sendSelfMessageWarning(message *tgbotapi.Message) {
	replyMsg := tgbotapi.NewMessage(message.Chat.ID, "Sorry, it doesn't work this way. Add me to your channel as an admin.")
	replyMsg.ReplyParameters = tgbotapi.ReplyParameters{
		MessageID: message.MessageID,
	}
	if _, err := b.api.Send(replyMsg); err != nil {
		b.logger.Error("Failed to send reply message", "error", err)
	}
}

func (b *Bot) isWhitelistedChannel(channelID int64) bool {
	return len(b.whitelistChannels) == 0 || b.whitelistChannels[channelID]
}

func (b *Bot) isNewUser(ctx context.Context, message *structs.Message) bool {
	channelID := message.ChannelID
	key := fmt.Sprintf("%d:%d", message.UserID, channelID)
	count, err := b.redis.Get(ctx, key).Int()
	if err != nil && !errors.Is(err, redis.Nil) {
		b.logger.Error("Error retrieving count from Redis", "error", err)
		return false
	}
	return count < b.config.NewUserThreshold
}

func (b *Bot) processTelegramMessage(ctx context.Context, message *structs.Message, privateMessage bool, isEdit bool, useCache bool) (structs.SpamCheckResult, error) {
	var processed structs.SpamCheckResult

	processed, err := b.spamProcessor.CheckForSpam(ctx, message, useCache)
	if err != nil {
		b.logger.Error("Error checking for spam after retries", "error", err)
		return structs.SpamCheckResult{}, err
	}

	if processed.FromCache {
		b.incrementStat(message.ChannelID, consts.StatKeyCacheHitCount)
	} else {
		b.incrementStat(message.ChannelID, consts.StatKeyAiCheckedCount)
	}

	b.logger.Debug(
		"Spam check result",
		"userID",
		message.UserID,
		"channelID",
		message.ChannelID,
		"spamScore",
		processed.SpamScore,
		"reasoning",
		processed.Reasoning,
	)

	if privateMessage {
		b.sendCheckResultMessage("Checked", message, processed, time.Time{}, message.ChannelID, isEdit)
		return processed, nil
	}
	if processed.SpamScore <= b.config.Threshold {
		if !isEdit {
			b.incrementUserMessageCount(message)
		}
		b.forwardMessageToLogChannel(message, processed, false, isEdit)
	} else {
		b.incrementStat(message.ChannelID, consts.StatKeySpamCount)
		b.handleSpamMessage(message, b.checkAdminRights(message.ChannelID, b.api.Self.ID), processed, isEdit)
	}
	return processed, nil
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

func (b *Bot) incrementUserMessageCount(message *structs.Message) {
	ctx := context.Background()
	channelID := message.ChannelID
	key := fmt.Sprintf("%d:%d", message.UserID, channelID)
	if _, err := b.redis.Incr(ctx, key).Result(); err != nil {
		b.logger.Error("Error incrementing count in Redis", "error", err)
	}
}

func (b *Bot) watchMessageForEdits(ctx context.Context, message *structs.Message) {
	key := b.editWatchKey(message.ChannelID, message.MessageID)
	if err := b.redis.Set(ctx, key, message.UserID, consts.EditWatchTTL).Err(); err != nil {
		b.logger.Error("Failed to set edit watch", "error", err, "channelID", message.ChannelID, "messageID", message.MessageID)
	}
}

func (b *Bot) isMessageWatched(ctx context.Context, channelID int64, messageID int64) (bool, error) {
	key := b.editWatchKey(channelID, messageID)
	exists, err := b.redis.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

func (b *Bot) editWatchKey(channelID int64, messageID int64) string {
	return fmt.Sprintf("%s%d:%d", consts.RedisEditWatchKeyPrefix, channelID, messageID)
}

func (b *Bot) sendCheckResultMessage(action string, message *structs.Message, processed structs.SpamCheckResult, deletedTime time.Time, channelID int64, isEdit bool) {
	if isEdit {
		action = "✏️ Edit " + action
	}
	logMessage := fmt.Sprintf("%s:\nUser ID: %d\nChannel ID: %d\nSpam Score: %.2f / %.2f \nReasoning: \n%s", action, message.UserID, message.ChannelID, processed.SpamScore, b.config.Threshold, processed.Reasoning)
	if !deletedTime.IsZero() {

		logMessage += fmt.Sprintf(
			"\nTime to delete: %s/%s seconds",
			formatDuration(message.ReceivedAt.UTC().Sub(message.MessageTime)),
			formatDuration(deletedTime.UTC().Sub(message.MessageTime)),
		)
	}

	logMsg := tgbotapi.NewMessage(channelID, logMessage)
	if _, err := b.api.Send(logMsg); err != nil {
		b.logger.Error("Failed to send log message to log channel", "error", err, "channelID", channelID)
	}
}

func (b *Bot) forwardMessageToLogChannel(message *structs.Message, processed structs.SpamCheckResult, isSpam bool, isEdit bool) {
	if logChannelID, exists := b.config.LogChannels[message.ChannelID]; exists {
		forwardMsg := tgbotapi.NewForward(logChannelID, message.ChannelID, int(message.MessageID))
		if _, err := b.api.Send(forwardMsg); err != nil {
			b.logger.Error("Failed to forward message to log channel", "error", err, "messageID", message.MessageID, "logChannelID", logChannelID)
		}

		action := "✅ New user check"
		if isSpam {
			action = "🤡 Spam detected and deleted"
		}
		if isEdit && !isSpam {
			action = "Check"
		}

		b.sendCheckResultMessage(action, message, processed, time.Time{}, logChannelID, isEdit)
	}
}

func (b *Bot) handleSpamMessage(message *structs.Message, adminRights AdminRights, processed structs.SpamCheckResult, isEdit bool) {
	action := "👻 Spam detected and logged"

	// TODO: count by user, not by message
	userSpamMessageCount := 0
	if processed.FromCache {
		userSpamMessageCount += 1
	}
	// отправляем в лог канал только если сообщение не из кеша
	if !processed.FromCache {
		if logChannelID, exists := b.config.LogChannels[message.ChannelID]; exists {
			forwardMsg := tgbotapi.NewForward(logChannelID, message.ChannelID, int(message.MessageID))
			_, err := b.api.Send(forwardMsg)
			if err != nil {
				b.logger.Error("Failed to forward spam message to log channel", "error", err, "messageID", message.MessageID, "logChannelID", logChannelID)
			} else {
				b.logger.Info("Forwarded spam message to log channel", "messageID", message.MessageID, "userID", message.UserID, "channelID", message.ChannelID, "logChannelID", logChannelID)
			}
		}
	}

	var deletedTime time.Time

	if adminRights.CanDeleteMessages {
		action = "🤡 Spam detected and deleted"
		deleteMsg := tgbotapi.NewDeleteMessage(message.ChannelID, int(message.MessageID))
		_, err := b.api.Request(deleteMsg)
		deletedTime = time.Now()
		if err != nil {
			b.logger.Error("Failed to delete spam message", "error", err, "messageID", message.MessageID)
		} else {
			b.logger.Info("Deleted spam message", "messageID", message.MessageID, "userID", message.UserID, "channelID", message.ChannelID)
		}
	}
	userWasRestricted := false
	banSetting := b.config.SettingByChannel[message.ChannelID]
	b.logger.Info("banSetting", "banSetting", banSetting, "userSpamMessageCount", userSpamMessageCount, "adminRights", adminRights)
	if adminRights.CanRestrictMembers && b.config.InstantBan {
		restrictConfig := tgbotapi.BanChatMemberConfig{
			ChatMemberConfig: tgbotapi.ChatMemberConfig{
				ChatConfig: tgbotapi.ChatConfig{
					ChatID: message.ChannelID,
				},
				UserID: message.UserID,
			},
		}
		_, err := b.api.Request(restrictConfig)
		if err != nil {
			slog.Error("Failed to ban user", "error", err, "userID", message.UserID, "channelID", message.ChannelID)
		} else {
			slog.Info("Banned user", "userID", message.UserID, "channelID", message.ChannelID)
			userWasRestricted = true
			action += "\n👩‍⚖️User banned"
		}
	} else if userSpamMessageCount >= banSetting.BanUserThreshold && adminRights.CanRestrictMembers {
		restrictConfig := tgbotapi.RestrictChatMemberConfig{
			ChatMemberConfig: tgbotapi.ChatMemberConfig{
				ChatConfig: tgbotapi.ChatConfig{
					ChatID: message.ChannelID,
				},
				UserID: message.UserID,
			},
		}
		_, err := b.api.Request(restrictConfig)
		if err != nil {
			b.logger.Error("Failed to restrict user", "error", err, "userID", message.UserID, "channelID", message.ChannelID)
		} else {
			b.logger.Info("Restricted user", "userID", message.UserID, "channelID", message.ChannelID)
			userWasRestricted = true
			action += "\n👩‍⚖️User banned"
		}
	}

	// отправляем в лог канал только если сообщение не из кеша или если юзер был забанен
	if !processed.FromCache || userWasRestricted {
		if logChannelID, exists := b.config.LogChannels[message.ChannelID]; exists {

			b.sendCheckResultMessage(action, message, processed, deletedTime, logChannelID, isEdit)
		}
	}
}

func formatDuration(d time.Duration) string {
	seconds := float64(d) / float64(time.Second)
	return fmt.Sprintf("%.1f", seconds)
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
			ChatConfig: tgbotapi.ChatConfig{
				ChatID: chatID,
			},
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

func (b *Bot) incrementStat(channelID int64, statType consts.StatKey) {
	ctx := context.Background()
	key := fmt.Sprintf("%s%d", consts.StatsKeys[statType], channelID)
	err := b.redis.Incr(ctx, key).Err()
	if err != nil {
		b.logger.Error("Failed to increment stat", "error", err, "statType", statType, "channelID", channelID)
	}
}

func (b *Bot) getStats(channelID int64) map[consts.StatKey]int64 {
	ctx := context.Background()
	stats := make(map[consts.StatKey]int64)

	for statType, keyPrefix := range consts.StatsKeys {
		key := fmt.Sprintf("%s%d", keyPrefix, channelID)
		count, err := b.redis.Get(ctx, key).Int64()
		if err != nil && !errors.Is(err, redis.Nil) {
			b.logger.Error("Failed to get stat", "error", err, "statType", statType, "channelID", channelID)
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
			b.logger.Error("Failed to reset stat", "error", err, "key", key, "channelID", channelID)
		}
	}
}

func (b *Bot) runDailyStatsReporting() {
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
		if stats[consts.StatKeyCheckedCount] == 0 {
			continue
		}

		message := fmt.Sprintf("📊 Daily Stats for Channel %d\n\n", channelID)
		spamCount := stats[consts.StatKeySpamCount]
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
			b.logger.Error("Failed to send daily stats", "error", err, "channelID", channelID, "logChannelID", logChannelID)
		}

		// Reset stats after sending
		b.resetStats(channelID)
	}
}

func (b *Bot) fromTGToInternalMessage(ctx context.Context, tgMessage *tgbotapi.Message) (structs.Message, error) {
	if tgMessage == nil {
		return structs.Message{}, errors.New("nil telegram message")
	}

	message := structs.Message{
		ChannelID:   tgMessage.Chat.ID,
		ChannelName: tgMessage.Chat.Title,
		MessageID:   int64(tgMessage.MessageID),
		UserID:      tgMessage.From.ID,
		UserName:    tgMessage.From.FirstName + " (@" + tgMessage.From.UserName + ")",
		ReceivedAt:  time.Now().UTC(),
		MessageTime: time.Unix(int64(tgMessage.Date), 0).UTC(),
	}

	if tgMessage.Quote != nil {
		message.Quote = tgMessage.Quote.Text
	}

	message.RawOriginal = tgMessage

	var textParts []string
	if tgMessage.Text != "" {
		textParts = append(textParts, tgMessage.Text)
	}
	if tgMessage.Caption != "" {
		textParts = append(textParts, tgMessage.Caption)
	}
	if keyboardText := inlineKeyboardText(tgMessage.ReplyMarkup); keyboardText != "" {
		textParts = append(textParts, keyboardText)
	}

	message.Text = strings.Join(textParts, "\n")

	if len(tgMessage.Photo) > 0 {
		// телега дает 3 размера фотографии в слайсе, от низкого до высокого качества, берем среднее
		imageData, err := b.downloadTelegramImage(ctx, tgMessage.Photo[len(tgMessage.Photo)/2])
		if err != nil {
			return structs.Message{}, fmt.Errorf("error downloading image: %w", err)
		}

		img := structs.Image(imageData)
		message.Image = &img
	}
	if message.Image == nil {
		if thumbnail := mediaThumbnail(tgMessage); thumbnail != nil {
			imageData, err := b.downloadTelegramImage(ctx, *thumbnail)
			if err != nil {
				return structs.Message{}, fmt.Errorf("error downloading thumbnail: %w", err)
			}

			img := structs.Image(imageData)
			message.Image = &img
		}
	}
	if message.Text == "" && message.Image == nil && !message.HasQuote() {
		if summary := mediaSummary(tgMessage); summary != "" {
			message.Text = summary
		}
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

func inlineKeyboardText(replyMarkup *tgbotapi.InlineKeyboardMarkup) string {
	if replyMarkup == nil {
		return ""
	}

	var buttons []string
	for _, row := range replyMarkup.InlineKeyboard {
		for _, button := range row {
			label := strings.TrimSpace(button.Text)
			target := ""

			if button.URL != nil && *button.URL != "" {
				target = *button.URL
			} else if button.LoginURL != nil && button.LoginURL.URL != "" {
				target = button.LoginURL.URL
			} else if button.WebApp != nil && button.WebApp.URL != "" {
				target = button.WebApp.URL
			} else if button.CallbackData != nil && *button.CallbackData != "" {
				target = "callback:" + *button.CallbackData
			} else if button.SwitchInlineQuery != nil && *button.SwitchInlineQuery != "" {
				target = "switch_inline_query:" + *button.SwitchInlineQuery
			} else if button.SwitchInlineQueryCurrentChat != nil && *button.SwitchInlineQueryCurrentChat != "" {
				target = "switch_inline_query_current_chat:" + *button.SwitchInlineQueryCurrentChat
			}

			if label == "" && target != "" {
				label = target
				target = ""
			}
			if label == "" && target == "" {
				continue
			}
			if target != "" {
				label = fmt.Sprintf("%s -> %s", label, target)
			}
			buttons = append(buttons, label)
		}
	}

	if len(buttons) == 0 {
		return ""
	}

	return "INLINE_BUTTONS:\n" + strings.Join(buttons, "\n")
}

func mediaThumbnail(message *tgbotapi.Message) *tgbotapi.PhotoSize {
	if message == nil {
		return nil
	}
	if message.Video != nil && message.Video.Thumbnail != nil {
		return message.Video.Thumbnail
	}
	if message.Animation != nil && message.Animation.Thumbnail != nil {
		return message.Animation.Thumbnail
	}
	if message.VideoNote != nil && message.VideoNote.Thumbnail != nil {
		return message.VideoNote.Thumbnail
	}
	if message.Document != nil && message.Document.Thumbnail != nil {
		return message.Document.Thumbnail
	}
	if message.Audio != nil && message.Audio.Thumbnail != nil {
		return message.Audio.Thumbnail
	}
	if message.Sticker != nil && message.Sticker.Thumbnail != nil {
		return message.Sticker.Thumbnail
	}
	return nil
}

func mediaSummary(message *tgbotapi.Message) string {
	if message == nil {
		return ""
	}

	switch {
	case message.Video != nil:
		return formatMediaSummary("video", message.Video.FileName, message.Video.MimeType, message.Video.Duration, message.Video.FileSize)
	case message.Animation != nil:
		return formatMediaSummary("animation", message.Animation.FileName, message.Animation.MimeType, message.Animation.Duration, message.Animation.FileSize)
	case message.VideoNote != nil:
		return formatMediaSummary("video_note", "", "", message.VideoNote.Duration, int64(message.VideoNote.FileSize))
	case message.Audio != nil:
		summary := formatMediaSummary("audio", message.Audio.FileName, message.Audio.MimeType, message.Audio.Duration, message.Audio.FileSize)
		if message.Audio.Performer != "" || message.Audio.Title != "" {
			return summary + fmt.Sprintf(" performer=%s title=%s", message.Audio.Performer, message.Audio.Title)
		}
		return summary
	case message.Voice != nil:
		return formatMediaSummary("voice", "", message.Voice.MimeType, message.Voice.Duration, message.Voice.FileSize)
	case message.Document != nil:
		return formatMediaSummary("document", message.Document.FileName, message.Document.MimeType, 0, message.Document.FileSize)
	case message.Sticker != nil:
		if message.Sticker.Emoji != "" {
			return "MEDIA: sticker emoji=" + message.Sticker.Emoji
		}
		return "MEDIA: sticker"
	default:
		return ""
	}
}

func formatMediaSummary(mediaType, fileName, mimeType string, duration int, fileSize int64) string {
	parts := []string{"MEDIA:", mediaType}
	if mimeType != "" {
		parts = append(parts, "mime="+mimeType)
	}
	if duration > 0 {
		parts = append(parts, fmt.Sprintf("duration=%ds", duration))
	}
	if fileName != "" {
		parts = append(parts, "file="+fileName)
	}
	if fileSize > 0 {
		parts = append(parts, fmt.Sprintf("size=%d", fileSize))
	}
	return strings.Join(parts, " ")
}
