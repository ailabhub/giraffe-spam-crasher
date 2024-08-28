package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/ailabhub/giraffe-spam-crasher/internal/ai"
	"github.com/ailabhub/giraffe-spam-crasher/internal/bot"
	"github.com/ailabhub/giraffe-spam-crasher/internal/history"
	"github.com/ailabhub/giraffe-spam-crasher/internal/spam/processor"
	"github.com/redis/go-redis/v9"
)

type AIProvider interface {
	ProcessMessage(ctx context.Context, message string) (string, error)
}

func main() { //nolint:gocyclo,gocognit
	ctx := context.Background()
	logLevel := flag.String("log-level", "info", "Logging level (debug, info, warn, error)")
	historyFile := flag.String("history", "", "Path to the history file")

	apiProvider := flag.String("provider", "openai", "API provider (openai or anthropic)")
	model := flag.String("model", "gpt-4o-mini", "Model to use (e.g., gpt-4 for OpenAI, claude-2 for Anthropic)")
	promptPath := flag.String("prompt", "", "Path to the prompt text file")
	threshold := flag.Float64("spam-threshold", 0.5, "Threshold for classifying a message as spam")
	newUserThreshold := flag.Int("new-user-threshold", 1, "Threshold for classifying user as new")
	var whitelistChannels intSliceFlag
	flag.Var(&whitelistChannels, "whitelist-channels", "Comma-separated list of whitelisted channel IDs")

	var logChannels logChannelsFlag
	flag.Var(&logChannels, "log-channels", "Comma-separated list of working chat ID and log channel ID pairs in the format 'workingChatID1:logChannelID1,workingChatID2:logChannelID2'")

	flag.Parse()

	var logLevelValue slog.Level
	switch strings.ToLower(*logLevel) {
	case "debug":
		logLevelValue = slog.LevelDebug
	case "info":
		logLevelValue = slog.LevelInfo
	case "warn":
		logLevelValue = slog.LevelWarn
	case "error":
		logLevelValue = slog.LevelError
	default:
		fmt.Printf("Invalid log level: %s. Defaulting to info.\n", *logLevel)
		logLevelValue = slog.LevelInfo
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevelValue}))
	slog.SetDefault(logger)

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		slog.Error("REDIS_URL environment variable is not set")
		os.Exit(1)
	}
	redisOptions, err := redis.ParseURL(redisURL)
	if err != nil {
		slog.Error("Failed to parse Redis URL", "error", err)
		os.Exit(1)
	}

	rdb := redis.NewClient(redisOptions)

	_, err = rdb.Ping(ctx).Result()
	if err != nil {
		slog.Error("Failed to connect to Redis", "error", err)
		os.Exit(1)
	}

	defer rdb.Close()

	slog.Info("Connected to Redis", "url", redisURL)

	// Load history if the flag is not empty and Redis is empty
	if *historyFile != "" {
		err := history.ProcessFile(*historyFile, rdb)
		if err != nil {
			slog.Error("Failed to load history", "error", err)
			// Decide whether to continue or exit based on your requirements
			// os.Exit(1)
		} else {
			slog.Info("History loaded", "file", *historyFile)
			slog.Info("Exiting, run without the --history flag to continue")
			os.Exit(0)
		}
	} else {
		// TODO: check by channel's ids for multiple channels
		// Check if Redis is empty
		keysCount, err := rdb.DBSize(ctx).Result()
		if err != nil {
			slog.Error("Failed to get Redis database size", "error", err)
			os.Exit(1)
		}
		if keysCount == 0 {
			slog.Warn("REDIS DATABASE IS EMPTY, STARTING WITH AN EMPTY HISTORY!")
		} else {
			slog.Info("Total history size", "count", keysCount)
		}
	}
	// Read API key from environment variable
	var apiKey string
	var provider AIProvider
	rateLimit := 0.0
	switch *apiProvider {
	case "openai":
		apiKey = os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			fmt.Println("OPENAI_API_KEY environment variable is not set")
			os.Exit(1)
		}
		provider = ai.NewOpenAIProvider(apiKey, *model, rateLimit)
		slog.Info("Using OpenAI API", "model", *model)
	case "anthropic":
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			fmt.Println("ANTHROPIC_API_KEY environment variable is not set")
			os.Exit(1)
		}
		provider = ai.NewAnthropicProvider(apiKey, *model, rateLimit)
		slog.Info("Using Anthropic API", "model", *model)
	case "gemini":
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			slog.Error("GEMINI_API_KEY environment variable is not set")
			os.Exit(1)
		}

		geminiProvider, err := ai.NewGeminiProvider(apiKey, *model, rateLimit)
		if err != nil {
			slog.Error("Error creating Gemini provider", "error", err)
			os.Exit(1)
		}
		provider = geminiProvider
		slog.Info("Using Gemini API", "model", *model)
	default:
		fmt.Printf("Unsupported API provider: %s\n", *apiProvider)
		os.Exit(1)
	}
	prompt := ""
	if *promptPath != "" {
		promptBytes, err := os.ReadFile(*promptPath)
		if err != nil {
			slog.Error("Failed to read prompt file", "error", err)
			os.Exit(1)
		}
		prompt = string(promptBytes)
	}
	if prompt == "" {
		fmt.Println("No prompt provided")
		os.Exit(1)
	}

	recordProcessor := ai.NewRecordProcessor(provider, prompt)
	spamProcessor := processor.NewSpamProcessor(recordProcessor)
	spamProcessorCache := processor.NewSpamProcessorCache(spamProcessor, rdb)

	bot, err := bot.New(rdb, spamProcessorCache, &bot.Config{
		Threshold:         *threshold,
		NewUserThreshold:  *newUserThreshold,
		WhitelistChannels: whitelistChannels,
		LogChannels:       logChannels,
	})

	if err != nil {
		slog.Error("Failed to create bot", "error", err)
		os.Exit(1)
	}

	go bot.Start()

	// Wait for interrupt signal to gracefully shutdown the bot
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down bot...")
	bot.Stop()
}

// intSliceFlag is a custom flag type for a slice of integers
type intSliceFlag []int64

func (i *intSliceFlag) String() string {
	return fmt.Sprint(*i)
}

func (i *intSliceFlag) Set(value string) error {
	if value == "" {
		return nil
	}

	// Split the input string by commas
	values := strings.Split(value, ",")

	for _, v := range values {
		// Trim any whitespace
		v = strings.TrimSpace(v)

		// Parse the integer
		intValue, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid integer value: %s", v)
		}

		// Append the parsed integer to the slice
		*i = append(*i, intValue)
	}

	return nil
}

// logChannelsFlag is a custom flag type for a map of working chat IDs to log channel IDs
type logChannelsFlag map[int64]int64

func (l *logChannelsFlag) String() string {
	pairs := make([]string, len(*l))
	for workingChatID, logChannelID := range *l {
		pairs = append(pairs, fmt.Sprintf("%d:%d", workingChatID, logChannelID))
	}
	return strings.Join(pairs, ",")
}

func (l *logChannelsFlag) Set(value string) error {
	if value == "" {
		return nil
	}
	pairs := strings.Split(value, ",")
	*l = make(map[int64]int64)
	for _, pair := range pairs {
		parts := strings.Split(strings.TrimSpace(pair), ":")
		if len(parts) != 2 {
			return fmt.Errorf("invalid format for log channel pair, expected 'workingChatID:logChannelID'")
		}

		workingChatID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid working chat ID: %v", err)
		}

		logChannelID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid log channel ID: %v", err)
		}

		(*l)[workingChatID] = logChannelID
	}
	return nil
}
