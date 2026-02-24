package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ClaudeConfig represents the Claude CLI configuration
type ClaudeConfig struct {
	SessionKey string `json:"sessionKey"`
}

// GetClaudeSessionKey reads the session key from Claude CLI config
func GetClaudeSessionKey() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("[GetClaudeSessionKey] ERROR: Failed to get home directory: %v\n", err)
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	// Try multiple possible config locations
	configPaths := []string{
		filepath.Join(homeDir, ".config", "claude", "config.json"),
		filepath.Join(homeDir, ".claude", "config.json"),
		filepath.Join(homeDir, "Library", "Application Support", "Claude", "config.json"),
	}

	fmt.Println("[GetClaudeSessionKey] Searching for Claude config in the following paths:")
	for _, configPath := range configPaths {
		fmt.Printf("[GetClaudeSessionKey] Trying: %s\n", configPath)
		data, err := os.ReadFile(configPath)
		if err != nil {
			fmt.Printf("[GetClaudeSessionKey] Not found or error: %v\n", err)
			continue // Try next path
		}

		fmt.Printf("[GetClaudeSessionKey] Found config file, parsing...\n")
		var config ClaudeConfig
		if err := json.Unmarshal(data, &config); err != nil {
			fmt.Printf("[GetClaudeSessionKey] Failed to parse JSON: %v\n", err)
			continue
		}

		if config.SessionKey != "" {
			fmt.Printf("[GetClaudeSessionKey] SUCCESS: Found session key (length: %d)\n", len(config.SessionKey))
			return config.SessionKey, nil
		}
		fmt.Println("[GetClaudeSessionKey] Config found but sessionKey is empty")
	}

	fmt.Println("[GetClaudeSessionKey] ERROR: Claude session key not found in any location")
	return "", fmt.Errorf("Claude session key not found. Please run 'claude login' first")
}
