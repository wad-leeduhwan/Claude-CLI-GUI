package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ansiRegex strips ANSI escape sequences from text
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\][^\x1b]*\x1b\\|\x1b\[[\?]?[0-9;]*[a-zA-Z]`)

// CLIWrapper wraps the Claude CLI command
type CLIWrapper struct {
	sessionIDs     map[string]string // conversationID -> UUID
	sessionStarted map[string]bool   // conversationID -> first message sent
	mu             sync.RWMutex
}

// NewCLIWrapper creates a new Claude CLI wrapper
func NewCLIWrapper() *CLIWrapper {
	return &CLIWrapper{
		sessionIDs:     make(map[string]string),
		sessionStarted: make(map[string]bool),
	}
}

// AskUserQuestionData holds the parsed AskUserQuestion tool invocation
type AskUserQuestionData struct {
	ToolUseID string        `json:"toolUseId"`
	Questions []AskQuestion `json:"questions"`
}

// AskQuestion represents a single question within AskUserQuestion
type AskQuestion struct {
	Question    string      `json:"question"`
	Header      string      `json:"header"`
	Options     []AskOption `json:"options"`
	MultiSelect bool        `json:"multiSelect"`
}

// AskOption represents a selectable option within a question
type AskOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// UsageData holds token usage information from Claude CLI
type UsageData struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// TotalInputTokens returns input_tokens + cache_creation + cache_read
func (u *UsageData) TotalInputTokens() int {
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens
}

// StreamResponse represents a streaming JSON response from Claude CLI
type StreamResponse struct {
	Type  string `json:"type"`
	Index int    `json:"index,omitempty"` // content_block_delta index
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta,omitempty"` // content_block_delta text
	Result string `json:"result,omitempty"` // Final result from "result" type
	Message struct {
		Content []ContentBlock `json:"content"`
		Usage   *UsageData     `json:"usage,omitempty"`
	} `json:"message,omitempty"`
	// For stream_event with --include-partial-messages
	Event *StreamEventData `json:"event,omitempty"`
	Usage *UsageData       `json:"usage,omitempty"` // top-level (result type)
}

// StreamEventData represents the event data in stream_event
type StreamEventData struct {
	Type  string `json:"type"`
	Index int    `json:"index,omitempty"`
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta,omitempty"`
}

