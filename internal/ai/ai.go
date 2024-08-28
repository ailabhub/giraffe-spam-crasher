package ai

import (
	"context"
	"regexp"
)

// Global variables for prompt
var (
	reasoningRegex = regexp.MustCompile(`<reasoning>([\s\S]*?)</reasoning>`)
	jsonRegex      = regexp.MustCompile(`<json>([\s\S]*?)</json>`)
)

type Provider interface {
	ProcessMessage(ctx context.Context, message string) (string, error)
	ProcessImage(ctx context.Context, imageData []byte) (string, error)
}
