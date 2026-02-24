package models

// GlobalSettings represents application-wide settings
type GlobalSettings struct {
	PlanModeDefault bool            `json:"planModeDefault"` // Default plan mode checkbox state
	AdminMode       bool            `json:"adminMode"`       // Admin mode toggle
	TabSettings     map[string]bool `json:"tabSettings"`     // Per-tab settings (tab ID -> enabled)
}

// FileInfo represents file attachment information
type FileInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Data string `json:"data"` // Base64 encoded file data
	Type string `json:"type"` // File extension
}