// SendMessage sends a message to Claude CLI and returns the response, turn count, and optional pending question.
// The turn count tracks how many agentic turns (tool-use cycles) were used.
// If Claude invokes AskUserQuestion, the pending question data is returned for interactive handling.
// The agents parameter, when non-empty, is passed as --agents JSON to enable sub-agent delegation.
func (w *CLIWrapper) SendMessage(ctx context.Context, conversationID, message string, files []string, model string, workDir string, maxTurns int, systemPrompt string, permissionMode string, tools string, agents string, onChunk func(string), onToolActivity func(string, string), onUsage func(int, int)) (string, int, *AskUserQuestionData, error) {
	sessionID := w.getOrCreateSessionID(conversationID)
	isResume := w.IsSessionStarted(conversationID)

	if isResume {
		fmt.Printf("[CLIWrapper] Resuming session (session: %s, model: %s)\n", sessionID[:8], model)
	} else {
		fmt.Printf("[CLIWrapper] Starting new session (session: %s, model: %s)\n", sessionID[:8], model)
	}

	// Build command arguments
	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
	}

	if isResume {
		args = append(args, "--resume", sessionID)
	} else {
		args = append(args, "--session-id", sessionID)
	}

	// Permission mode: "plan" for read-only, "bypassPermissions" for full access
	if permissionMode == "" {
		permissionMode = "bypassPermissions"
	}
	args = append(args, "--permission-mode", permissionMode)

	// Pass model flag if specified
	if model != "" {
		args = append(args, "--model", model)
	}

	// Limit turns — 0 means unlimited, so pass a large value to override CLI default (100)
	if maxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", maxTurns))
	} else {
		args = append(args, "--max-turns", "999")
	}

	// Append system prompt (adds to default, doesn't replace)
	if systemPrompt != "" {
		args = append(args, "--append-system-prompt", systemPrompt)
	}

	// Restrict available tools (e.g., "Read,Glob,Grep" for plan mode)
	if tools != "" {
		args = append(args, "--tools", tools)
	}

	// Always allow AskUserQuestion in --print mode for GUI interactive handling
	args = append(args, "--allowedTools", "AskUserQuestion")

	// Pass agents JSON for sub-agent delegation (Teams mode)
	if agents != "" {
		args = append(args, "--agents", agents)
	}

	// Include image files using @path syntax for Claude Code CLI
	for _, filePath := range files {
		message = "@" + filePath + " " + message
	}

	// Use "--" to separate flags from the prompt argument
	// This prevents variadic flags (--tools, --append-system-prompt) from consuming the message
	args = append(args, "--", message)

	cmd := exec.Command("claude", args...)
	cmd.Env = EnrichedEnv("TERM=dumb", "NO_COLOR=1")
	if workDir != "" {
		cmd.Dir = workDir
	}
	// killProcess terminates the claude process
	killProcess := func() {
		if cmd.Process != nil {
			fmt.Printf("[CLIWrapper] Killing process (pid: %d)\n", cmd.Process.Pid)
			cmd.Process.Kill()
		}
	}

	// Use stdout pipe instead of PTY to get clean JSON output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Capture stderr for debugging
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", 0, nil, fmt.Errorf("failed to start claude: %w", err)
	}

	// Monitor context for cancellation (backup — main loop also handles ctx.Done)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			killProcess()
		case <-done:
		}
	}()

	// Log stderr in background and capture output for error detection
	var stderrBuf strings.Builder
	go func() {
		stderrScanner := bufio.NewScanner(stderr)
		for stderrScanner.Scan() {
			line := stderrScanner.Text()
			fmt.Printf("[CLIWrapper] STDERR: %s\n", line)
			stderrBuf.WriteString(line)
			stderrBuf.WriteString("\n")
		}
	}()

	// Read stdout via channel with stall timeout detection
	type scanLine struct {
		text string
		err  error
	}
	lines := make(chan scanLine, 10)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024) // 10MB max line size
		for scanner.Scan() {
			lines <- scanLine{text: scanner.Text()}
		}
		if err := scanner.Err(); err != nil {
			lines <- scanLine{err: err}
		}
		close(lines)
	}()

	var fullResponse string
	var streamingText string // 현재 턴의 실시간 스트리밍 텍스트 누적
	var toolActivityText string // 도구 활동 항목 (인라인 표시용)
	var pendingQuestion *AskUserQuestionData
	turnCount := 0
	stalled := false
	var totalInputTokens, totalOutputTokens int
	stallTimeout := 10 * time.Minute
	timer := time.NewTimer(stallTimeout)
	defer timer.Stop()

	// buildDisplayResponse combines fullResponse, streamingText, and tool activity lines.
	buildDisplayResponse := func() string {
		display := fullResponse
		if streamingText != "" {
			if display != "" {
				display += "\n\n" + streamingText
			} else {
				display = streamingText
			}
		}
		if toolActivityText != "" {
			display += "\n\n" + toolActivityText
		}
		return display
	}

	// buildFinalResponse combines fullResponse and streamingText, excluding tool activity
	// (tool activity is stored separately in the ToolUses array)
	buildFinalResponse := func() string {
		display := fullResponse
		if streamingText != "" {
			if display != "" {
				display += "\n\n" + streamingText
			} else {
				display = streamingText
			}
		}
		return display
	}

	lineNum := 0
