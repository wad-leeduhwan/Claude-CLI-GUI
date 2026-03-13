package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SessionInfo holds metadata for a session listing
type SessionInfo struct {
	SessionID    string `json:"sessionId"`
	Timestamp    string `json:"timestamp"`    // first user message time (ISO8601)
	LastActivity string `json:"lastActivity"` // last entry time
	Preview      string `json:"preview"`      // first user message (max 80 chars)
	MessageCount int    `json:"messageCount"` // user+assistant message count
	ProjectPath  string `json:"projectPath"`  // decoded absolute project path
}

// SessionMessage holds a parsed message from a JSONL session file
type SessionMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
}

// EncodeProjectPath encodes an absolute path the same way Claude CLI does:
// replace '/' with '-'. e.g. "/Users/cat/proj" → "-Users-cat-proj"
func EncodeProjectPath(absPath string) string {
	return strings.ReplaceAll(absPath, "/", "-")
}

// DecodeProjectPath reverses EncodeProjectPath:
// "-Users-cat-proj" → "/Users/cat/proj"
func DecodeProjectPath(encoded string) string {
	if encoded == "" {
		return ""
	}
	// encoded always starts with "-" (from leading "/")
	return strings.ReplaceAll(encoded, "-", "/")
}

// ListSessions scans ~/.claude/projects/{encoded}/ for *.jsonl files
// and returns session metadata sorted by LastActivity descending.
func ListSessions(workDir string) ([]SessionInfo, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	encoded := EncodeProjectPath(workDir)
	projectDir := filepath.Join(homeDir, ".claude", "projects", encoded)

	sessions, err := scanProjectDir(projectDir, workDir)
	if err != nil {
		return nil, err
	}

	sortSessionsByActivity(sessions)
	return sessions, nil
}

// ListAllSessions scans all project directories under ~/.claude/projects/
// and returns sessions grouped: current workDir sessions first, then others.
// Both groups are sorted by LastActivity descending.
func ListAllSessions(currentWorkDir string) ([]SessionInfo, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}

	projectsRoot := filepath.Join(homeDir, ".claude", "projects")
	dirs, err := os.ReadDir(projectsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return []SessionInfo{}, nil
		}
		return nil, fmt.Errorf("failed to read projects dir: %w", err)
	}

	currentEncoded := EncodeProjectPath(currentWorkDir)

	var currentSessions []SessionInfo
	var otherSessions []SessionInfo

	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}

		dirName := dir.Name()
		projectDir := filepath.Join(projectsRoot, dirName)
		projectPath := DecodeProjectPath(dirName)

		sessions, err := scanProjectDir(projectDir, projectPath)
		if err != nil {
			fmt.Printf("[SessionScanner] Skipping project %s: %v\n", dirName, err)
			continue
		}

		if dirName == currentEncoded {
			currentSessions = append(currentSessions, sessions...)
		} else {
			otherSessions = append(otherSessions, sessions...)
		}
	}

	sortSessionsByActivity(currentSessions)
	sortSessionsByActivity(otherSessions)

	return append(currentSessions, otherSessions...), nil
}

// scanProjectDir scans a single project directory for JSONL session files.
func scanProjectDir(projectDir, projectPath string) ([]SessionInfo, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []SessionInfo{}, nil
		}
		return nil, fmt.Errorf("failed to read project dir: %w", err)
	}

	var sessions []SessionInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(projectDir, entry.Name())
		info, err := scanSessionMetadata(filePath, projectPath)
		if err != nil {
			fmt.Printf("[SessionScanner] Skipping %s: %v\n", entry.Name(), err)
			continue
		}
		if info != nil {
			sessions = append(sessions, *info)
		}
	}
	return sessions, nil
}

func sortSessionsByActivity(sessions []SessionInfo) {
	sort.Slice(sessions, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339Nano, sessions[i].LastActivity)
		tj, _ := time.Parse(time.RFC3339Nano, sessions[j].LastActivity)
		return ti.After(tj)
	})
}

// jsonlEntry represents a single line in the JSONL session file
type jsonlEntry struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
}

