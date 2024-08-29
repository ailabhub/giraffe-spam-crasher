package structs

type SpamCheckResult struct {
	Reasoning string  `json:"reasoning"`
	SpamScore float64 `json:"spam_score"`
	FromCache bool
}

type SpamClassification struct {
	SpamScore float64 `json:"spam_score"`
}
