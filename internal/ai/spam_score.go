package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
)

// TODO закоменчено пока нет фолбека не другие провайдеры
// фолбек запилю как напишу тест на весь флоу
// а то страшно без теста что-то делать
// пока все равно запускаем только с claude
type RecordProcessor struct {
	prompt         string
	claudeProvider Provider
	// openAIProvider Provider
	// geminiProvider Provider
}

func NewRecordProcessor(
	claudeProvider Provider,
	prompt string,
	// openAIProvider Provider,
	// geminiProvider Provider,
) *RecordProcessor {
	return &RecordProcessor{
		claudeProvider: claudeProvider,
		prompt:         prompt,
		// openAIProvider: openAIProvider,
		// geminiProvider: geminiProvider,
	}
}

func (rp *RecordProcessor) GetSpamScoreForMessage(ctx context.Context, message string) (structs.SpamCheckResult, error) {
	prompt := strings.ReplaceAll(rp.prompt, "{{CHANNEL_CONTENT}}", message)

	response, err := rp.claudeProvider.ProcessMessage(ctx, prompt)
	if err != nil {
		return structs.SpamCheckResult{}, fmt.Errorf("API error: %w", err)
	}

	// Extract reasoning
	reasoningMatch := reasoningRegex.FindStringSubmatch(response)

	var reasoning string
	if len(reasoningMatch) > 1 {
		reasoning = reasoningMatch[1]
	} else {
		return structs.SpamCheckResult{}, fmt.Errorf("could not extract reasoning from response")
	}

	// Extract JSON
	jsonMatch := jsonRegex.FindStringSubmatch(response)

	var classification structs.SpamClassification
	if len(jsonMatch) > 1 {
		err = json.Unmarshal([]byte(jsonMatch[1]), &classification)
		if err != nil {
			return structs.SpamCheckResult{}, fmt.Errorf("failed to parse JSON classification: %w", err)
		}
	} else {
		return structs.SpamCheckResult{}, fmt.Errorf("could not extract JSON classification from response")
	}

	return structs.SpamCheckResult{
		Reasoning: reasoning,
		SpamScore: classification.SpamScore,
	}, nil
}
