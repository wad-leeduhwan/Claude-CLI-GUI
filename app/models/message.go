package models

// Message represents a single message in a conversation
type Message struct {
	Role        string            `json:"role"` // "user", "assistant", or "system"
	Content     string            `json:"content"`
	Attachments []string          `json:"attachments"`
	Timestamp   int64             `json:"timestamp"`
	DurationMs  int64             `json:"durationMs,omitempty"`  // Response generation time in milliseconds
	Metadata    map[string]string `json:"metadata,omitempty"`    // e.g., {"type":"orchestration","jobId":"..."}
}

// UsageInfo represents token usage information for a conversation
type UsageInfo struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
	MessageCount int `json:"messageCount"`
}
