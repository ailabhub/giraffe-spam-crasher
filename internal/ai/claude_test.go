package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAnthropicStructuredOutputSingleRequest(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	provider := NewAnthropicProvider("test-key", "claude-sonnet-4-6", 0, "Classify spam.",
		slog.New(slog.NewTextHandler(&logs, nil)))
	calls := 0
	const classification = `{"spam_score":0.25,"reasoning":"test result"}`
	provider.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodPost || req.URL.String() != "https://api.anthropic.com/v1/messages" {
			t.Fatalf("unexpected endpoint: %s %s", req.Method, req.URL)
		}
		if req.Header.Get("x-api-key") != "test-key" || req.Header.Get("anthropic-version") != "2023-06-01" {
			t.Fatal("missing API headers")
		}
		defer req.Body.Close()
		var payload map[string]any
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode HTTP request body: %v", err)
		}
		if payload["model"] != "claude-sonnet-4-6" || payload["temperature"] != float64(0) || payload["max_tokens"] != float64(1000) {
			t.Fatal("model or generation parameters changed")
		}
		assertAnthropicOutputConfig(t, payload["output_config"])
		body, err := json.Marshal(map[string]any{
			"content": []map[string]string{{"type": "text", "text": classification}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewReader(body)),
		}, nil
	})

	result, err := provider.ProcessMessage(context.Background(), &structs.Message{Text: "Hello"})
	if err != nil {
		t.Fatalf("ProcessMessage: %v", err)
	}
	if result != classification || calls != 1 {
		t.Fatalf("result = %q, HTTP calls = %d; want expected JSON in one call", result, calls)
	}
	if strings.Contains(logs.String(), "retrying without output_config") {
		t.Fatal("successful structured output must not trigger fallback")
	}
}

func assertAnthropicOutputConfig(t *testing.T, config any) {
	t.Helper()
	// Compare the serialized HTTP schema, including absence of unsupported bounds.
	const expectedConfig = `{"format":{"type":"json_schema","schema":{
		"type":"object",
		"properties":{
			"spam_score":{"type":"number","description":"Spam score from 0 to 1 inclusive; validated locally."},
			"reasoning":{"type":"string"}
		},
		"required":["spam_score","reasoning"],
		"additionalProperties":false
	}}}`
	var expected map[string]any
	if err := json.Unmarshal([]byte(expectedConfig), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config, expected) {
		t.Fatalf("unexpected HTTP output_config: %#v", config)
	}
}