loop:
	for {
		select {
		case result, ok := <-lines:
			if !ok {
				break loop // scanner finished (pipe closed)
			}
			if result.err != nil {
				fmt.Printf("[CLIWrapper] Scanner error: %v\n", result.err)
				break loop
			}
			timer.Reset(stallTimeout) // Reset stall timer on each line

			lineNum++
			line := strings.TrimSpace(result.text)
			if line == "" {
				continue
			}

			line = stripANSI(line)

			jsonStart := strings.Index(line, "{")
			if jsonStart < 0 {
				fmt.Printf("[CLIWrapper] Line %d: no JSON found: %s\n", lineNum, truncate(line, 100))
				continue
			}
			if jsonStart > 0 {
				line = line[jsonStart:]
			}

			var resp StreamResponse
			if err := json.Unmarshal([]byte(line), &resp); err != nil {
				fmt.Printf("[CLIWrapper] Line %d: JSON parse error: %v (line: %s)\n", lineNum, err, truncate(line, 200))
				continue
			}

			fmt.Printf("[CLIWrapper] Line %d: type=%s\n", lineNum, resp.Type)

			if resp.Type == "user" {
				turnCount++
			}

				// 실시간 스트리밍: content_block_delta에서 텍스트 추출
			if resp.Type == "content_block_delta" && resp.Delta != nil && resp.Delta.Type == "text_delta" && resp.Delta.Text != "" {
				streamingText += resp.Delta.Text
				if onChunk != nil {
					onChunk(buildDisplayResponse())
				}
				continue
			}

			if resp.Type == "result" && resp.Result != "" {
				fmt.Printf("[CLIWrapper] Final result (length: %d)\n", len(resp.Result))
				fullResponse = resp.Result
				if onChunk != nil {
					onChunk(buildDisplayResponse())
				}
			} else if resp.Type == "assistant" && len(resp.Message.Content) > 0 {
				streamingText = "" // 완전한 메시지로 대체되므로 스트리밍 버퍼 리셋
				for _, content := range resp.Message.Content {
					if content.Type == "text" && content.Text != "" {
						fmt.Printf("[CLIWrapper] Assistant message (length: %d)\n", len(content.Text))
						if fullResponse != "" {
							fullResponse += "\n\n"
						}
						fullResponse += content.Text
						if onChunk != nil {
							onChunk(buildDisplayResponse())
						}
					} else if content.Type == "tool_use" && content.Name == "AskUserQuestion" {
						fmt.Printf("[CLIWrapper] AskUserQuestion tool_use detected (id: %s)\n", content.ID)
						var askInput struct {
							Questions []AskQuestion `json:"questions"`
						}
						if err := json.Unmarshal(content.Input, &askInput); err == nil {
							pendingQuestion = &AskUserQuestionData{
								ToolUseID: content.ID,
								Questions: askInput.Questions,
							}
							fmt.Printf("[CLIWrapper] Parsed %d questions from AskUserQuestion\n", len(askInput.Questions))
						} else {
							fmt.Printf("[CLIWrapper] Failed to parse AskUserQuestion input: %v\n", err)
						}
					} else if content.Type == "tool_use" && content.Name != "AskUserQuestion" {
						if onToolActivity != nil {
							detail := extractToolDetail(content.Name, content.Input)
							onToolActivity(content.Name, detail)
						}
						// 인라인으로 도구 활동 추가
						detail := extractToolDetail(content.Name, content.Input)
						entry := fmt.Sprintf("%s **%s** %s", toolIcon(content.Name), content.Name, detail)
						if toolActivityText != "" {
							toolActivityText += "\\\n" + entry
						} else {
							toolActivityText = entry
						}
						if onChunk != nil {
							onChunk(buildDisplayResponse())
						}
					}
				}
				// Extract usage from assistant message (accumulate across turns, include cache tokens)
				if resp.Message.Usage != nil && onUsage != nil {
					u := resp.Message.Usage
					totalInputTokens += u.TotalInputTokens()
					totalOutputTokens += u.OutputTokens
					fmt.Printf("[CLIWrapper] Usage: input=%d (cache_create=%d, cache_read=%d), output=%d, total: in=%d out=%d\n",
						u.InputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens, u.OutputTokens,
						totalInputTokens, totalOutputTokens)
					onUsage(totalInputTokens, totalOutputTokens)
				}
			}
			// Extract usage from result message (accumulate, include cache tokens)
			if resp.Type == "result" && resp.Usage != nil && onUsage != nil {
				u := resp.Usage
				totalInputTokens += u.TotalInputTokens()
				totalOutputTokens += u.OutputTokens
				fmt.Printf("[CLIWrapper] Result usage: input=%d (cache_create=%d, cache_read=%d), output=%d, total: in=%d out=%d\n",
					u.InputTokens, u.CacheCreationInputTokens, u.CacheReadInputTokens, u.OutputTokens,
					totalInputTokens, totalOutputTokens)
				onUsage(totalInputTokens, totalOutputTokens)
			}

		case <-timer.C:
			stalled = true
			fmt.Printf("[CLIWrapper] Stall timeout (%v) - no output received, killing process group\n", stallTimeout)
			killProcess()
			break loop

		case <-ctx.Done():
			fmt.Printf("[CLIWrapper] Context cancelled, killing process group\n")
			killProcess()
			if !isResume {
				w.markSessionStarted(conversationID)
			}
			break loop
		}
	}

	// Wait for process to complete
	if err := cmd.Wait(); err != nil {
		fmt.Printf("[CLIWrapper] Process exit error: %v\n", err)
		// Detect "already in use" error and retry with --resume
		stderrStr := stderrBuf.String()
		if !isResume && strings.Contains(stderrStr, "already in use") {
			fmt.Printf("[CLIWrapper] Session already in use, retrying with --resume\n")
			w.markSessionStarted(conversationID)
			return w.SendMessage(ctx, conversationID, message, files, model, workDir, maxTurns, systemPrompt, permissionMode, tools, agents, onChunk, onToolActivity, onUsage)
		}
		// If resume failed with no output, fall back to a fresh session
		if fullResponse == "" && isResume && pendingQuestion == nil {
			fmt.Printf("[CLIWrapper] Resume failed, falling back to fresh session\n")
			w.ClearSession(conversationID)
			return w.SendMessage(ctx, conversationID, message, files, model, workDir, maxTurns, systemPrompt, permissionMode, tools, agents, onChunk, onToolActivity, onUsage)
		}
		if fullResponse != "" || pendingQuestion != nil {
			if stalled {
				fullResponse += "\n\n---\n⚠️ 응답 대기 시간이 초과되어 프로세스가 중단되었습니다."
			}
			w.markSessionStarted(conversationID)
			return buildFinalResponse(), turnCount, pendingQuestion, nil
		}
		if stalled {
			return "", turnCount, nil, fmt.Errorf("응답 대기 시간 초과 (10분간 출력 없음)")
		}
		return "", turnCount, nil, fmt.Errorf("claude command failed: %w", err)
	}

	w.markSessionStarted(conversationID)
	fmt.Printf("[CLIWrapper] Response received (length: %d, lines: %d, turns: %d, pendingQuestion: %v)\n", len(fullResponse), lineNum, turnCount, pendingQuestion != nil)

	return buildFinalResponse(), turnCount, pendingQuestion, nil
}


