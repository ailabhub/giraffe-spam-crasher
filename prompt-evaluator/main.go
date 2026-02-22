package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/time/rate"
)

type Result struct {
	Reasoning  string  `json:"reasoning"`
	Scratchpad string  `json:"scratchpad"`
	IsSpam     float64 `json:"isSpam"`
}

type LogEntry struct {
	Input    string `json:"input"`
	Response Result `json:"response"`
}

type SpamClassification struct {
	IsSpam float64 `json:"spam_score"`
}

type RunInfo struct {
	PromptName  string
	InputFile   string
	APIProvider string
	Model       string
	RateLimit   float64
	Threshold   float64
	Timestamp   time.Time
}

type AIProvider interface {
	ProcessMessage(ctx context.Context, message string) (string, error)
}

type OpenAIProvider struct {
	client      *openai.Client
	model       string
	rateLimiter *rate.Limiter
}

func NewOpenAIProvider(apiKey, model string, rateLimit float64) *OpenAIProvider {
	var limiter *rate.Limiter
	if rateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(rateLimit), 1)
	} else {
		limiter = rate.NewLimiter(rate.Inf, 0) // No rate limit
	}
	return &OpenAIProvider{
		client:      openai.NewClient(apiKey),
		model:       model,
		rateLimiter: limiter,
	}
}

func (p *OpenAIProvider) ProcessMessage(ctx context.Context, message string) (string, error) {
	err := p.rateLimiter.Wait(ctx) // Wait for rate limit
	if err != nil {
		return "", fmt.Errorf("rate limit error: %w", err)
	}

	resp, err := p.client.CreateChatCompletion(
		ctx,
		openai.ChatCompletionRequest{
			Model: p.model,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleUser,
					Content: message,
				},
			},
			Temperature: 0,
		},
	)

	if err != nil {
		return "", fmt.Errorf("OpenAI API error: %w", err)
	}

	return resp.Choices[0].Message.Content, nil
}

type AnthropicProvider struct {
	client      *http.Client
	apiKey      string
	model       string
	rateLimiter *rate.Limiter
}

func NewAnthropicProvider(apiKey, model string, rateLimit float64) *AnthropicProvider {
	var limiter *rate.Limiter
	if rateLimit > 0 {
		limiter = rate.NewLimiter(rate.Limit(rateLimit), 1)
	} else {
		limiter = rate.NewLimiter(rate.Inf, 0) // No rate limit
	}
	return &AnthropicProvider{
		client:      &http.Client{Timeout: 30 * time.Second},
		apiKey:      apiKey,
		model:       model,
		rateLimiter: limiter,
	}
}

type AnthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type AnthropicRequest struct {
	Model       string             `json:"model"`
	Messages    []AnthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature"`
}

type AnthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

func (p *AnthropicProvider) ProcessMessage(ctx context.Context, message string) (string, error) {
	err := p.rateLimiter.Wait(ctx) // Wait for rate limit
	if err != nil {
		return "", fmt.Errorf("rate limit error: %w", err)
	}

	requestBody, err := json.Marshal(AnthropicRequest{
		Model: p.model,
		Messages: []AnthropicMessage{
			{Role: "user", Content: message},
		},
		MaxTokens:   1000,
		Temperature: 0,
	})
	if err != nil {
		return "", fmt.Errorf("error marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(requestBody))
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
	}

	var anthropicResp AnthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&anthropicResp); err != nil {
		return "", fmt.Errorf("error decoding response: %w", err)
	}

	if len(anthropicResp.Content) == 0 || len(anthropicResp.Content[0].Text) == 0 {
		return "", fmt.Errorf("empty response from Anthropic API")
	}

	return anthropicResp.Content[0].Text, nil
}