// jsonlMessage represents the message field within a JSONL entry
type jsonlMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// scanSessionMetadata reads a JSONL file and extracts session metadata.
func scanSessionMetadata(filePath, projectPath string) (*SessionInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sessionID := strings.TrimSuffix(filepath.Base(filePath), ".jsonl")

	var firstUserTimestamp string
	var firstUserContent string
	var lastTimestamp string
	messageCount := 0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry jsonlEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if entry.Timestamp != "" {
			lastTimestamp = entry.Timestamp
		}

		if entry.Type != "user" && entry.Type != "assistant" {
			continue
		}

		messageCount++

		if entry.Type == "user" && firstUserContent == "" && entry.Message != nil {
			var msg jsonlMessage
			if err := json.Unmarshal(entry.Message, &msg); err == nil {
				// user content can be string or array (tool_result)
				var strContent string
				if err := json.Unmarshal(msg.Content, &strContent); err == nil {
					firstUserContent = strContent
					firstUserTimestamp = entry.Timestamp
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Skip sessions with no messages
	if messageCount == 0 {
		return nil, nil
	}

	// Truncate preview to 80 chars
	preview := firstUserContent
	if len([]rune(preview)) > 80 {
		preview = string([]rune(preview)[:80]) + "..."
	}

	return &SessionInfo{
		SessionID:    sessionID,
		Timestamp:    firstUserTimestamp,
		LastActivity: lastTimestamp,
		Preview:      preview,
		MessageCount: messageCount,
		ProjectPath:  projectPath,
	}, nil
}

// LoadSessionMessages parses a JSONL session file and returns user+assistant messages.
// For user messages, extracts the string content.
// For assistant messages, concatenates text blocks from the content array.
func LoadSessionMessages(filePath string) ([]SessionMessage, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var messages []SessionMessage

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry jsonlEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		if entry.Type != "user" && entry.Type != "assistant" {
			continue
		}

		if entry.Message == nil {
			continue
		}

		var msg jsonlMessage
		if err := json.Unmarshal(entry.Message, &msg); err != nil {
			continue
		}

		if entry.Type == "user" {
			// User content: string → user message, array → tool_result (skip)
			var strContent string
			if err := json.Unmarshal(msg.Content, &strContent); err == nil {
				messages = append(messages, SessionMessage{
					Role:      "user",
					Content:   strContent,
					Timestamp: entry.Timestamp,
				})
			}
			// If content is array (tool_result), skip
		} else if entry.Type == "assistant" {
			// Assistant content: array of content blocks
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(msg.Content, &blocks); err == nil {
				var textParts []string
				for _, b := range blocks {
					if b.Type == "text" && b.Text != "" {
						textParts = append(textParts, b.Text)
					}
				}
				if len(textParts) > 0 {
					messages = append(messages, SessionMessage{
						Role:      "assistant",
						Content:   strings.Join(textParts, "\n\n"),
						Timestamp: entry.Timestamp,
					})
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return messages, nil
}

// ListAllSessionsExcluding is like ListAllSessions but filters out sessions
// whose UUID matches any key in excludeUUIDs. Used to hide agent sessions.
func ListAllSessionsExcluding(currentWorkDir string, excludeUUIDs map[string]bool) ([]SessionInfo, error) {
	sessions, err := ListAllSessions(currentWorkDir)
	if err != nil {
		return nil, err
	}
	if len(excludeUUIDs) == 0 {
		return sessions, nil
	}

	filtered := make([]SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		if !excludeUUIDs[s.SessionID] {
			filtered = append(filtered, s)
		}
	}
	return filtered, nil
}

// ExtractSessionSlug reads a JSONL session file and returns the first "slug" field found.
// Returns empty string (no error) if no slug exists.
func ExtractSessionSlug(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		slugRaw, ok := raw["slug"]
		if !ok {
			continue
		}

		var slug string
		if err := json.Unmarshal(slugRaw, &slug); err != nil {
			continue
		}
		if slug != "" {
			return slug, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", nil
}

// DeleteSession removes a session JSONL file from disk.
func DeleteSession(projectPath, sessionID string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	encoded := EncodeProjectPath(projectPath)
	jsonlPath := filepath.Join(homeDir, ".claude", "projects", encoded, sessionID+".jsonl")

	if err := os.Remove(jsonlPath); err != nil {
		if os.IsNotExist(err) {
			return nil // already gone
		}
		return fmt.Errorf("failed to delete session file: %w", err)
	}
	fmt.Printf("[SessionScanner] Deleted session %s from %s\n", sessionID[:8], projectPath)
	return nil
}
