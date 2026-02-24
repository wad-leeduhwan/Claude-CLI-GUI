package models

// TabState represents the state of a single conversation tab
type TabState struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Messages       []Message `json:"messages"`
	ConversationID string    `json:"conversationId"`
	IsActive       bool      `json:"isActive"`
	AdminMode      bool               `json:"adminMode"`                // Per-tab admin mode
	PlanMode       bool               `json:"planMode"`                 // Per-tab plan mode
	Orchestrator   *OrchestratorState `json:"orchestrator,omitempty"`   // Orchestrator state (admin mode only)
	WorkDir        string    `json:"workDir"`          // Working directory for this tab
	ContextFiles   []string  `json:"contextFiles"`    // Context file paths for this tab
}