func main() {
	// Define command-line flags
	inputFile := flag.String("input", "", "Path to the input CSV file")
	promptPath := flag.String("prompt", "", "Path to the prompt text file or folder")
	apiProvider := flag.String("provider", "openai", "API provider (openai or anthropic)")
	model := flag.String("model", "gpt-4o-mini", "Model to use (e.g., gpt-4 for OpenAI, claude-2 for Anthropic)")
	rateLimit := flag.Float64("ratelimit", 0.0, "Rate limit for API requests (requests per second, 0 for no limit)")
	threshold := flag.Float64("threshold", 0.5, "Threshold for classifying a message as spam")
	flag.Parse()

	// Check if input file is provided
	if *inputFile == "" {
		fmt.Println("Please provide an input file using the -input flag")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Read API key from environment variable
	var apiKey string
	var provider AIProvider

	switch *apiProvider {
	case "openai":
		apiKey = os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			fmt.Println("OPENAI_API_KEY environment variable is not set")
			os.Exit(1)
		}
		provider = NewOpenAIProvider(apiKey, *model, *rateLimit)
	case "anthropic":
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			fmt.Println("ANTHROPIC_API_KEY environment variable is not set")
			os.Exit(1)
		}
		provider = NewAnthropicProvider(apiKey, *model, *rateLimit)
	default:
		fmt.Printf("Unsupported API provider: %s\n", *apiProvider)
		os.Exit(1)
	}
	timeStart := time.Now()

	// logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{}))
	logFile, err := os.OpenFile(timeStart.Format("2006-01-02_15-04-05")+"_score.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Println("Failed to open log file:", err)
		os.Exit(1)
	}
	defer logFile.Close()

	multiWriter := io.MultiWriter(os.Stdout, logFile)

	// Create a new logger that writes to the multi-writer
	logger := slog.New(slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Read CSV file
	records, err := readCSV(*inputFile)
	if err != nil {
		logger.Error("Failed to read CSV", "error", err)
		os.Exit(1)
	}

	// Read prompts
	prompts, err := readPrompts(*promptPath)
	if err != nil {
		logger.Error("Failed to read prompts", "error", err)
		os.Exit(1)
	}

	// Process records for each prompt
	for promptName, prompt := range prompts {
		// Create progress bar
		bar := progressbar.Default(int64(len(records)))

		// Process records
		var wg sync.WaitGroup
		results := make([]Result, len(records))
		logs := make([]LogEntry, len(records))

		for i, record := range records {
			wg.Add(1)
			go func(i int, r []string) {
				defer wg.Done()
				result, err := processRecord(r, prompt, provider)
				if err != nil {
					logger.Error("Failed to process record", "error", err, "record", r)
					return
				}
				results[i] = result
				logs[i] = LogEntry{
					Input:    r[0],
					Response: result,
				}
				bar.Add(1)
			}(i, record)
		}

		wg.Wait()

		runInfo := RunInfo{
			PromptName:  promptName,
			InputFile:   *inputFile,
			APIProvider: *apiProvider,
			Model:       *model,
			RateLimit:   *rateLimit,
			Threshold:   *threshold,
			Timestamp:   timeStart,
		}

		// Calculate and print results
		printResults(results, *threshold, logger, runInfo)

		// Write logs to file
		writeLogsToFile(logs, promptName, timeStart, logger)
	}
}

func readCSV(filename string) ([][]string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.LazyQuotes = true // Handle escaped quotes

	var records [][]string
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read CSV: %w", err)
		}
		records = append(records, record)
	}

	return records, nil
}

func readPrompts(path string) (map[string]string, error) {
	prompts := make(map[string]string)

	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat path: %w", err)
	}

	if fileInfo.IsDir() {
		files, err := os.ReadDir(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read directory: %w", err)
		}

		for _, file := range files {
			if !file.IsDir() && strings.HasSuffix(file.Name(), ".txt") {
				fullPath := filepath.Join(path, file.Name())
				content, err := os.ReadFile(fullPath)
				if err != nil {
					return nil, fmt.Errorf("failed to read file %s: %w", fullPath, err)
				}
				prompts[file.Name()] = string(content)
			}
		}
	} else {
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}
		prompts[filepath.Base(path)] = string(content)
	}

	if len(prompts) == 0 {
		return nil, fmt.Errorf("no prompt files found")
	}

	return prompts, nil
}

