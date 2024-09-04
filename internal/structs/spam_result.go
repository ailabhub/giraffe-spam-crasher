package structs

type SpamCheckResult struct {
	Reasoning string  `json:"reasoning,omitempty"`
	SpamScore float64 `json:"spam_score"`
	FromCache bool
}