// toolIcon returns an emoji icon for a given tool name.
func toolIcon(name string) string {
	icons := map[string]string{
		"Read": "📖", "Write": "📁", "Edit": "✏️", "Bash": "💻",
		"Glob": "🔍", "Grep": "🔎", "WebSearch": "🌐", "WebFetch": "🌐", "Task": "📋",
	}
	if icon, ok := icons[name]; ok {
		return icon
	}
	return "⚙️"
}

// extractToolDetail extracts a human-readable detail string from a tool_use input
func extractToolDetail(toolName string, input json.RawMessage) string {
	var raw map[string]interface{}
	if err := json.Unmarshal(input, &raw); err != nil {
		return ""
	}
	switch toolName {
	case "Read", "Write", "Edit":
		if fp, ok := raw["file_path"].(string); ok {
			return shortPath(fp)
		}
	case "Bash":
		if cmd, ok := raw["command"].(string); ok {
			if len(cmd) > 60 {
				cmd = cmd[:60] + "..."
			}
			return cmd
		}
	case "Glob":
		if p, ok := raw["pattern"].(string); ok {
			return p
		}
	case "Grep":
		if p, ok := raw["pattern"].(string); ok {
			return p
		}
	case "WebSearch":
		if q, ok := raw["query"].(string); ok {
			return q
		}
	case "Task":
		if d, ok := raw["description"].(string); ok {
			if len(d) > 60 {
				d = d[:60] + "..."
			}
			return d
		}
	}
	return ""
}

// shortPath returns just the filename from a full path
func shortPath(fullPath string) string {
	if i := strings.LastIndex(fullPath, "/"); i >= 0 {
		return fullPath[i+1:]
	}
	return fullPath
}

// getOrCreateSessionID gets or creates a session ID for a conversation
func (w *CLIWrapper) getOrCreateSessionID(conversationID string) string {
	w.mu.Lock()
	defer w.mu.Unlock()

	if sessionID, exists := w.sessionIDs[conversationID]; exists {
		return sessionID
	}

	// Create new UUID for this conversation
	sessionID := uuid.New().String()
	w.sessionIDs[conversationID] = sessionID

	fmt.Printf("[CLIWrapper] Created new session ID for %s: %s\n", conversationID, sessionID[:8])

	return sessionID
}

// IsSessionStarted returns whether a first message has been sent for this conversation
func (w *CLIWrapper) IsSessionStarted(conversationID string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.sessionStarted[conversationID]
}

// markSessionStarted records that the first message has been sent successfully
func (w *CLIWrapper) markSessionStarted(conversationID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sessionStarted[conversationID] = true
}

// ClearSession removes the session ID and started flag for a conversation
func (w *CLIWrapper) ClearSession(conversationID string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	delete(w.sessionIDs, conversationID)
	delete(w.sessionStarted, conversationID)
	fmt.Printf("[CLIWrapper] Cleared session for %s\n", conversationID)
}

