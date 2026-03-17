package models

// TeamPreset represents a saved team configuration
type TeamPreset struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	WorkerNames []string `json:"workerNames"` // Tab names (IDs change per session)
	CreatedAt   int64    `json:"createdAt"`
}
