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
	"syscall"
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

// StreamResponse represents a streaming JSON response from Claude CLI
type StreamResponse struct {
	Type   string `json:"type"`
	Result string `json:"result,omitempty"` // Final result from "result" type
	Message struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message,omitempty"`
	// For stream_event with --include-partial-messages
	Event *StreamEventData `json:"event,omitempty"`
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

// SendMessage sends a message to Claude CLI and returns the response and turn count.
// The turn count tracks how many agentic turns (tool-use cycles) were used.
func (w *CLIWrapper) SendMessage(ctx context.Context, conversationID, message string, files []string, model string, workDir string, maxTurns int, systemPrompt string, permissionMode string, tools string, onChunk func(string)) (string, int, error) {
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
	if permissionMode != "" {
		args = append(args, "--permission-mode", permissionMode)
	} else {
		args = append(args, "--dangerously-skip-permissions")
	}

	// Pass model flag if specified
	if model != "" {
		args = append(args, "--model", model)
	}

	// Limit turns if specified
	if maxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", maxTurns))
	}

	// Append system prompt (adds to default, doesn't replace)
	if systemPrompt != "" {
		args = append(args, "--append-system-prompt", systemPrompt)
	}

	// Restrict available tools (e.g., "Read,Glob,Grep" for plan mode)
	if tools != "" {
		args = append(args, "--tools", tools)
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
	// Create a new process group so we can kill all child processes
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// killProcessGroup kills the entire process group (parent + all children)
	killProcessGroup := func() {
		if cmd.Process != nil {
			pgid := cmd.Process.Pid
			fmt.Printf("[CLIWrapper] Killing process group (pgid: %d)\n", pgid)
			syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}

	// Use stdout pipe instead of PTY to get clean JSON output
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", 0, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Capture stderr for debugging
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", 0, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return "", 0, fmt.Errorf("failed to start claude: %w", err)
	}

	// Monitor context for cancellation (backup — main loop also handles ctx.Done)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			killProcessGroup()
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
	turnCount := 0
	stalled := false
	stallTimeout := 10 * time.Minute
	timer := time.NewTimer(stallTimeout)
	defer timer.Stop()

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

			if resp.Type == "result" && resp.Result != "" {
				fmt.Printf("[CLIWrapper] Final result (length: %d)\n", len(resp.Result))
				fullResponse = resp.Result
				if onChunk != nil {
					onChunk(fullResponse)
				}
			} else if resp.Type == "assistant" && len(resp.Message.Content) > 0 {
				for _, content := range resp.Message.Content {
					if content.Type == "text" && content.Text != "" {
						fmt.Printf("[CLIWrapper] Assistant message (length: %d)\n", len(content.Text))
						if fullResponse != "" {
							fullResponse += "\n\n"
						}
						fullResponse += content.Text
						if onChunk != nil {
							onChunk(fullResponse)
						}
					}
				}
			}

		case <-timer.C:
			stalled = true
			fmt.Printf("[CLIWrapper] Stall timeout (%v) - no output received, killing process group\n", stallTimeout)
			killProcessGroup()
			break loop

		case <-ctx.Done():
			fmt.Printf("[CLIWrapper] Context cancelled, killing process group\n")
			killProcessGroup()
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
			return w.SendMessage(ctx, conversationID, message, files, model, workDir, maxTurns, systemPrompt, permissionMode, tools, onChunk)
		}
		// If resume failed with no output, fall back to a fresh session
		if fullResponse == "" && isResume {
			fmt.Printf("[CLIWrapper] Resume failed, falling back to fresh session\n")
			w.ClearSession(conversationID)
			return w.SendMessage(ctx, conversationID, message, files, model, workDir, maxTurns, systemPrompt, permissionMode, tools, onChunk)
		}
		if fullResponse != "" {
			if stalled {
				fullResponse += "\n\n---\n⚠️ 응답 대기 시간이 초과되어 프로세스가 중단되었습니다."
			}
			w.markSessionStarted(conversationID)
			return fullResponse, turnCount, nil
		}
		if stalled {
			return "", turnCount, fmt.Errorf("응답 대기 시간 초과 (10분간 출력 없음)")
		}
		return "", turnCount, fmt.Errorf("claude command failed: %w", err)
	}

	w.markSessionStarted(conversationID)
	fmt.Printf("[CLIWrapper] Response received (length: %d, lines: %d, turns: %d)\n", len(fullResponse), lineNum, turnCount)

	return fullResponse, turnCount, nil
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

// loadShellEnv sources the user's shell profile and captures the resulting environment.
func loadShellEnv() []string {
	// Detect the user's login shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh" // macOS default
	}

	// Build a command that sources the profile and prints env
	// -l = login shell (sources profile), -i = interactive (sources rc)
	// We use -l only to avoid side effects of interactive mode
	var profileArg string
	if strings.HasSuffix(shell, "zsh") {
		profileArg = "source ~/.zprofile 2>/dev/null; source ~/.zshrc 2>/dev/null; env"
	} else {
		profileArg = "source ~/.bash_profile 2>/dev/null; source ~/.bashrc 2>/dev/null; env"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, "-c", profileArg)
	cmd.Dir, _ = os.UserHomeDir()
	out, err := cmd.Output()
	if err != nil {
		fmt.Printf("[EnrichedEnv] Failed to source shell profile (%s): %v, falling back to os.Environ()\n", shell, err)
		return nil
	}

	var envVars []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "=") {
			envVars = append(envVars, line)
		}
	}

	fmt.Printf("[EnrichedEnv] Loaded %d env vars from %s profile\n", len(envVars), shell)
	return envVars
}

// EnrichedEnv returns the user's full shell environment (from .zshrc/.bashrc)
// with optional extra KEY=VALUE pairs appended.
// On first call it sources the shell profile and caches the result.
// Falls back to os.Environ() + common paths if sourcing fails.
func EnrichedEnv(extra ...string) []string {
	shellEnvOnce.Do(func() {
		shellEnvCached = loadShellEnv()
	})

	var env []string
	if shellEnvCached != nil {
		env = make([]string, len(shellEnvCached))
		copy(env, shellEnvCached)
	} else {
		// Fallback: os.Environ() + common binary paths
		env = os.Environ()
		extraPaths := []string{
			"/opt/homebrew/bin",
			"/opt/homebrew/sbin",
			"/usr/local/bin",
			"/usr/local/sbin",
		}
		if home, err := os.UserHomeDir(); err == nil {
			extraPaths = append(extraPaths, home+"/.local/bin")
		}
		for i, e := range env {
			if strings.HasPrefix(e, "PATH=") {
				current := e[5:]
				env[i] = "PATH=" + strings.Join(extraPaths, ":") + ":" + current
				return append(env, extra...)
			}
		}
		env = append(env, "PATH="+strings.Join(extraPaths, ":"))
	}

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
