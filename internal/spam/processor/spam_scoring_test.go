package processor

import (
	"fmt"
	"math"
	"testing"

	"github.com/ailabhub/giraffe-spam-crasher/internal/structs"
)

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

func TestParseSpamClassificationScoreRange(t *testing.T) {
	t.Parallel()

	for _, score := range []float64{0, 1, 0.5, -0.01, 1.01, math.Nextafter(0, -1), math.Nextafter(1, 2)} {
		for _, prefix := range []string{"", "Analysis:\n"} {
			t.Run(fmt.Sprintf("score=%g/prose=%t", score, prefix != ""), func(t *testing.T) {
				t.Parallel()
				resp := prefix + fmt.Sprintf(`{"spam_score":%g,"reasoning":"test"}`, score)
				got, err := parseSpamClassification(resp)
				wantError := score < 0 || score > 1
				if (err != nil) != wantError {
					t.Fatalf("score %g: error = %v, wantError = %t", score, err, wantError)
				}
				if !wantError && got.SpamScore != score {
					t.Fatalf("got score %g, want %g", got.SpamScore, score)
				}
			})
		}
	}
}

func TestValidateSpamClassificationRejectsNonFinite(t *testing.T) {
	t.Parallel()

	for _, score := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run(fmt.Sprint(score), func(t *testing.T) {
			t.Parallel()
			if _, err := validateSpamClassification(structs.SpamCheckResult{SpamScore: score}); err == nil {
				t.Fatal("expected error for non-finite spam_score")
			}
		})
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