// SetSession assigns an existing session ID and marks it as started (resume mode).
func (w *CLIWrapper) SetSession(conversationID, sessionID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sessionIDs[conversationID] = sessionID
	w.sessionStarted[conversationID] = true
	fmt.Printf("[CLIWrapper] Set session for %s: %s\n", conversationID, sessionID[:8])
}

// SessionState holds the persisted session info for a single conversation
type SessionState struct {
	SessionID string
	Started   bool
}

// ExportSessions returns a snapshot of all session mappings (for persistence).
// Agent sessions (prefixed with __agent__) are excluded.
func (w *CLIWrapper) ExportSessions() map[string]SessionState {
	w.mu.RLock()
	defer w.mu.RUnlock()

	out := make(map[string]SessionState, len(w.sessionIDs))
	for convID, sid := range w.sessionIDs {
		if strings.HasPrefix(convID, AgentSessionPrefix) {
			continue
		}
		out[convID] = SessionState{
			SessionID: sid,
			Started:   w.sessionStarted[convID],
		}
	}
	return out
}

// GetAgentSessionUUIDs returns the set of session UUIDs used by background agents.
func (w *CLIWrapper) GetAgentSessionUUIDs() map[string]bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	uuids := make(map[string]bool)
	for convID, sid := range w.sessionIDs {
		if strings.HasPrefix(convID, AgentSessionPrefix) {
			uuids[sid] = true
		}
	}
	return uuids
}

// ImportSessions restores session mappings from a persisted snapshot
func (w *CLIWrapper) ImportSessions(mappings map[string]SessionState) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for convID, state := range mappings {
		w.sessionIDs[convID] = state.SessionID
		w.sessionStarted[convID] = state.Started
	}
	fmt.Printf("[CLIWrapper] Imported %d session mappings\n", len(mappings))
}

// Close cleans up resources
func (w *CLIWrapper) Close() error {
	// Nothing to clean up for CLI wrapper
	return nil
}

// shellEnvCache caches the result of sourcing the user's shell profile.
// Computed once on first call and reused for all subsequent calls.
var (
	shellEnvOnce   sync.Once
	shellEnvCached []string // "KEY=VALUE" pairs from the user's login shell
)

// EnrichedEnv returns the current environment with common binary paths prepended.
// This ensures claude CLI can be found in Homebrew, nvm, etc. paths.
func EnrichedEnv(extra ...string) []string {
	shellEnvOnce.Do(func() {
		env := os.Environ()
		extraPaths := []string{
			"/opt/homebrew/bin",
			"/opt/homebrew/sbin",
			"/usr/local/bin",
			"/usr/local/sbin",
		}
		if home, err := os.UserHomeDir(); err == nil {
			extraPaths = append(extraPaths,
				home+"/.local/bin",
				home+"/.npm-global/bin",
				home+"/.volta/bin",
				home+"/.bun/bin",
				home+"/.fnm/aliases/default/bin",
				home+"/.yarn/bin",
				home+"/.pnpm",
				home+"/Library/pnpm",
				home+"/.proto/shims",
				home+"/.asdf/shims",
			)
			// nvm: find active node version bin path
			nvmDir := home + "/.nvm/versions/node"
			if entries, err := os.ReadDir(nvmDir); err == nil {
				for i := len(entries) - 1; i >= 0; i-- {
					if entries[i].IsDir() {
						extraPaths = append(extraPaths, nvmDir+"/"+entries[i].Name()+"/bin")
						break // latest version only
					}
				}
			}
			// fnm: find current version bin path
			fnmDir := home + "/Library/Application Support/fnm/node-versions"
			if entries, err := os.ReadDir(fnmDir); err == nil {
				for i := len(entries) - 1; i >= 0; i-- {
					if entries[i].IsDir() {
						extraPaths = append(extraPaths, fnmDir+"/"+entries[i].Name()+"/installation/bin")
						break
					}
				}
			}
		}
		for i, e := range env {
			if strings.HasPrefix(e, "PATH=") {
				env[i] = "PATH=" + strings.Join(extraPaths, ":") + ":" + e[5:]
				shellEnvCached = env
				return
			}
		}
		env = append(env, "PATH="+strings.Join(extraPaths, ":"))
		shellEnvCached = env
	})

	env := make([]string, len(shellEnvCached))
	copy(env, shellEnvCached)
	return append(env, extra...)
}

// stripANSI removes ANSI escape sequences from a string
func stripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// truncate shortens a string for logging
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// drainReader reads and discards remaining data from a reader
func drainReader(r io.Reader) {
	io.Copy(io.Discard, r)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
