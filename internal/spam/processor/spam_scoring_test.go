package processor

import "testing"

func TestParseSpamClassificationPlainJSON(t *testing.T) {
	t.Parallel()

	resp := `{"spam_score":0.88,"reasoning":"vpn promo"}`
	got, err := parseSpamClassification(resp)
	if err != nil {
		t.Fatalf("parseSpamClassification returned error: %v", err)
	}
	if got.SpamScore != 0.88 {
		t.Fatalf("unexpected spam_score: got %v want %v", got.SpamScore, 0.88)
	}
}

func TestParseSpamClassificationProseThenJSON(t *testing.T) {
	t.Parallel()

	resp := "I'll analyze this.\n\n{\"spam_score\":0.91,\"reasoning\":\"promo\"}"
	got, err := parseSpamClassification(resp)
	if err != nil {
		t.Fatalf("parseSpamClassification returned error: %v", err)
	}
	if got.SpamScore != 0.91 {
		t.Fatalf("unexpected spam_score: got %v want %v", got.SpamScore, 0.91)
	}
}

func TestParseSpamClassificationRejectsOutOfRange(t *testing.T) {
	t.Parallel()

	resp := `{"spam_score":1.7,"reasoning":"bad"}`
	_, err := parseSpamClassification(resp)
	if err == nil {
		t.Fatal("expected error for out-of-range spam_score, got nil")
	}
}

func TestParseSpamClassificationNoJSON(t *testing.T) {
	t.Parallel()

	resp := "Only text, no json"
	_, err := parseSpamClassification(resp)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}
