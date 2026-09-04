package structs

import "testing"

func TestToAnthropicMessageIncludesSenderName(t *testing.T) {
	t.Parallel()

	message := Message{
		Text:     "n8",
		UserName: "promotional sender",
	}

	anthropicMessage, err := message.ToAnthropicMessage("prompt")
	if err != nil {
		t.Fatalf("ToAnthropicMessage() error = %v", err)
	}

	if len(anthropicMessage.Content) != 3 {
		t.Fatalf("content length = %d, want 3", len(anthropicMessage.Content))
	}
	if got := anthropicMessage.Content[1].Text; got != "SENDER_NAME: promotional sender" {
		t.Errorf("sender content = %q", got)
	}
	if got := anthropicMessage.Content[2].Text; got != "MESSAGE_TEXT: n8" {
		t.Errorf("message content = %q", got)
	}
}

func TestMessageHashIncludesSenderName(t *testing.T) {
	t.Parallel()

	first := Message{Text: "n8", UserName: "promotional sender"}
	second := Message{Text: "n8", UserName: "regular sender"}

	if first.Hash() == second.Hash() {
		t.Fatal("messages with different sender names must have different hashes")
	}
}

func TestEmptyMessageHasNoHash(t *testing.T) {
	t.Parallel()

	message := Message{UserName: "sender"}
	if got := message.Hash(); got != "" {
		t.Fatalf("empty message hash = %q, want empty string", got)
	}
}
