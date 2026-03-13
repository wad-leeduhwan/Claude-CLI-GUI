package persistence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"claude-gui/app/models"
)

const (
	stateVersion = 1
	stateDir     = ".claude-gui"
	stateFile    = "state.json"
)

// SessionMapping holds the CLI session mapping for a tab
type SessionMapping struct {
	SessionID string `json:"sessionID"`
	Started   bool   `json:"started"`
}

// AppState is the top-level structure persisted to disk
type AppState struct {
	Version         int                       `json:"version"`
	SavedAt         time.Time                 `json:"savedAt"`
	Model           string                    `json:"model"`
	Settings        *models.GlobalSettings    `json:"settings"`
	Tabs            []*models.TabState        `json:"tabs"`
	SessionMappings map[string]SessionMapping `json:"sessionMappings"`
}

// Store handles reading and writing the application state to disk
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore creates a Store that persists state to ~/.claude-gui/state.json.
// Returns nil if the directory cannot be created.
func NewStore() *Store {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("[Store] Failed to get home dir: %v\n", err)
		return nil
	}

	dir := filepath.Join(home, stateDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Printf("[Store] Failed to create state dir %s: %v\n", dir, err)
		return nil
	}

	return &Store{
		path: filepath.Join(dir, stateFile),
	}
}

// Save writes the application state to disk atomically (write tmp then rename).
func (s *Store) Save(state *AppState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state.Version = stateVersion
	state.SavedAt = time.Now()

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write tmp file: %w", err)
	}

	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename tmp to state: %w", err)
	}

	return nil
}

// Load reads the application state from disk.
// Returns (nil, nil) on first run (file not found), corrupt JSON, or version mismatch.
func (s *Store) Load() (*AppState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // first run
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}

	var state AppState
	if err := json.Unmarshal(data, &state); err != nil {
		fmt.Printf("[Store] Warning: corrupt state.json, starting fresh: %v\n", err)
		return nil, nil
	}

	if state.Version != stateVersion {
		fmt.Printf("[Store] Warning: version mismatch (file=%d, expected=%d), starting fresh\n", state.Version, stateVersion)
		return nil, nil
	}

	return &state, nil
}
