package history

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

type TelegramData struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	ID       int64     `json:"id"`
	Messages []Message `json:"messages"`
}

type Message struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	FromID  string          `json:"from_id"`
	From    string          `json:"from"`
	Text    json.RawMessage `json:"text"` // Changed to json.RawMessage
	Date    string          `json:"date"`
	ActorID string          `json:"actor_id"`
}

// ProcessFile reads a Telegram export file and stores message counts in Redis
func ProcessFile(filePath string, redisClient *redis.Client) error {
	// Read the file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("error reading file: %v", err)
	}

	// Parse JSON
	var telegramData TelegramData
	err = json.Unmarshal(data, &telegramData)
	if err != nil {
		return fmt.Errorf("error parsing JSON: %v", err)
	}

	// Count messages for each user in the channel
	userCounts := make(map[string]int)
	for _, message := range telegramData.Messages {
		if message.Type == "message" && message.FromID != "" {
			userID := strings.TrimPrefix(message.FromID, "user")
			userCounts[userID]++
		}
	}
	chatID := ""
	switch telegramData.Type {
	case "public_supergroup", "private_supergroup", "channel":
		chatID = "-100" + strconv.FormatInt(telegramData.ID, 10)
	default:
		return fmt.Errorf("wrong chat type: %s", telegramData.Type)
	}

	// Store counts in Redis
	for userID, count := range userCounts {
		key := fmt.Sprintf("%s:%s", userID, chatID)
		err = redisClient.Set(context.TODO(), key, count, 0).Err()
		if err != nil {
			return fmt.Errorf("error storing count for user %s in channel %s: %v", userID, chatID, err)
		}
	}

	fmt.Printf("Processed %d messages in channel %s\n", len(telegramData.Messages), chatID)
	return nil
}

// GetUserMessageCount retrieves the message count for a specific user and channel from Redis
func GetUserMessageCount(redisClient *redis.Client, userID string, channelID int64) (int, error) {
	key := fmt.Sprintf("%s:%d", userID, channelID)
	count, err := redisClient.Get(context.TODO(), key).Int()
	if err != nil {
		if err == redis.Nil {
			return 0, nil // User-channel combination not found, return 0 count
		}
		return 0, fmt.Errorf("error retrieving count for user %s in channel %d: %v", userID, channelID, err)
	}
	return count, nil
}
