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
	claudeProvider Provider
	// openAIProvider Provider
	// geminiProvider Provider
}

func NewRecordProcessor(
	claudeProvider Provider,
	// openAIProvider Provider,
	// geminiProvider Provider,
) *RecordProcessor {
	return &RecordProcessor{
		claudeProvider: claudeProvider,
		// openAIProvider: openAIProvider,
		// geminiProvider: geminiProvider,
	}
}

func (rp *RecordProcessor) ProcessRecord(message string, prompt string) (structs.Result, error) {
	prompt = strings.ReplaceAll(prompt, "{{CHANNEL_CONTENT}}", message)

	response, err := rp.claudeProvider.ProcessMessage(context.Background(), prompt)
	if err != nil {
		return structs.Result{}, fmt.Errorf("API error: %w", err)
	}

	// Extract reasoning
	reasoningMatch := reasoningRegex.FindStringSubmatch(response)

	var reasoning string
	if len(reasoningMatch) > 1 {
		reasoning = reasoningMatch[1]
	} else {
		return structs.Result{}, fmt.Errorf("could not extract reasoning from response")
	}

	// Extract JSON
	jsonMatch := jsonRegex.FindStringSubmatch(response)

	var classification structs.SpamClassification
	if len(jsonMatch) > 1 {
		err = json.Unmarshal([]byte(jsonMatch[1]), &classification)
		if err != nil {
			return structs.Result{}, fmt.Errorf("failed to parse JSON classification: %w", err)
		}
	} else {
		return structs.Result{}, fmt.Errorf("could not extract JSON classification from response")
	}

	return structs.Result{
		Reasoning: reasoning,
		SpamScore: classification.SpamScore,
	}, nil
}
