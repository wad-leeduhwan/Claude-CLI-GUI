package models

// ToolUse represents a single tool invocation during response generation
type ToolUse struct {
	ToolName string `json:"toolName"`
	Detail   string `json:"detail"`
}

// Message represents a single message in a conversation
type Message struct {
	Role         string            `json:"role"` // "user", "assistant", or "system"
	Content      string            `json:"content"`
	Attachments  []string          `json:"attachments"`
	Timestamp    int64             `json:"timestamp"`
	DurationMs   int64             `json:"durationMs,omitempty"`   // Response generation time in milliseconds
	InputTokens  int               `json:"inputTokens,omitempty"`  // Token usage for this response
	OutputTokens int               `json:"outputTokens,omitempty"` // Token usage for this response
	ToolUses     []ToolUse         `json:"toolUses,omitempty"`     // Tool invocations during this response
	Metadata     map[string]string `json:"metadata,omitempty"`     // e.g., {"type":"orchestration","jobId":"..."}
}

// UsageInfo represents token usage information for a conversation
type UsageInfo struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
	TotalTokens  int `json:"totalTokens"`
	MessageCount int `json:"messageCount"`
}