func processRecord(record []string, prompt string, provider AIProvider) (Result, error) {
	if len(record) == 0 {
		return Result{}, fmt.Errorf("empty record")
	}

	prompt = strings.ReplaceAll(prompt, "{{CHANNEL_CONTENT}}", record[0])

	response, err := provider.ProcessMessage(context.Background(), prompt)
	if err != nil {
		return Result{}, fmt.Errorf("API error: %w", err)
	}

	// Extract scratchpad
	var scratchpad string
	scratchpadRegex := regexp.MustCompile(`<scratchpad>([\s\S]*?)</scratchpad>`)
	scratchpadMatch := scratchpadRegex.FindStringSubmatch(response)
	if len(scratchpadMatch) > 1 {
		scratchpad = scratchpadMatch[1]
	}

	// Extract reasoning
	reasoningRegex := regexp.MustCompile(`<reasoning>([\s\S]*?)</reasoning>`)
	reasoningMatch := reasoningRegex.FindStringSubmatch(response)

	var reasoning string
	if len(reasoningMatch) > 1 {
		reasoning = reasoningMatch[1]
	} else {
		return Result{}, fmt.Errorf("could not extract reasoning from response")
	}

	// Extract JSON
	jsonRegex := regexp.MustCompile(`<json>([\s\S]*?)</json>`)
	jsonMatch := jsonRegex.FindStringSubmatch(response)

	var classification SpamClassification
	if len(jsonMatch) > 1 {
		err = json.Unmarshal([]byte(jsonMatch[1]), &classification)
		if err != nil {
			return Result{}, fmt.Errorf("failed to parse JSON classification: %w", err)
		}
	} else {
		return Result{}, fmt.Errorf("could not extract JSON classification from response")
	}

	return Result{
		Reasoning:  reasoning,
		Scratchpad: scratchpad,
		IsSpam:     classification.IsSpam,
	}, nil
}

func printResults(results []Result, threshold float64, logger *slog.Logger, runInfo RunInfo) {
	var totalProcessed int
	var totalSpam int
	var totalSpamScore float64
	for _, result := range results {
		if result.IsSpam != 0 || result.Reasoning != "" {
			totalProcessed++
			totalSpamScore += result.IsSpam
			if result.IsSpam > threshold {
				totalSpam++
			}
		}
	}

	totalScore := struct {
		SpamScore struct {
			Spam      int     `json:"spam"`
			Total     int     `json:"total"`
			SpamScore float64 `json:"spam_score"`
		} `json:"spam_score"`
		RunInfo struct {
			PromptName  string    `json:"prompt_name"`
			InputFile   string    `json:"input_file"`
			APIProvider string    `json:"api_provider"`
			Model       string    `json:"model"`
			RateLimit   float64   `json:"rate_limit"`
			Threshold   float64   `json:"threshold"`
			Timestamp   time.Time `json:"timestamp"`
		} `json:"run_info"`
	}{
		SpamScore: struct {
			Spam      int     `json:"spam"`
			Total     int     `json:"total"`
			SpamScore float64 `json:"spam_score"`
		}{
			Spam:      totalSpam,
			Total:     totalProcessed,
			SpamScore: totalSpamScore / float64(totalProcessed),
		},
		RunInfo: struct {
			PromptName  string    `json:"prompt_name"`
			InputFile   string    `json:"input_file"`
			APIProvider string    `json:"api_provider"`
			Model       string    `json:"model"`
			RateLimit   float64   `json:"rate_limit"`
			Threshold   float64   `json:"threshold"`
			Timestamp   time.Time `json:"timestamp"`
		}(runInfo),
	}

	logger.Info("Total score",
		"spam_score", totalScore.SpamScore,
		"run_info", totalScore.RunInfo,
	)
}
func writeLogsToFile(logs []LogEntry, promptName string, timeStart time.Time, logger *slog.Logger) {
	logsJSON, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		logger.Error("Failed to marshal logs", "error", err)
		return
	}

	filename := fmt.Sprintf("%s_%s_logs.json", timeStart.Format("2006-01-02_15-04-05"), strings.TrimSuffix(promptName, filepath.Ext(promptName)))
	if err := os.WriteFile(filename, logsJSON, 0644); err != nil {
		logger.Error("Failed to write logs file", "error", err)
	}
}
