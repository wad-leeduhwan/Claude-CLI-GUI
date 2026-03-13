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
	TeamsMode      bool               `json:"teamsMode"`                // Per-tab Teams (Beta) mode
	Orchestrator   *OrchestratorState `json:"orchestrator,omitempty"`   // Orchestrator state (admin mode only)
	TeamsState     *TeamsState        `json:"teamsState,omitempty"`     // Teams state (teams mode only)
	WorkDir        string    `json:"workDir"`          // Working directory for this tab
	ContextFiles   []string  `json:"contextFiles"`    // Context file paths for this tab
}

// TeamsState holds state for Teams (Beta) mode — single CLI call with --agents
type TeamsState struct {
	ConnectedTabs []string          `json:"connectedTabs"`          // Connected worker tab IDs
	AgentMapping  map[string]string `json:"agentMapping,omitempty"` // agentName → workerTabID
	IsRunning     bool              `json:"isRunning"`
}
