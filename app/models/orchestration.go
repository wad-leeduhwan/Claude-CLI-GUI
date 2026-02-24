package models

import "time"

// TaskStatus represents the status of an orchestration task
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

// WorkerTask represents a single task assigned to a worker tab
type WorkerTask struct {
	ID          string     `json:"id"`
	WorkerTabID string     `json:"workerTabId"`
	AdminTabID  string     `json:"adminTabId"`
	Description string     `json:"description"`
	Prompt      string     `json:"prompt"`
	Status      TaskStatus `json:"status"`
	Result      string     `json:"result"`
	Error       string     `json:"error,omitempty"`
	StartedAt   time.Time  `json:"startedAt,omitempty"`
	CompletedAt time.Time  `json:"completedAt,omitempty"`
	DurationMs  int64      `json:"durationMs,omitempty"`
}

// OrchestrationJob represents a complete orchestration job with multiple tasks
type OrchestrationJob struct {
	ID              string       `json:"id"`
	AdminTabID      string       `json:"adminTabId"`
	UserRequest     string       `json:"userRequest"`
	Tasks           []WorkerTask `json:"tasks"`
	Status          TaskStatus   `json:"status"`
	SynthesisResult string       `json:"synthesisResult,omitempty"`
}

// OrchestratorState holds the orchestrator state for an admin tab
type OrchestratorState struct {
	ConnectedTabs []string           `json:"connectedTabs"`
	CurrentJob    *OrchestrationJob  `json:"currentJob,omitempty"`
	JobHistory    []OrchestrationJob `json:"jobHistory"`
}
