package structs

type Result struct {
	Reasoning string  `json:"reasoning"`
	SpamScore float64 `json:"spam_score"`
}

type SpamClassification struct {
	SpamScore float64 `json:"spam_score"`
}