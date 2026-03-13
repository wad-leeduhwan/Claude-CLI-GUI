package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"claude-gui/app/claude"
	"claude-gui/app/models"
	"claude-gui/app/persistence"
	"claude-gui/app/utils"
	"claude-gui/app/websocket"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Allowed models list
var allowedModels = []string{
	"claude-sonnet-4-5-20250929",
	"claude-haiku-4-5-20251001",
	"claude-opus-4-6",
}

// UpdateInfo holds version comparison result for the frontend
type UpdateInfo struct {
	CurrentVersion  string `json:"currentVersion"`
	LatestVersion   string `json:"latestVersion"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

// App struct
type App struct {
	ctx                 context.Context
	tabs                map[string]*models.TabState
	tabsMu              sync.RWMutex
	settings            *models.GlobalSettings
	claude              *claude.Service
	wsServer            *websocket.Server
	model               string
	cancelFuncs         map[string]context.CancelFunc
	cancelMu            sync.Mutex
	orchestrationCancel map[string]context.CancelFunc
	store               *persistence.Store
	version             string
	buildDate           string
	agentService        *claude.AgentService
	cachedReleaseNotes  []ReleaseNote
}

// NewApp creates a new App application struct
func NewApp(version, buildDate string) *App {
	return &App{
		tabs: make(map[string]*models.TabState),
		settings: &models.GlobalSettings{
			PlanModeDefault: false,
			TabSettings:     make(map[string]bool),
		},
		claude:              claude.NewService(),
		wsServer:            websocket.NewServer(9876),
		model:               "claude-sonnet-4-20250514",
		cancelFuncs:         make(map[string]context.CancelFunc),
		orchestrationCancel: make(map[string]context.CancelFunc),
		store:               persistence.NewStore(), // nil on failure (graceful degradation)
		version:             version,
		buildDate:           buildDate,
	}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Enrich current process PATH from user's shell profile.
	// This ensures GetClaudeVersion() and other direct exec.Command calls also
	// find binaries in Homebrew, nvm, etc. paths.
	for _, e := range claude.EnrichedEnv() {
		if strings.HasPrefix(e, "PATH=") {
			os.Setenv("PATH", e[5:])
			fmt.Printf("[App.startup] PATH enriched from shell profile\n")
			break
		}
	}
	fmt.Printf("[App.startup] PATH: %s\n", os.Getenv("PATH"))

	// Start WebSocket server for real-time streaming
	if err := a.wsServer.Start(); err != nil {
		fmt.Printf("Failed to start WebSocket server: %v\n", err)
	}

	// Try to restore previous state
	restored := false
	if a.store != nil {
		if state, err := a.store.Load(); err == nil && state != nil && len(state.Tabs) > 0 {
			for _, tab := range state.Tabs {
				tab.Orchestrator = nil // ephemeral, not restored
				tab.TeamsState = nil   // ephemeral, not restored
				if tab.Messages == nil {
					tab.Messages = []models.Message{}
				}
				if tab.ContextFiles == nil {
					tab.ContextFiles = []string{}
				}
				a.tabs[tab.ID] = tab
			}
			if state.Settings != nil {
				if state.Settings.TabSettings == nil {
					state.Settings.TabSettings = make(map[string]bool)
				}
				a.settings = state.Settings
			}
			if state.Model != "" {
				a.model = state.Model
			}
			// Restore CLI session mappings
			if len(state.SessionMappings) > 0 {
				cliMappings := make(map[string]claude.SessionState, len(state.SessionMappings))
				for k, v := range state.SessionMappings {
					cliMappings[k] = claude.SessionState{
						SessionID: v.SessionID,
						Started:   v.Started,
					}
				}
				a.claude.ImportSessions(cliMappings)
			}
			restored = true
			fmt.Printf("[App.startup] Restored %d tabs from state.json\n", len(state.Tabs))
		}
	}

	if !restored {
		homeDir, _ := os.UserHomeDir()
		tabID := "conversation-1"
		a.tabs[tabID] = &models.TabState{
			ID:       tabID,
			Name:     "대화 1",
			Messages: []models.Message{},
			IsActive: false,
			WorkDir:  homeDir,
		}
		a.settings.TabSettings[tabID] = false
	}

	// Sync initial model to claude service
	a.claude.SetModel(a.model)

	// Initialize Claude service in background
	go func() {
		// Initialize in headless mode by default
		if err := a.claude.Initialize(true); err != nil {
			fmt.Printf("Failed to initialize Claude service: %v\n", err)
			fmt.Println("You can try manual authentication later.")
		} else {
			fmt.Println("Claude service initialized successfully")
		}
	}()

	// Initialize background agent service
	agentModel := a.settings.AgentModel
	if agentModel == "" {
		agentModel = "claude-haiku-4-5-20251001"
		a.settings.AgentModel = agentModel
	}
	a.agentService = claude.NewAgentService(a.claude, agentModel)
	a.agentService.OnResult = a.handleAgentResult
	a.agentService.Start(2)
}

// GetWebSocketPort returns the WebSocket server port for frontend connection
func (a *App) GetWebSocketPort() int {
	return a.wsServer.GetPort()
}

// GetAppVersion returns the app build version string
func (a *App) GetAppVersion() string {
	if a.version == "dev" {
		return "dev"
	}
	return fmt.Sprintf("%s (%s)", a.version, a.buildDate)
}

// GetClaudeVersion returns the installed Claude CLI version string
func (a *App) GetClaudeVersion() string {
	cmd := exec.Command("claude", "--version")
	cmd.Env = claude.EnrichedEnv()
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// extractVersion extracts a semver string (e.g. "1.0.23") from claude --version output.
// The output may be just "1.0.23" or "claude-code 1.0.23".
func extractVersion(raw string) string {
	re := regexp.MustCompile(`(\d+\.\d+\.\d+)`)
	match := re.FindString(strings.TrimSpace(raw))
	return match
}

// compareSemver returns true if current < latest (i.e. update available).
func compareSemver(current, latest string) bool {
	parseParts := func(v string) (int, int, int) {
		parts := strings.SplitN(v, ".", 3)
		if len(parts) != 3 {
			return 0, 0, 0
		}
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		patch, _ := strconv.Atoi(parts[2])
		return major, minor, patch
	}

	cMajor, cMinor, cPatch := parseParts(current)
	lMajor, lMinor, lPatch := parseParts(latest)

	if cMajor != lMajor {
		return cMajor < lMajor
	}
	if cMinor != lMinor {
		return cMinor < lMinor
	}
	return cPatch < lPatch
}

// CheckForUpdate checks the current Claude CLI version against the latest on npm.
// Returns UpdateInfo with version details. On any error, returns UpdateAvailable: false.
func (a *App) CheckForUpdate() UpdateInfo {
	// 1. Get current version
	cmd := exec.Command("claude", "--version")
	cmd.Env = claude.EnrichedEnv()
	out, err := cmd.Output()
	if err != nil {
		return UpdateInfo{CurrentVersion: "unknown", UpdateAvailable: false}
	}
	currentVersion := extractVersion(string(out))
	if currentVersion == "" {
		return UpdateInfo{CurrentVersion: strings.TrimSpace(string(out)), UpdateAvailable: false}
	}

	// 2. Fetch latest version from npm registry
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("https://registry.npmjs.org/@anthropic-ai/claude-code/latest")
	if err != nil {
		return UpdateInfo{CurrentVersion: currentVersion, UpdateAvailable: false}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return UpdateInfo{CurrentVersion: currentVersion, UpdateAvailable: false}
	}

	var npmResult struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &npmResult); err != nil {
		return UpdateInfo{CurrentVersion: currentVersion, UpdateAvailable: false}
	}

	latestVersion := extractVersion(npmResult.Version)
	if latestVersion == "" {
		return UpdateInfo{CurrentVersion: currentVersion, UpdateAvailable: false}
	}

	// 3. Compare
	updateAvailable := compareSemver(currentVersion, latestVersion)

	return UpdateInfo{
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVersion,
		UpdateAvailable: updateAvailable,
	}
}

// ReleaseNote holds a single release note entry from GitHub Releases
type ReleaseNote struct {
	Version     string `json:"version"`
	PublishedAt string `json:"publishedAt"`
	Body        string `json:"body"`
}

// GetReleaseNotes fetches release notes from the Claude Code changelog page
// for versions between currentVersion (exclusive) and latestVersion (inclusive).
func (a *App) GetReleaseNotes(currentVersion, latestVersion string) []ReleaseNote {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://code.claude.com/docs/en/changelog")
	if err != nil {
		return []ReleaseNote{}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []ReleaseNote{}
	}

	html := string(body)

	// Parse changelog: split by <h2> tags to get version sections
	h2Re := regexp.MustCompile(`(?i)<h2[^>]*>([\d]+\.[\d]+\.[\d]+)</h2>`)
	h2Matches := h2Re.FindAllStringSubmatchIndex(html, -1)

	var notes []ReleaseNote
	for i, match := range h2Matches {
		ver := html[match[2]:match[3]]

		// Filter: current < ver <= latest
		if !compareSemver(currentVersion, ver) || compareSemver(latestVersion, ver) {
			continue
		}

		// Extract the section between this <h2> and the next <h2>
		sectionStart := match[1]
		sectionEnd := len(html)
		if i+1 < len(h2Matches) {
			sectionEnd = h2Matches[i+1][0]
		}
		section := html[sectionStart:sectionEnd]

		// Extract <li> contents
		liRe := regexp.MustCompile(`(?i)<li[^>]*>(.*?)</li>`)
		liMatches := liRe.FindAllStringSubmatch(section, -1)

		var lines []string
		for _, li := range liMatches {
			text := stripHTMLTags(li[1])
			text = strings.TrimSpace(text)
			if text != "" {
				lines = append(lines, "- "+text)
			}
		}

		notes = append(notes, ReleaseNote{
			Version: ver,
			Body:    strings.Join(lines, "\n"),
		})
	}

	// Sort descending by version (latest first)
	sort.Slice(notes, func(i, j int) bool {
		return compareSemver(notes[j].Version, notes[i].Version)
	})

	if notes == nil {
		return []ReleaseNote{}
	}
	a.cachedReleaseNotes = notes
	return notes
}

// OpenChangelogPage opens the Claude Code changelog page in the default browser.
func (a *App) OpenChangelogPage() {
	runtime.BrowserOpenURL(a.ctx, "https://code.claude.com/docs/en/changelog")
}

// TranslateReleaseNotes translates cached release note bodies to Korean
// using the Claude CLI (one-shot --print mode). On failure, the original notes are returned.
func (a *App) TranslateReleaseNotes() []ReleaseNote {
	notes := a.cachedReleaseNotes
	if len(notes) == 0 {
		return notes
	}

	// Build a single prompt with all note bodies separated by ---
	var parts []string
	for _, n := range notes {
		parts = append(parts, n.Body)
	}
	combined := strings.Join(parts, "\n---\n")

	prompt := fmt.Sprintf("Translate the following release notes to Korean. Keep the `- ` bullet format exactly. Separate each version block with `---` on its own line. Do not add any extra text or explanation.\n\n%s", combined)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude", "--print", "--model", "claude-haiku-4-5-20251001", "--max-turns", "1", prompt)
	cmd.Env = claude.EnrichedEnv()

	out, err := cmd.Output()
	if err != nil {
		fmt.Printf("[TranslateReleaseNotes] CLI translation failed: %v\n", err)
		return notes
	}

	result := strings.TrimSpace(string(out))
	if result == "" {
		return notes
	}

	// Split response by --- and map back to notes
	translated := strings.Split(result, "---")

	ret := make([]ReleaseNote, len(notes))
	copy(ret, notes)
	for i := range ret {
		if i < len(translated) {
			body := strings.TrimSpace(translated[i])
			if body != "" {
				ret[i].Body = body
			}
		}
	}

	return ret
}

// stripHTMLTags removes all HTML tags from a string, preserving text content.
func stripHTMLTags(s string) string {
	tagRe := regexp.MustCompile(`<[^>]*>`)
	result := tagRe.ReplaceAllString(s, "")
	// Decode common HTML entities
	result = strings.ReplaceAll(result, "&amp;", "&")
	result = strings.ReplaceAll(result, "&lt;", "<")
	result = strings.ReplaceAll(result, "&gt;", ">")
	result = strings.ReplaceAll(result, "&quot;", "\"")
	result = strings.ReplaceAll(result, "&#39;", "'")
	return result
}

// UpdateResult holds the result of an auto-update attempt
type UpdateResult struct {
	Success       bool   `json:"success"`
	Output        string `json:"output"`
	Error         string `json:"error"`
	NewVersion    string `json:"newVersion"`
	InstallMethod string `json:"installMethod"`
}

// DetectInstallMethod detects whether Claude CLI was installed via npm or Homebrew.
func (a *App) DetectInstallMethod() string {
	cmd := exec.Command("which", "claude")
	cmd.Env = claude.EnrichedEnv()
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	path := strings.TrimSpace(string(out))
	if strings.Contains(path, "homebrew") || strings.Contains(path, "Cellar") {
		return "brew"
	}
	return "npm"
}

// UpdateClaude performs an automatic update of the Claude CLI.
func (a *App) UpdateClaude() UpdateResult {
	method := a.DetectInstallMethod()

	var cmd *exec.Cmd
	switch method {
	case "brew":
		cmd = exec.Command("brew", "upgrade", "claude-code")
	case "npm":
		cmd = exec.Command("npm", "install", "-g", "@anthropic-ai/claude-code")
	default:
		return UpdateResult{
			Success:       false,
			Error:         "설치 방법을 감지할 수 없습니다",
			InstallMethod: method,
		}
	}

	cmd.Env = claude.EnrichedEnv()
	out, err := cmd.CombinedOutput()
	output := string(out)

	if err != nil {
		return UpdateResult{
			Success:       false,
			Output:        output,
			Error:         err.Error(),
			InstallMethod: method,
		}
	}

	// Get the new version after update
	newVersion := a.GetClaudeVersion()

	return UpdateResult{
		Success:       true,
		Output:        output,
		NewVersion:    newVersion,
		InstallMethod: method,
	}
}

// GetTabs returns all tab states
func (a *App) GetTabs() []models.TabState {
	fmt.Println("[App.GetTabs] Called")
	tabs := make([]models.TabState, 0, len(a.tabs))

	// Add all conversation tabs
	// Sort by ID to maintain order
	maxNum := 0
	for id := range a.tabs {
		var num int
		if _, err := fmt.Sscanf(id, "conversation-%d", &num); err == nil {
			if num > maxNum {
				maxNum = num
			}
		}
	}

	for i := 1; i <= maxNum; i++ {
		tabID := fmt.Sprintf("conversation-%d", i)
		if tab, ok := a.tabs[tabID]; ok {
			fmt.Printf("[App.GetTabs] Tab %s has %d messages\n", tabID, len(tab.Messages))
			tabs = append(tabs, *tab)
		}
	}

	fmt.Printf("[App.GetTabs] Returning %d tabs\n", len(tabs))
	return tabs
}

// GetSettings returns current global settings
func (a *App) GetSettings() models.GlobalSettings {
	return *a.settings
}

// UpdateSettings updates the global settings
func (a *App) UpdateSettings(settings models.GlobalSettings) error {
	a.settings = &settings
	go a.saveState()
	return nil
}

// SendMessage sends a message to Claude
func (a *App) SendMessage(tabID, message string, files []string) error {
	fmt.Printf("[App.SendMessage] Called with tabID=%s, message=%s, files=%v\n", tabID, message, files)

	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	// Check if Claude service is initialized
	if !a.claude.IsInitialized() {
		fmt.Println("[App.SendMessage] ERROR: Claude service not initialized")
		return fmt.Errorf("Claude service not initialized. Please restart the application.")
	}

	fmt.Println("[App.SendMessage] Adding user message to tab")

	// Add user message to tab
	userMsg := models.Message{
		Role:        "user",
		Content:     message,
		Attachments: files,
		Timestamp:   0, // TODO: Add timestamp
	}
	tab.Messages = append(tab.Messages, userMsg)

	// Update the tab in the map
	a.tabs[tabID] = tab

	// Emit event to show user message immediately
	fmt.Println("[App.SendMessage] Emitting user-message-added event")
	runtime.EventsEmit(a.ctx, "user-message-added", tabID)

	fmt.Println("[App.SendMessage] Sending to Claude CLI...")

	// CLI session maintains full tool-call history, so only send the new message.
	messageToSend := message

	// If session was reset but there are previous messages (e.g. after TruncateMessages/retry),
	// include previous context summary so Claude has some continuity.
	if !a.claude.IsSessionStarted(tabID) && len(tab.Messages) > 1 {
		var contextParts []string
		for _, msg := range tab.Messages[:len(tab.Messages)-1] {
			if msg.Role == "user" {
				contextParts = append(contextParts, "User: "+msg.Content)
			} else if msg.Role == "assistant" {
				content := msg.Content
				if len(content) > 500 {
					content = content[:500] + "... [truncated]"
				}
				contextParts = append(contextParts, "Assistant: "+content)
			}
		}
		if len(contextParts) > 0 {
			messageToSend = "Previous conversation context:\n" +
				strings.Join(contextParts, "\n\n") +
				"\n\n---\nNew message:\n" + message
		}
	}

	// Build system prompt and tool restrictions based on mode
	permissionMode := "bypassPermissions"

	// Base system prompt for GUI environment
	guiContext := "You are running inside a GUI chat application. " +
		"IMPORTANT: When you need to ask the user a question, clarify requirements, or let the user choose between approaches, " +
		"you MUST ALWAYS use the AskUserQuestion tool. NEVER output questions as plain numbered lists or bullet points in plain text. " +
		"The GUI renders AskUserQuestion as an interactive selection UI with clickable options. " +
		"If the user's request is ambiguous or multiple approaches are possible, use AskUserQuestion BEFORE taking action. " +
		"The user will respond in the next message."

	var systemPrompt string
	var tools string // empty = all tools (default)

	if tab.PlanMode {
		permissionMode = "plan"
		systemPrompt = guiContext + "\n\n[PLAN MODE] You are in read-only plan mode. You MUST NOT modify any files.\n" +
			"Follow this workflow thoroughly:\n\n" +
			"1. EXPLORE PHASE: Use Task tool with subagent_type='Explore' to deeply analyze the codebase. " +
			"Launch multiple explore agents in parallel when different areas need investigation. " +
			"Read key files directly with Read tool. Use Glob and Grep extensively to find all relevant code.\n\n" +
			"2. DESIGN PHASE: Use Task tool with subagent_type='Plan' to design the implementation approach. " +
			"Consider multiple perspectives and trade-offs.\n\n" +
			"3. CLARIFICATION: If the user's request is ambiguous or you need decisions, " +
			"STOP and ask the user BEFORE proceeding. Do NOT assume — always ask.\n" +
			"Format questions with numbered options so the user can click to choose:\n" +
			"Example:\n어떤 방식을 사용할까요?\n1. Option A description\n2. Option B description\n3. Option C description\n\n" +
			"4. FINAL PLAN: Present a detailed implementation plan including:\n" +
			"   - Context: why this change is needed\n" +
			"   - Files to modify with specific line ranges and changes\n" +
			"   - Step-by-step implementation order\n" +
			"   - Trade-offs and alternatives considered\n" +
			"   - Verification steps\n\n" +
			"Take your time. Thoroughness is more important than speed. " +
			"A 5-minute deep analysis is far more valuable than a 1-minute shallow answer."

		// Read-only tools + Task (for sub-agent exploration/planning)
		tools = "Read,Glob,Grep,WebSearch,WebFetch,Task"
	} else {
		systemPrompt = guiContext
	}

	// Append context files content to system prompt
	var contextParts []string
	for _, cf := range tab.ContextFiles {
		content, err := os.ReadFile(cf)
		if err == nil {
			contextParts = append(contextParts, fmt.Sprintf("[Context: %s]\n%s", filepath.Base(cf), string(content)))
		}
	}
	if len(contextParts) > 0 {
		systemPrompt += "\n\n--- Context Files ---\n" + strings.Join(contextParts, "\n\n")
	}

	fmt.Printf("[App.SendMessage] Message to send: %d messages, %d chars, planMode: %v, tools: %s, contextFiles: %d\n", len(tab.Messages), len(messageToSend), tab.PlanMode, tools, len(tab.ContextFiles))

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelMu.Lock()
	a.cancelFuncs[tabID] = cancel
	a.cancelMu.Unlock()
	defer func() {
		cancel()
		a.cancelMu.Lock()
		delete(a.cancelFuncs, tabID)
		a.cancelMu.Unlock()
	}()

	// Send streaming start via WebSocket (real-time) and Wails event (backup)
	fmt.Printf("[App.SendMessage] Sending streaming-start via WebSocket for tabID=%s\n", tabID)
	a.wsServer.SendStreamStart(tabID)
	runtime.EventsEmit(a.ctx, "streaming-start", tabID)

	// Send full conversation context to Claude CLI with streaming callback
	var streamingContent string
	var chunkCount int
	var lastInputTokens, lastOutputTokens int
	var toolUses []models.ToolUse
	sendStartTime := time.Now()

	maxTurns := 0 // 0 = unlimited — stall timeout + manual cancel로 충분

	response, turnCount, pendingQ, err := a.claude.SendMessage(ctx, tabID, messageToSend, files, tab.WorkDir, maxTurns, systemPrompt, permissionMode, tools, "", func(chunk string) {
		chunkCount++
		streamingContent = chunk
		fmt.Printf("[App.SendMessage] Chunk %d received (length=%d) - sending via WebSocket\n", chunkCount, len(chunk))

		// Send chunk via WebSocket for real-time delivery
		a.wsServer.SendStreamChunk(tabID, streamingContent)
	}, func(toolName, detail string) {
		toolUses = append(toolUses, models.ToolUse{ToolName: toolName, Detail: detail})
		a.wsServer.SendToolActivity(tabID, toolName, detail)
	}, func(input, output int) {
		lastInputTokens = input
		lastOutputTokens = output
		a.wsServer.SendTokenUsage(tabID, input, output)
	})
	if err != nil {
		// Check if cancelled by user
		if ctx.Err() == context.Canceled {
			fmt.Printf("[App.SendMessage] Cancelled by user for tab: %s\n", tabID)
			a.wsServer.SendStreamEnd(tabID)
			runtime.EventsEmit(a.ctx, "streaming-end", tabID)
			return nil
		}
		fmt.Printf("[App.SendMessage] ERROR: %v\n", err)
		a.wsServer.SendStreamError(tabID, err.Error())
		runtime.EventsEmit(a.ctx, "streaming-end", tabID)
		return fmt.Errorf("failed to send message to Claude: %w", err)
	}

	// Handle AskUserQuestion: send question to frontend and wait for user response
	if pendingQ != nil {
		fmt.Printf("[App.SendMessage] AskUserQuestion detected (%d questions), sending to frontend\n", len(pendingQ.Questions))

		// Don't save partial text response — the AskUserQuestion panel replaces it

		// Convert to websocket types
		wsQuestions := make([]websocket.AskUserQuestionItem, len(pendingQ.Questions))
		for i, q := range pendingQ.Questions {
			wsOpts := make([]websocket.AskUserQuestionOption, len(q.Options))
			for j, opt := range q.Options {
				wsOpts[j] = websocket.AskUserQuestionOption{
					Label:       opt.Label,
					Description: opt.Description,
				}
			}
			wsQuestions[i] = websocket.AskUserQuestionItem{
				Question:    q.Question,
				Header:      q.Header,
				Options:     wsOpts,
				MultiSelect: q.MultiSelect,
			}
		}

		// Send question to frontend via WebSocket
		a.wsServer.SendAskUserQuestion(tabID, pendingQ.ToolUseID, wsQuestions)
		a.wsServer.SendStreamEnd(tabID)
		runtime.EventsEmit(a.ctx, "streaming-end", tabID)
		return nil // Not an error — waiting for user response
	}

	fmt.Printf("[App.SendMessage] Response received (length=%d), total chunks=%d, turns=%d/%d\n", len(response), chunkCount, turnCount, maxTurns)
	fmt.Printf("[App.SendMessage] ===== RAW RESPONSE START =====\n%s\n[App.SendMessage] ===== RAW RESPONSE END =====\n", response)

	// Send final chunk via WebSocket
	if streamingContent != "" {
		fmt.Printf("[App.SendMessage] Sending final chunk via WebSocket\n")
		a.wsServer.SendStreamChunk(tabID, streamingContent)
	}

	// Small delay to let frontend process the final chunk
	time.Sleep(50 * time.Millisecond)

	// Send streaming end via WebSocket and Wails event
	fmt.Printf("[App.SendMessage] Sending streaming-end via WebSocket for tabID=%s\n", tabID)
	a.wsServer.SendStreamEnd(tabID)
	runtime.EventsEmit(a.ctx, "streaming-end", tabID)

	// Add assistant message to tab
	durationMs := time.Since(sendStartTime).Milliseconds()
	responseMsg := models.Message{
		Role:         "assistant",
		Content:      response,
		Attachments:  []string{},
		Timestamp:    0,
		DurationMs:   durationMs,
		InputTokens:  lastInputTokens,
		OutputTokens: lastOutputTokens,
		ToolUses:     toolUses,
	}
	tab.Messages = append(tab.Messages, responseMsg)

	// Update the tab in the map (important: tab is a pointer, so changes are reflected)
	a.tabs[tabID] = tab

	fmt.Printf("[App.SendMessage] SUCCESS - message and response added. Tab now has %d messages\n", len(tab.Messages))

	// Debug: Print all messages in tab
	for i, msg := range tab.Messages {
		fmt.Printf("  Message %d: %s - %s\n", i, msg.Role, msg.Content[:min(50, len(msg.Content))])
	}

	// Emit event to frontend to refresh
	fmt.Println("[App.SendMessage] Emitting tabs-updated event to frontend")
	runtime.EventsEmit(a.ctx, "tabs-updated")

	// Trigger background agent: tab rename suggestion after first conversation
	if len(tab.Messages) == 2 && strings.HasPrefix(tab.Name, "대화 ") && a.agentService != nil {
		a.agentService.Enqueue(claude.AgentTask{
			Type:             claude.AgentTaskTabRename,
			TabID:            tabID,
			WorkDir:          tab.WorkDir,
			FirstUserMessage: message,
		})
	}

	go a.saveState()

	return nil
}

// AnswerQuestion sends the user's answers to pending AskUserQuestion back to Claude via --resume.
// The answers map is { questionText: selectedOptionLabel }.
func (a *App) AnswerQuestion(tabID string, answers map[string]string) error {
	fmt.Printf("[App.AnswerQuestion] Called with tabID=%s, %d answers\n", tabID, len(answers))

	// Format answers as readable text for Claude to process via --resume
	var parts []string
	for question, answer := range answers {
		parts = append(parts, fmt.Sprintf("Q: %s\nA: %s", question, answer))
	}
	answerText := strings.Join(parts, "\n\n")

	// Resume the session with the answer text
	return a.SendMessage(tabID, answerText, nil)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// AddNewTab creates a new conversation tab
// Maximum 6 tabs allowed
func (a *App) AddNewTab() (models.TabState, error) {
	// Check if we already have 6 tabs
	if len(a.tabs) >= 6 {
		return models.TabState{}, fmt.Errorf("maximum 6 tabs allowed")
	}

	// Find the next available conversation number
	maxNum := 0
	for id := range a.tabs {
		var num int
		if _, err := fmt.Sscanf(id, "conversation-%d", &num); err == nil {
			if num > maxNum {
				maxNum = num
			}
		}
	}

	newNum := maxNum + 1
	tabID := fmt.Sprintf("conversation-%d", newNum)

	homeDir, _ := os.UserHomeDir()
	newTab := &models.TabState{
		ID:       tabID,
		Name:     fmt.Sprintf("대화 %d", newNum),
		Messages: []models.Message{},
		IsActive: false,
		WorkDir:  homeDir,
	}

	a.tabs[tabID] = newTab
	a.settings.TabSettings[tabID] = false

	// Create new chat in Claude if service is initialized
	if a.claude.IsInitialized() {
		if err := a.claude.CreateNewChat(tabID); err != nil {
			fmt.Printf("Warning: failed to create new chat in Claude: %v\n", err)
		}
	}

	go a.saveState()

	return *newTab, nil
}

// RemoveTab removes a tab by ID
func (a *App) RemoveTab(tabID string) error {
	if _, exists := a.tabs[tabID]; !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	// Close Claude page if service is initialized
	if a.claude.IsInitialized() {
		if err := a.claude.ClosePage(tabID); err != nil {
			fmt.Printf("Warning: failed to close Claude page: %v\n", err)
		}
	}

	delete(a.tabs, tabID)
	delete(a.settings.TabSettings, tabID)

	go a.saveState()

	return nil
}

// RenameTab renames a tab
func (a *App) RenameTab(tabID, newName string) error {
	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	tab.Name = newName
	go a.saveState()
	return nil
}

// ToggleTabAdminMode toggles admin mode for a specific tab
// Only one tab can have admin mode at a time
func (a *App) ToggleTabAdminMode(tabID string, enabled bool) error {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()

	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	if enabled {
		// Disable admin mode for all other tabs
		for _, t := range a.tabs {
			if t.ID != tabID {
				t.AdminMode = false
				t.Orchestrator = nil
			}
		}

		// Initialize orchestrator state
		connectedTabs := []string{}
		for _, t := range a.tabs {
			if t.ID != tabID {
				connectedTabs = append(connectedTabs, t.ID)
			}
		}
		tab.Orchestrator = &models.OrchestratorState{
			ConnectedTabs: connectedTabs,
			JobHistory:    []models.OrchestrationJob{},
		}
		fmt.Printf("[App.ToggleTabAdminMode] Admin mode enabled for %s, connected workers: %v\n", tabID, connectedTabs)
	} else {
		tab.Orchestrator = nil
		fmt.Printf("[App.ToggleTabAdminMode] Admin mode disabled for %s\n", tabID)
	}

	tab.AdminMode = enabled
	return nil
}

// ToggleTabPlanMode toggles plan mode for a specific tab
func (a *App) ToggleTabPlanMode(tabID string, enabled bool) error {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()

	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	tab.PlanMode = enabled
	fmt.Printf("[App.ToggleTabPlanMode] Tab %s plan mode: %v\n", tabID, enabled)
	go a.saveState()
	return nil
}

// ConnectWorkerTab connects a worker tab to an admin tab's orchestrator
func (a *App) ConnectWorkerTab(adminTabID, workerTabID string) error {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()

	adminTab, exists := a.tabs[adminTabID]
	if !exists {
		return fmt.Errorf("admin tab not found: %s", adminTabID)
	}
	if !adminTab.AdminMode || adminTab.Orchestrator == nil {
		return fmt.Errorf("tab %s is not in admin mode", adminTabID)
	}
	if _, exists := a.tabs[workerTabID]; !exists {
		return fmt.Errorf("worker tab not found: %s", workerTabID)
	}

	// Check for duplicates
	for _, id := range adminTab.Orchestrator.ConnectedTabs {
		if id == workerTabID {
			return nil // Already connected
		}
	}

	adminTab.Orchestrator.ConnectedTabs = append(adminTab.Orchestrator.ConnectedTabs, workerTabID)
	fmt.Printf("[App.ConnectWorkerTab] Connected %s to admin %s\n", workerTabID, adminTabID)
	return nil
}

// DisconnectWorkerTab disconnects a worker tab from an admin tab's orchestrator
func (a *App) DisconnectWorkerTab(adminTabID, workerTabID string) error {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()

	adminTab, exists := a.tabs[adminTabID]
	if !exists {
		return fmt.Errorf("admin tab not found: %s", adminTabID)
	}
	if !adminTab.AdminMode || adminTab.Orchestrator == nil {
		return fmt.Errorf("tab %s is not in admin mode", adminTabID)
	}

	for i, id := range adminTab.Orchestrator.ConnectedTabs {
		if id == workerTabID {
			adminTab.Orchestrator.ConnectedTabs = append(
				adminTab.Orchestrator.ConnectedTabs[:i],
				adminTab.Orchestrator.ConnectedTabs[i+1:]...,
			)
			fmt.Printf("[App.DisconnectWorkerTab] Disconnected %s from admin %s\n", workerTabID, adminTabID)
			return nil
		}
	}

	return nil // Not connected, no-op
}

// DecompositionResult represents the JSON structure returned by Claude for task decomposition
type DecompositionResult struct {
	Analysis string `json:"analysis"`
	Tasks    []struct {
		WorkerTabID string `json:"workerTabId"`
		Prompt      string `json:"prompt"`
		Description string `json:"description"`
	} `json:"tasks"`
}

// SendAdminMessage handles orchestration: decompose → dispatch → collect → synthesize
func (a *App) SendAdminMessage(adminTabID, message string, files []string) error {
	fmt.Printf("[App.SendAdminMessage] Called with adminTabID=%s, message=%s\n", adminTabID, message)

	a.tabsMu.RLock()
	adminTab, exists := a.tabs[adminTabID]
	if !exists {
		a.tabsMu.RUnlock()
		return fmt.Errorf("admin tab not found: %s", adminTabID)
	}
	if !adminTab.AdminMode || adminTab.Orchestrator == nil {
		a.tabsMu.RUnlock()
		return fmt.Errorf("tab %s is not in admin mode with orchestrator", adminTabID)
	}

	connectedTabs := make([]string, len(adminTab.Orchestrator.ConnectedTabs))
	copy(connectedTabs, adminTab.Orchestrator.ConnectedTabs)
	a.tabsMu.RUnlock()

	if len(connectedTabs) == 0 {
		return fmt.Errorf("연결된 워커 탭이 없습니다. 다른 탭을 먼저 생성하세요.")
	}

	// Add user message to admin tab
	userMsg := models.Message{
		Role:        "user",
		Content:     message,
		Attachments: files,
		Metadata:    map[string]string{"type": "orchestration"},
	}
	a.tabsMu.Lock()
	adminTab.Messages = append(adminTab.Messages, userMsg)
	a.tabsMu.Unlock()

	runtime.EventsEmit(a.ctx, "user-message-added", adminTabID)

	// Create cancellable context for the entire orchestration
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelMu.Lock()
	a.orchestrationCancel[adminTabID] = cancel
	a.cancelMu.Unlock()
	defer func() {
		cancel()
		a.cancelMu.Lock()
		delete(a.orchestrationCancel, adminTabID)
		a.cancelMu.Unlock()
	}()

	// Generate job ID
	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())

	// Build tab info for decomposition prompt
	var tabInfo []string
	a.tabsMu.RLock()
	for _, wID := range connectedTabs {
		if wTab, ok := a.tabs[wID]; ok {
			tabInfo = append(tabInfo, fmt.Sprintf("- %s (ID: %s, WorkDir: %s)", wTab.Name, wTab.ID, wTab.WorkDir))
		}
	}
	a.tabsMu.RUnlock()

	// ============ Phase 1a: Codebase Analysis ============
	fmt.Printf("[App.SendAdminMessage] Phase 1a: Analyzing codebase\n")

	a.wsServer.SendStreamStart(adminTabID)
	runtime.EventsEmit(a.ctx, "streaming-start", adminTabID)

	analysisPrompt := fmt.Sprintf(`사용자 요청을 실행하기 위해 코드베이스를 분석해주세요.

사용자 요청: %s

다음을 수행하세요:
1. 관련 파일을 찾아 읽으세요 (Glob, Grep, Read 도구 사용)
2. 기존 코드 패턴과 컨벤션을 파악하세요
3. 수정이 필요한 파일과 위치를 정리하세요
4. 여러 파일이 동시에 수정될 때의 충돌 가능성을 확인하세요

분석 결과를 정리해주세요.`, message)

	analysisStartTime := time.Now()

	analysisResponse, _, _, err := a.claude.SendMessage(ctx, adminTabID, analysisPrompt, files, adminTab.WorkDir, 20, "You are analyzing a codebase. Explore thoroughly using available tools.", "bypassPermissions", "", "", func(chunk string) {
		a.wsServer.SendStreamChunk(adminTabID, chunk)
	}, func(toolName, detail string) {
		a.wsServer.SendToolActivity(adminTabID, toolName, detail)
	}, func(input, output int) {
		a.wsServer.SendTokenUsage(adminTabID, input, output)
	})
	if err != nil {
		if ctx.Err() == context.Canceled {
			a.wsServer.SendStreamEnd(adminTabID)
			return nil
		}
		a.wsServer.SendStreamError(adminTabID, err.Error())
		return fmt.Errorf("codebase analysis failed: %w", err)
	}

	a.wsServer.SendStreamEnd(adminTabID)
	runtime.EventsEmit(a.ctx, "streaming-end", adminTabID)

	// Add analysis result as assistant message
	analysisDuration := time.Since(analysisStartTime).Milliseconds()
	analysisMsg := models.Message{
		Role:       "assistant",
		Content:    analysisResponse,
		DurationMs: analysisDuration,
		Metadata:   map[string]string{"type": "orchestration", "jobId": jobID, "phase": "analysis"},
	}
	a.tabsMu.Lock()
	adminTab.Messages = append(adminTab.Messages, analysisMsg)
	a.tabsMu.Unlock()
	runtime.EventsEmit(a.ctx, "tabs-updated")

	// ============ Phase 1b: Task Decomposition ============
	fmt.Printf("[App.SendAdminMessage] Phase 1b: Decomposing task based on analysis\n")

	a.wsServer.SendStreamStart(adminTabID)
	runtime.EventsEmit(a.ctx, "streaming-start", adminTabID)

	decompositionPrompt := fmt.Sprintf(`위 분석을 기반으로 작업을 분해해주세요.

Available worker tabs:
%s

You MUST respond with ONLY a JSON object (no markdown, no code fences, no explanation before/after):
{
  "analysis": "분석 요약",
  "tasks": [
    {
      "workerTabId": "conversation-N",
      "prompt": "The exact prompt to send to this worker",
      "description": "Brief description of what this worker will do"
    }
  ]
}

Rules:
- Assign tasks only to the available worker tab IDs listed above
- Each worker should get a meaningful, self-contained task
- If the request is simple and only needs one worker, assign to just one
- 각 task의 "prompt"에 반드시 포함할 것:
  * 수정할 파일의 정확한 경로
  * 참고할 기존 코드 스니펫이나 패턴
  * 구체적인 구현 지침
  * 다른 워커 작업과의 의존성 주의사항
- Respond with ONLY valid JSON, no other text`, strings.Join(tabInfo, "\n"))

	var decompositionResponse string
	sendStartTime := time.Now()

	decompositionResponse, _, _, err = a.claude.SendMessage(ctx, adminTabID, decompositionPrompt, nil, adminTab.WorkDir, 1, "You are a task orchestrator. Respond ONLY with valid JSON.", "bypassPermissions", "", "", func(chunk string) {
		a.wsServer.SendStreamChunk(adminTabID, chunk)
	}, nil, nil)
	if err != nil {
		if ctx.Err() == context.Canceled {
			a.wsServer.SendStreamEnd(adminTabID)
			return nil
		}
		a.wsServer.SendStreamError(adminTabID, err.Error())
		return fmt.Errorf("decomposition failed: %w", err)
	}

	a.wsServer.SendStreamEnd(adminTabID)
	runtime.EventsEmit(a.ctx, "streaming-end", adminTabID)

	// Parse decomposition result
	var decomp DecompositionResult

	// Try to extract JSON from the response (handle markdown code fences)
	jsonStr := decompositionResponse
	if idx := strings.Index(jsonStr, "```json"); idx != -1 {
		jsonStr = jsonStr[idx+7:]
		if endIdx := strings.Index(jsonStr, "```"); endIdx != -1 {
			jsonStr = jsonStr[:endIdx]
		}
	} else if idx := strings.Index(jsonStr, "```"); idx != -1 {
		jsonStr = jsonStr[idx+3:]
		if endIdx := strings.Index(jsonStr, "```"); endIdx != -1 {
			jsonStr = jsonStr[:endIdx]
		}
	}
	jsonStr = strings.TrimSpace(jsonStr)

	if err := json.Unmarshal([]byte(jsonStr), &decomp); err != nil {
		fmt.Printf("[App.SendAdminMessage] Failed to parse decomposition JSON: %v\nRaw: %s\n", err, decompositionResponse)
		// Add error message and return
		decompDuration := time.Since(sendStartTime).Milliseconds()
		errorMsg := models.Message{
			Role:       "assistant",
			Content:    fmt.Sprintf("작업 분해에 실패했습니다. Claude의 응답을 JSON으로 파싱할 수 없습니다.\n\n원본 응답:\n%s", decompositionResponse),
			DurationMs: decompDuration,
			Metadata:   map[string]string{"type": "orchestration", "jobId": jobID, "phase": "decomposition-error"},
		}
		a.tabsMu.Lock()
		adminTab.Messages = append(adminTab.Messages, errorMsg)
		a.tabsMu.Unlock()
		runtime.EventsEmit(a.ctx, "tabs-updated")
		return nil
	}

	// Add decomposition result as assistant message
	decompDuration := time.Since(sendStartTime).Milliseconds()
	var taskSummary strings.Builder
	taskSummary.WriteString(fmt.Sprintf("**작업 분석**: %s\n\n**배정된 작업 (%d개)**:\n", decomp.Analysis, len(decomp.Tasks)))
	for i, t := range decomp.Tasks {
		taskSummary.WriteString(fmt.Sprintf("%d. **%s** → %s\n", i+1, t.WorkerTabID, t.Description))
	}
	decompMsg := models.Message{
		Role:       "assistant",
		Content:    taskSummary.String(),
		DurationMs: decompDuration,
		Metadata:   map[string]string{"type": "orchestration", "jobId": jobID, "phase": "decomposition"},
	}
	a.tabsMu.Lock()
	adminTab.Messages = append(adminTab.Messages, decompMsg)
	a.tabsMu.Unlock()
	runtime.EventsEmit(a.ctx, "tabs-updated")

	// Create orchestration job
	job := models.OrchestrationJob{
		ID:          jobID,
		AdminTabID:  adminTabID,
		UserRequest: message,
		Status:      models.TaskRunning,
	}

	for i, t := range decomp.Tasks {
		task := models.WorkerTask{
			ID:          fmt.Sprintf("%s-task-%d", jobID, i),
			WorkerTabID: t.WorkerTabID,
			AdminTabID:  adminTabID,
			Description: t.Description,
			Prompt:      t.Prompt,
			Status:      models.TaskPending,
		}
		job.Tasks = append(job.Tasks, task)
	}

	a.tabsMu.Lock()
	adminTab.Orchestrator.CurrentJob = &job
	a.tabsMu.Unlock()

	// ============ Phase 2: Dispatch ============
	fmt.Printf("[App.SendAdminMessage] Phase 2: Dispatching %d tasks\n", len(job.Tasks))

	var wg sync.WaitGroup
	var resultMu sync.Mutex

	for i := range job.Tasks {
		task := &job.Tasks[i]

		// Verify worker tab exists and is connected
		a.tabsMu.RLock()
		_, workerExists := a.tabs[task.WorkerTabID]
		a.tabsMu.RUnlock()
		if !workerExists {
			task.Status = models.TaskFailed
			task.Error = "worker tab not found"
			continue
		}

		wg.Add(1)
		go func(t *models.WorkerTask) {
			defer wg.Done()

			// Check for cancellation
			if ctx.Err() != nil {
				resultMu.Lock()
				t.Status = models.TaskCancelled
				resultMu.Unlock()
				return
			}

			resultMu.Lock()
			t.Status = models.TaskRunning
			t.StartedAt = time.Now()
			resultMu.Unlock()

			// Send orchestrator event: task-started
			a.wsServer.SendOrchestratorEvent(websocket.OrchestratorMessage{
				Type:        "task-started",
				AdminTabID:  adminTabID,
				TaskID:      t.ID,
				WorkerTabID: t.WorkerTabID,
				Content:     t.Description,
			})

			fmt.Printf("[App.SendAdminMessage] Dispatching task %s to worker %s\n", t.ID, t.WorkerTabID)

			// Use existing SendMessage to send to worker tab (this handles streaming)
			err := a.SendMessage(t.WorkerTabID, t.Prompt, nil)

			resultMu.Lock()
			t.CompletedAt = time.Now()
			t.DurationMs = t.CompletedAt.Sub(t.StartedAt).Milliseconds()

			if err != nil {
				t.Status = models.TaskFailed
				t.Error = err.Error()
				a.wsServer.SendOrchestratorEvent(websocket.OrchestratorMessage{
					Type:        "task-failed",
					AdminTabID:  adminTabID,
					TaskID:      t.ID,
					WorkerTabID: t.WorkerTabID,
					Content:     err.Error(),
					Status:      string(models.TaskFailed),
				})
			} else {
				t.Status = models.TaskCompleted
				// Collect the last assistant message from worker tab
				a.tabsMu.RLock()
				if wTab, ok := a.tabs[t.WorkerTabID]; ok && len(wTab.Messages) > 0 {
					for j := len(wTab.Messages) - 1; j >= 0; j-- {
						if wTab.Messages[j].Role == "assistant" {
							t.Result = wTab.Messages[j].Content
							break
						}
					}
				}
				a.tabsMu.RUnlock()

				a.wsServer.SendOrchestratorEvent(websocket.OrchestratorMessage{
					Type:        "task-completed",
					AdminTabID:  adminTabID,
					TaskID:      t.ID,
					WorkerTabID: t.WorkerTabID,
					Status:      string(models.TaskCompleted),
				})
			}
			resultMu.Unlock()

			fmt.Printf("[App.SendAdminMessage] Task %s finished: %s\n", t.ID, t.Status)
		}(task)
	}

	// ============ Phase 3: Collection ============
	fmt.Printf("[App.SendAdminMessage] Phase 3: Waiting for all tasks to complete\n")
	wg.Wait()

	// Check if cancelled
	if ctx.Err() != nil {
		job.Status = models.TaskCancelled
		a.tabsMu.Lock()
		adminTab.Orchestrator.CurrentJob = nil
		adminTab.Orchestrator.JobHistory = append(adminTab.Orchestrator.JobHistory, job)
		a.tabsMu.Unlock()
		return nil
	}

	// Collect results
	var resultSummary strings.Builder
	successCount := 0
	failCount := 0

	for _, t := range job.Tasks {
		if t.Status == models.TaskCompleted {
			successCount++
			resultSummary.WriteString(fmt.Sprintf("=== Worker: %s ===\nTask: %s\nResult:\n%s\n\n", t.WorkerTabID, t.Description, t.Result))
		} else if t.Status == models.TaskFailed {
			failCount++
			resultSummary.WriteString(fmt.Sprintf("=== Worker: %s (FAILED) ===\nTask: %s\nError: %s\n\n", t.WorkerTabID, t.Description, t.Error))
		}
	}

	fmt.Printf("[App.SendAdminMessage] Phase 3 complete: %d success, %d failed\n", successCount, failCount)

	// ============ Phase 4: Synthesis ============
	fmt.Printf("[App.SendAdminMessage] Phase 4: Synthesizing results\n")

	a.wsServer.SendStreamStart(adminTabID)
	runtime.EventsEmit(a.ctx, "streaming-start", adminTabID)

	synthesisPrompt := fmt.Sprintf(`You are an orchestrator summarizing results from multiple worker tabs.

Original user request: %s

Worker results:
%s

Please synthesize all the results into a comprehensive, well-organized response for the user.
- Highlight key findings from each worker
- Note any failures or issues
- Provide an overall summary and any recommendations
- Use Korean for the response`, message, resultSummary.String())

	synthesisStartTime := time.Now()
	var synthStreamContent string

	synthesisResponse, _, _, err := a.claude.SendMessage(ctx, adminTabID, synthesisPrompt, nil, adminTab.WorkDir, 1, "You are a helpful assistant summarizing orchestrated task results.", "bypassPermissions", "", "", func(chunk string) {
		synthStreamContent = chunk
		a.wsServer.SendStreamChunk(adminTabID, chunk)
	}, nil, nil)
	if err != nil {
		if ctx.Err() == context.Canceled {
			a.wsServer.SendStreamEnd(adminTabID)
			return nil
		}
		synthesisResponse = fmt.Sprintf("종합 보고 생성에 실패했습니다: %v\n\n수집된 결과:\n%s", err, resultSummary.String())
	}

	// Send final chunk if available
	if synthStreamContent != "" {
		a.wsServer.SendStreamChunk(adminTabID, synthStreamContent)
	}
	time.Sleep(50 * time.Millisecond)

	a.wsServer.SendStreamEnd(adminTabID)
	runtime.EventsEmit(a.ctx, "streaming-end", adminTabID)

	// Add synthesis as assistant message
	synthDuration := time.Since(synthesisStartTime).Milliseconds()
	totalDuration := time.Since(sendStartTime).Milliseconds()
	synthMsg := models.Message{
		Role:       "assistant",
		Content:    synthesisResponse,
		DurationMs: totalDuration,
		Metadata: map[string]string{
			"type":    "orchestration",
			"jobId":   jobID,
			"phase":   "synthesis",
			"success": fmt.Sprintf("%d", successCount),
			"failed":  fmt.Sprintf("%d", failCount),
			"synthMs": fmt.Sprintf("%d", synthDuration),
		},
	}
	a.tabsMu.Lock()
	adminTab.Messages = append(adminTab.Messages, synthMsg)
	job.Status = models.TaskCompleted
	job.SynthesisResult = synthesisResponse
	adminTab.Orchestrator.CurrentJob = nil
	adminTab.Orchestrator.JobHistory = append(adminTab.Orchestrator.JobHistory, job)
	a.tabsMu.Unlock()

	// Send job-completed event
	a.wsServer.SendOrchestratorEvent(websocket.OrchestratorMessage{
		Type:       "job-completed",
		AdminTabID: adminTabID,
		Status:     string(models.TaskCompleted),
	})

	runtime.EventsEmit(a.ctx, "tabs-updated")
	fmt.Printf("[App.SendAdminMessage] Orchestration complete. Total time: %dms\n", totalDuration)

	go a.saveState()

	return nil
}

// CancelOrchestrationJob cancels an in-progress orchestration job
func (a *App) CancelOrchestrationJob(adminTabID string) error {
	a.cancelMu.Lock()
	cancelFunc, exists := a.orchestrationCancel[adminTabID]
	a.cancelMu.Unlock()

	if !exists {
		return fmt.Errorf("no active orchestration for admin tab: %s", adminTabID)
	}

	// Cancel the orchestration context
	cancelFunc()

	// Also cancel all worker tabs that are currently running
	a.tabsMu.RLock()
	adminTab, tabExists := a.tabs[adminTabID]
	a.tabsMu.RUnlock()

	if tabExists && adminTab.Orchestrator != nil && adminTab.Orchestrator.CurrentJob != nil {
		for _, task := range adminTab.Orchestrator.CurrentJob.Tasks {
			if task.Status == models.TaskRunning {
				a.CancelMessage(task.WorkerTabID)
			}
		}
	}

	fmt.Printf("[App.CancelOrchestrationJob] Cancelled orchestration for admin tab: %s\n", adminTabID)
	return nil
}

// ToggleTabTeamsMode toggles Teams (Beta) mode for a specific tab.
// Teams mode and Admin mode are mutually exclusive.
func (a *App) ToggleTabTeamsMode(tabID string, enabled bool) error {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()

	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	if enabled {
		// Disable teams mode on all other tabs (only one team tab allowed)
		for _, t := range a.tabs {
			if t.ID != tabID {
				t.TeamsMode = false
				t.TeamsState = nil
			}
		}

		// Disable admin mode if active on this tab (mutually exclusive)
		tab.AdminMode = false
		tab.Orchestrator = nil

		// Initialize teams state with all other tabs as workers
		connectedTabs := []string{}
		for _, t := range a.tabs {
			if t.ID != tabID {
				connectedTabs = append(connectedTabs, t.ID)
			}
		}
		tab.TeamsState = &models.TeamsState{
			ConnectedTabs: connectedTabs,
			AgentMapping:  make(map[string]string),
			IsRunning:     false,
		}
		fmt.Printf("[App.ToggleTabTeamsMode] Teams mode enabled for %s, connected workers: %v\n", tabID, connectedTabs)
	} else {
		tab.TeamsState = nil
		fmt.Printf("[App.ToggleTabTeamsMode] Teams mode disabled for %s\n", tabID)
	}

	tab.TeamsMode = enabled
	return nil
}

// ConnectTeamsWorker adds a worker tab to a teams tab
func (a *App) ConnectTeamsWorker(teamTabID, workerTabID string) error {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()

	teamTab, exists := a.tabs[teamTabID]
	if !exists {
		return fmt.Errorf("team tab not found: %s", teamTabID)
	}
	if !teamTab.TeamsMode || teamTab.TeamsState == nil {
		return fmt.Errorf("tab %s is not in teams mode", teamTabID)
	}
	if _, exists := a.tabs[workerTabID]; !exists {
		return fmt.Errorf("worker tab not found: %s", workerTabID)
	}

	for _, id := range teamTab.TeamsState.ConnectedTabs {
		if id == workerTabID {
			return nil // Already connected
		}
	}

	teamTab.TeamsState.ConnectedTabs = append(teamTab.TeamsState.ConnectedTabs, workerTabID)
	fmt.Printf("[App.ConnectTeamsWorker] Connected %s to team %s\n", workerTabID, teamTabID)
	return nil
}

// DisconnectTeamsWorker removes a worker tab from a teams tab
func (a *App) DisconnectTeamsWorker(teamTabID, workerTabID string) error {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()

	teamTab, exists := a.tabs[teamTabID]
	if !exists {
		return fmt.Errorf("team tab not found: %s", teamTabID)
	}
	if !teamTab.TeamsMode || teamTab.TeamsState == nil {
		return fmt.Errorf("tab %s is not in teams mode", teamTabID)
	}

	for i, id := range teamTab.TeamsState.ConnectedTabs {
		if id == workerTabID {
			teamTab.TeamsState.ConnectedTabs = append(
				teamTab.TeamsState.ConnectedTabs[:i],
				teamTab.TeamsState.ConnectedTabs[i+1:]...,
			)
			fmt.Printf("[App.DisconnectTeamsWorker] Disconnected %s from team %s\n", workerTabID, teamTabID)
			return nil
		}
	}

	return nil
}

// SendTeamsMessage handles Teams (Beta) orchestration: single CLI call with --agents flag.
// Claude autonomously delegates work to sub-agents. Task tool activities are routed to worker tabs.
func (a *App) SendTeamsMessage(teamTabID, message string, files []string) error {
	fmt.Printf("[App.SendTeamsMessage] Called with teamTabID=%s, message=%s\n", teamTabID, message)

	a.tabsMu.RLock()
	teamTab, exists := a.tabs[teamTabID]
	if !exists {
		a.tabsMu.RUnlock()
		return fmt.Errorf("team tab not found: %s", teamTabID)
	}
	if !teamTab.TeamsMode || teamTab.TeamsState == nil {
		a.tabsMu.RUnlock()
		return fmt.Errorf("tab %s is not in teams mode with teams state", teamTabID)
	}

	connectedTabs := make([]string, len(teamTab.TeamsState.ConnectedTabs))
	copy(connectedTabs, teamTab.TeamsState.ConnectedTabs)
	a.tabsMu.RUnlock()

	if len(connectedTabs) == 0 {
		return fmt.Errorf("연결된 워커 탭이 없습니다. 다른 탭을 먼저 생성하세요.")
	}

	// Add user message to team tab
	userMsg := models.Message{
		Role:        "user",
		Content:     message,
		Attachments: files,
		Metadata:    map[string]string{"type": "teams"},
	}
	a.tabsMu.Lock()
	teamTab.Messages = append(teamTab.Messages, userMsg)
	teamTab.TeamsState.IsRunning = true
	a.tabsMu.Unlock()

	runtime.EventsEmit(a.ctx, "user-message-added", teamTabID)

	// Build agents JSON from connected worker tabs
	var workerInfos []claude.WorkerTabInfo
	agentMapping := make(map[string]string) // agentName → workerTabID

	a.tabsMu.RLock()
	for _, wID := range connectedTabs {
		if wTab, ok := a.tabs[wID]; ok {
			info := claude.WorkerTabInfo{
				ID:      wTab.ID,
				Name:    wTab.Name,
				WorkDir: wTab.WorkDir,
			}
			workerInfos = append(workerInfos, info)
			agentName := sanitizeAgentNameForMapping(wTab.Name)
			agentMapping[agentName] = wTab.ID
		}
	}
	a.tabsMu.RUnlock()

	agentsJSON, err := claude.BuildAgentsJSON(workerInfos)
	if err != nil {
		return fmt.Errorf("에이전트 정의 생성 실패: %w", err)
	}

	// Save agent mapping for activity routing
	a.tabsMu.Lock()
	teamTab.TeamsState.AgentMapping = agentMapping
	a.tabsMu.Unlock()

	fmt.Printf("[App.SendTeamsMessage] Built agents JSON: %s\n", agentsJSON)
	fmt.Printf("[App.SendTeamsMessage] Agent mapping: %v\n", agentMapping)

	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	a.cancelMu.Lock()
	a.orchestrationCancel[teamTabID] = cancel
	a.cancelMu.Unlock()
	defer func() {
		cancel()
		a.cancelMu.Lock()
		delete(a.orchestrationCancel, teamTabID)
		a.cancelMu.Unlock()
		a.tabsMu.Lock()
		if teamTab.TeamsState != nil {
			teamTab.TeamsState.IsRunning = false
		}
		a.tabsMu.Unlock()
	}()

	// Build system prompt for teams orchestration
	var tabInfo []string
	a.tabsMu.RLock()
	for _, wID := range connectedTabs {
		if wTab, ok := a.tabs[wID]; ok {
			tabInfo = append(tabInfo, fmt.Sprintf("- %s (WorkDir: %s)", wTab.Name, wTab.WorkDir))
		}
	}
	a.tabsMu.RUnlock()

	systemPrompt := fmt.Sprintf(
		"You are a team orchestrator. You have access to the following worker agents:\n%s\n\n"+
			"Use the Task tool to delegate work to these agents. "+
			"Each agent has access to Read, Write, Edit, Bash, Glob, Grep tools. "+
			"Delegate tasks in parallel when possible. "+
			"After all tasks are complete, synthesize the results into a comprehensive response.",
		strings.Join(tabInfo, "\n"))

	// Send streaming start
	a.wsServer.SendStreamStart(teamTabID)
	runtime.EventsEmit(a.ctx, "streaming-start", teamTabID)

	sendStartTime := time.Now()
	var streamingContent string

	// Single CLI call with --agents — Claude autonomously delegates
	response, _, _, err := a.claude.SendMessage(ctx, teamTabID, message, files, teamTab.WorkDir, 0, systemPrompt, "bypassPermissions", "", agentsJSON,
		func(chunk string) {
			streamingContent = chunk
			a.wsServer.SendStreamChunk(teamTabID, chunk)
		},
		func(toolName, detail string) {
			// Always send tool activity to team tab
			a.wsServer.SendToolActivity(teamTabID, toolName, detail)

			// If it's a Task tool use, route to the matching worker tab
			if toolName == "Task" {
				workerTabID := matchAgentToTab(detail, agentMapping)
				if workerTabID != "" {
					a.wsServer.SendOrchestratorEvent(websocket.OrchestratorMessage{
						Type:        "task-started",
						AdminTabID:  teamTabID,
						TaskID:      fmt.Sprintf("teams-%d", time.Now().UnixNano()),
						WorkerTabID: workerTabID,
						Content:     detail,
					})
				}
			}
		},
		func(input, output int) {
			a.wsServer.SendTokenUsage(teamTabID, input, output)
		},
	)

	if err != nil {
		if ctx.Err() == context.Canceled {
			a.wsServer.SendStreamEnd(teamTabID)
			runtime.EventsEmit(a.ctx, "streaming-end", teamTabID)
			return nil
		}
		a.wsServer.SendStreamError(teamTabID, err.Error())
		runtime.EventsEmit(a.ctx, "streaming-end", teamTabID)
		return fmt.Errorf("teams message failed: %w", err)
	}

	// Send final chunk
	if streamingContent != "" {
		a.wsServer.SendStreamChunk(teamTabID, streamingContent)
	}
	time.Sleep(50 * time.Millisecond)

	a.wsServer.SendStreamEnd(teamTabID)
	runtime.EventsEmit(a.ctx, "streaming-end", teamTabID)

	// Add response as assistant message
	durationMs := time.Since(sendStartTime).Milliseconds()
	responseMsg := models.Message{
		Role:       "assistant",
		Content:    response,
		DurationMs: durationMs,
		Metadata:   map[string]string{"type": "teams"},
	}
	a.tabsMu.Lock()
	teamTab.Messages = append(teamTab.Messages, responseMsg)
	a.tabsMu.Unlock()

	// Send job-completed event
	a.wsServer.SendOrchestratorEvent(websocket.OrchestratorMessage{
		Type:       "job-completed",
		AdminTabID: teamTabID,
		Status:     string(models.TaskCompleted),
	})

	runtime.EventsEmit(a.ctx, "tabs-updated")
	fmt.Printf("[App.SendTeamsMessage] Teams orchestration complete. Duration: %dms\n", durationMs)

	go a.saveState()

	return nil
}

// matchAgentToTab tries to find the worker tab ID for a given Task tool detail string.
// It checks if any agent name appears in the detail string.
func matchAgentToTab(detail string, agentMapping map[string]string) string {
	detailLower := strings.ToLower(detail)
	for agentName, tabID := range agentMapping {
		if strings.Contains(detailLower, strings.ToLower(agentName)) {
			return tabID
		}
	}
	return ""
}

// sanitizeAgentNameForMapping mirrors the sanitization in claude.sanitizeAgentName
func sanitizeAgentNameForMapping(name string) string {
	name = strings.ReplaceAll(name, " ", "-")
	re := regexp.MustCompile(`[^a-zA-Z0-9가-힣_-]+`)
	name = re.ReplaceAllString(name, "")
	if name == "" {
		name = "worker"
	}
	return name
}

// ClearConversation clears all messages in a conversation tab
func (a *App) ClearConversation(tabID string) error {
	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	tab.Messages = []models.Message{}
	a.tabs[tabID] = tab

	// Reset CLI session so next message starts fresh
	if a.claude.IsInitialized() {
		a.claude.CreateNewChat(tabID)
	}

	runtime.EventsEmit(a.ctx, "tabs-updated")
	go a.saveState()
	return nil
}

// GetUsageInfo returns token usage estimation for a conversation tab
func (a *App) GetUsageInfo(tabID string) (models.UsageInfo, error) {
	tab, exists := a.tabs[tabID]
	if !exists {
		return models.UsageInfo{}, fmt.Errorf("tab not found: %s", tabID)
	}

	var inputChars, outputChars int
	for _, msg := range tab.Messages {
		if msg.Role == "user" {
			inputChars += len(msg.Content)
		} else if msg.Role == "assistant" {
			outputChars += len(msg.Content)
		}
	}

	// Rough estimation: ~4 chars per token for English, ~2 chars per token for Korean
	// Use ~3 as a middle ground
	inputTokens := inputChars / 3
	outputTokens := outputChars / 3

	return models.UsageInfo{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
		MessageCount: len(tab.Messages),
	}, nil
}

// GetCurrentModel returns the current model name
func (a *App) GetCurrentModel() string {
	return a.model
}

// SetModel sets the model to use after validating against allowed models
func (a *App) SetModel(modelName string) error {
	for _, m := range allowedModels {
		if m == modelName {
			a.model = modelName
			a.claude.SetModel(modelName)
			fmt.Printf("[App.SetModel] Model changed to: %s\n", modelName)
			go a.saveState()
			return nil
		}
	}
	return fmt.Errorf("허용되지 않는 모델입니다: %s", modelName)
}

// GetAvailableModels returns the list of allowed model names
func (a *App) GetAvailableModels() []string {
	return allowedModels
}

// GetAvailableSessions returns session metadata from all projects.
// Current workDir sessions come first, followed by other projects.
// Agent sessions are excluded from the listing.
func (a *App) GetAvailableSessions(workDir string) ([]claude.SessionInfo, error) {
	excludeUUIDs := a.claude.GetAgentSessionUUIDs()
	return claude.ListAllSessionsExcluding(workDir, excludeUUIDs)
}

// SwitchTabSession switches a tab to use an existing or new session.
// If sessionID is empty, creates a new session. Otherwise, loads messages from the JSONL file.
// projectPath is the project directory the session belongs to (may differ from tab's workDir).
func (a *App) SwitchTabSession(tabID, sessionID, projectPath string) error {
	a.tabsMu.Lock()
	defer a.tabsMu.Unlock()

	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	if sessionID == "" {
		// New session: clear messages and reset CLI session
		tab.Messages = []models.Message{}
		a.claude.CreateNewChat(tabID)
		runtime.EventsEmit(a.ctx, "tabs-updated")
		go a.saveState()
		return nil
	}

	// Check if another tab already uses this session
	exported := a.claude.ExportSessions()
	for convID, state := range exported {
		if convID != tabID && state.SessionID == sessionID {
			return fmt.Errorf("이 세션은 다른 탭에서 사용 중입니다")
		}
	}

	// Build JSONL path using the session's own project path
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	resolvedPath := projectPath
	if resolvedPath == "" {
		resolvedPath = tab.WorkDir
	}
	encoded := claude.EncodeProjectPath(resolvedPath)
	jsonlPath := filepath.Join(homeDir, ".claude", "projects", encoded, sessionID+".jsonl")

	// Load messages from JSONL
	sessionMsgs, err := a.claude.LoadSessionMessages(jsonlPath)
	if err != nil {
		return fmt.Errorf("세션 메시지 로드 실패: %w", err)
	}

	// Convert SessionMessage → models.Message
	messages := make([]models.Message, 0, len(sessionMsgs))
	for _, sm := range sessionMsgs {
		var ts int64
		if t, err := time.Parse(time.RFC3339Nano, sm.Timestamp); err == nil {
			ts = t.UnixMilli()
		}
		messages = append(messages, models.Message{
			Role:      sm.Role,
			Content:   sm.Content,
			Timestamp: ts,
		})
	}

	// 세션의 프로젝트 경로로 작업 디렉토리 동기화
	if projectPath != "" {
		tab.WorkDir = projectPath
	}

	tab.Messages = messages
	a.claude.SetSession(tabID, sessionID)

	runtime.EventsEmit(a.ctx, "tabs-updated")
	go a.saveState()
	return nil
}

// DeleteSession deletes a session JSONL file. If the session is in use by a tab, clears that tab first.
func (a *App) DeleteSession(sessionID, projectPath string) error {
	// If this session is in use by any tab, clear it
	exported := a.claude.ExportSessions()
	for convID, state := range exported {
		if state.SessionID == sessionID {
			a.claude.CreateNewChat(convID)
			a.tabsMu.Lock()
			if tab, ok := a.tabs[convID]; ok {
				tab.Messages = []models.Message{}
			}
			a.tabsMu.Unlock()
		}
	}

	if err := a.claude.DeleteSession(projectPath, sessionID); err != nil {
		return err
	}

	runtime.EventsEmit(a.ctx, "tabs-updated")
	go a.saveState()
	return nil
}

// GetTabSessionID returns the current session UUID for a tab (empty if not started)
func (a *App) GetTabSessionID(tabID string) string {
	exported := a.claude.ExportSessions()
	if state, ok := exported[tabID]; ok && state.Started {
		return state.SessionID
	}
	return ""
}

// GetPlanContent returns the plan file content for the current session of a tab.
// Returns empty string (no error) if no plan file exists.
func (a *App) GetPlanContent(tabID string) (string, error) {
	sessionID := a.GetTabSessionID(tabID)
	if sessionID == "" {
		fmt.Printf("[GetPlanContent] No session ID for tab %s\n", tabID)
		return "", nil
	}

	a.tabsMu.RLock()
	tab, exists := a.tabs[tabID]
	a.tabsMu.RUnlock()
	if !exists {
		fmt.Printf("[GetPlanContent] Tab not found: %s\n", tabID)
		return "", nil
	}

	fmt.Printf("[GetPlanContent] tab=%s, session=%s, workDir=%s\n", tabID, sessionID[:8], tab.WorkDir)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}

	encoded := claude.EncodeProjectPath(tab.WorkDir)
	jsonlPath := filepath.Join(homeDir, ".claude", "projects", encoded, sessionID+".jsonl")

	slug, err := claude.ExtractSessionSlug(jsonlPath)
	if err != nil {
		fmt.Printf("[GetPlanContent] ExtractSessionSlug error: %v (path: %s)\n", err, jsonlPath)
		return "", nil
	}
	if slug == "" {
		fmt.Printf("[GetPlanContent] No slug found in %s\n", jsonlPath)
		return "", nil
	}

	planPath := filepath.Join(homeDir, ".claude", "plans", slug+".md")
	fmt.Printf("[GetPlanContent] Looking for plan at: %s\n", planPath)
	data, err := os.ReadFile(planPath)
	if err == nil {
		return string(data), nil
	}

	// Plan file not found — fall back to last assistant message content
	fmt.Printf("[GetPlanContent] Plan file not found, falling back to last assistant message\n")
	a.tabsMu.RLock()
	messages := tab.Messages
	a.tabsMu.RUnlock()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" && messages[i].Content != "" {
			return messages[i].Content, nil
		}
	}

	return "", nil
}

// CancelMessage cancels an in-progress message generation
func (a *App) CancelMessage(tabID string) error {
	a.cancelMu.Lock()
	cancelFunc, exists := a.cancelFuncs[tabID]
	a.cancelMu.Unlock()

	if !exists {
		return fmt.Errorf("no active message for tab: %s", tabID)
	}

	cancelFunc()
	fmt.Printf("[App.CancelMessage] Cancelled message for tab: %s\n", tabID)
	return nil
}

// TruncateMessages removes all messages from the given index onward
func (a *App) TruncateMessages(tabID string, fromIndex int) error {
	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	if fromIndex < 0 || fromIndex > len(tab.Messages) {
		return fmt.Errorf("invalid index: %d (total: %d)", fromIndex, len(tab.Messages))
	}

	tab.Messages = tab.Messages[:fromIndex]
	fmt.Printf("[App.TruncateMessages] Tab %s truncated to %d messages\n", tabID, fromIndex)

	// Reset CLI session since UI state and session history are now out of sync
	if a.claude.IsInitialized() {
		a.claude.CreateNewChat(tabID)
	}

	runtime.EventsEmit(a.ctx, "tabs-updated")
	go a.saveState()
	return nil
}

// RemoveLastUserMessage removes the last user message from the tab (used before retry to prevent duplicates)
func (a *App) RemoveLastUserMessage(tabID string) error {
	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	// Find and remove the last user message
	for i := len(tab.Messages) - 1; i >= 0; i-- {
		if tab.Messages[i].Role == "user" {
			tab.Messages = append(tab.Messages[:i], tab.Messages[i+1:]...)
			fmt.Printf("[App.RemoveLastUserMessage] Removed message at index %d from tab %s\n", i, tabID)
			runtime.EventsEmit(a.ctx, "tabs-updated")
			go a.saveState()
			return nil
		}
	}

	return nil // No user message to remove
}

// AddContextFile adds a context file to a tab (with duplicate check)
func (a *App) AddContextFile(tabID, filePath string) error {
	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	// Resolve to absolute path
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return fmt.Errorf("invalid path: %s", filePath)
	}

	// Verify file exists
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("파일을 찾을 수 없습니다: %s", absPath)
	}
	if info.IsDir() {
		return fmt.Errorf("디렉토리는 추가할 수 없습니다: %s", absPath)
	}

	// Duplicate check
	for _, cf := range tab.ContextFiles {
		if cf == absPath {
			return fmt.Errorf("이미 추가된 파일입니다: %s", absPath)
		}
	}

	tab.ContextFiles = append(tab.ContextFiles, absPath)
	fmt.Printf("[App.AddContextFile] Tab %s: added context file %s\n", tabID, absPath)
	go a.saveState()
	return nil
}

// RemoveContextFile removes a context file from a tab
func (a *App) RemoveContextFile(tabID, filePath string) error {
	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	for i, cf := range tab.ContextFiles {
		if cf == filePath {
			tab.ContextFiles = append(tab.ContextFiles[:i], tab.ContextFiles[i+1:]...)
			fmt.Printf("[App.RemoveContextFile] Tab %s: removed context file %s\n", tabID, filePath)
			go a.saveState()
			return nil
		}
	}

	return fmt.Errorf("컨텍스트 파일을 찾을 수 없습니다: %s", filePath)
}

// GetContextFileContent reads and returns the content of a file (for preview)
func (a *App) GetContextFileContent(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("파일을 읽을 수 없습니다: %w", err)
	}
	return string(content), nil
}

// SaveFileContent writes content to a file (for editor save)
func (a *App) SaveFileContent(filePath string, content string) error {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("파일이 존재하지 않습니다: %s", filePath)
	}
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("파일 저장 실패: %w", err)
	}
	return nil
}

// DeleteFile removes a file from disk
func (a *App) DeleteFile(filePath string) error {
	info, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		return fmt.Errorf("파일이 존재하지 않습니다: %s", filePath)
	}
	if err != nil {
		return fmt.Errorf("파일 확인 실패: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("디렉토리는 삭제할 수 없습니다: %s", filePath)
	}
	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("파일 삭제 실패: %w", err)
	}
	return nil
}

// RenameFile renames (moves) a file from oldPath to newPath
func (a *App) RenameFile(oldPath string, newPath string) error {
	if _, err := os.Stat(oldPath); os.IsNotExist(err) {
		return fmt.Errorf("파일이 존재하지 않습니다: %s", oldPath)
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("대상 파일이 이미 존재합니다: %s", newPath)
	}
	dir := filepath.Dir(newPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("디렉토리 생성 실패: %w", err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("파일 이름 변경 실패: %w", err)
	}
	return nil
}

// CreateClaudeMd creates a CLAUDE.md file in the given workDir with a default template
func (a *App) CreateClaudeMd(workDir string) (string, error) {
	filePath := filepath.Join(workDir, "CLAUDE.md")
	if _, err := os.Stat(filePath); err == nil {
		return filePath, fmt.Errorf("CLAUDE.md가 이미 존재합니다: %s", filePath)
	}
	content := "# CLAUDE.md\n\n프로젝트 컨텍스트를 여기에 작성하세요.\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("CLAUDE.md 생성 실패: %w", err)
	}
	return filePath, nil
}

// AutoContextFile represents a file automatically referenced by Claude CLI
type AutoContextFile struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Scope  string `json:"scope"` // "project" or "global"
	Exists bool   `json:"exists"`
}

// GetAutoContextFiles detects .claude related files that Claude CLI automatically references
func (a *App) GetAutoContextFiles(workDir string) []AutoContextFile {
	homeDir, _ := os.UserHomeDir()

	candidates := []struct {
		path  string
		name  string
		scope string
	}{
		{filepath.Join(workDir, "CLAUDE.md"), "CLAUDE.md", "project"},
		{filepath.Join(workDir, ".claude", "settings.json"), ".claude/settings.json", "project"},
		{filepath.Join(workDir, ".claude", "settings.local.json"), ".claude/settings.local.json", "project"},
		{filepath.Join(homeDir, ".claude", "CLAUDE.md"), "~/.claude/CLAUDE.md", "global"},
		{filepath.Join(homeDir, ".claude", "settings.json"), "~/.claude/settings.json", "global"},
	}

	// Also scan .claude/commands/ directories
	for _, dir := range []struct {
		base   string
		scope  string
		prefix string
	}{
		{filepath.Join(workDir, ".claude", "commands"), "project", ".claude/commands/"},
		{filepath.Join(homeDir, ".claude", "commands"), "global", "~/.claude/commands/"},
	} {
		entries, err := os.ReadDir(dir.base)
		if err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					candidates = append(candidates, struct {
						path  string
						name  string
						scope string
					}{
						filepath.Join(dir.base, e.Name()),
						dir.prefix + e.Name(),
						dir.scope,
					})
				}
			}
		}
	}

	var results []AutoContextFile
	for _, c := range candidates {
		_, err := os.Stat(c.path)
		if err == nil {
			results = append(results, AutoContextFile{
				Path:   c.path,
				Name:   c.name,
				Scope:  c.scope,
				Exists: true,
			})
		}
	}

	return results
}

// IsGitRepo checks if the given directory contains a .git directory
func (a *App) IsGitRepo(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && info.IsDir()
}

// SetWorkDir sets the working directory for a tab
func (a *App) SetWorkDir(tabID, dir string) error {
	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	// Expand ~ to home dir
	if strings.HasPrefix(dir, "~") {
		home, _ := os.UserHomeDir()
		dir = home + dir[1:]
	}

	// Resolve to absolute path
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("invalid path: %s", dir)
	}

	// Verify directory exists
	info, err := os.Stat(absDir)
	if err != nil {
		return fmt.Errorf("경로를 찾을 수 없습니다: %s", absDir)
	}
	if !info.IsDir() {
		return fmt.Errorf("디렉토리가 아닙니다: %s", absDir)
	}

	tab.WorkDir = absDir
	fmt.Printf("[App.SetWorkDir] Tab %s workdir set to: %s\n", tabID, absDir)

	// Reset CLI session since session files are directory-based
	if a.claude.IsInitialized() {
		a.claude.CreateNewChat(tabID)
	}

	// Clear messages from previous directory and notify frontend
	tab.Messages = []models.Message{}
	a.tabs[tabID] = tab

	runtime.EventsEmit(a.ctx, "tabs-updated")

	// Trigger background agent tasks for the new work directory
	if a.agentService != nil {
		a.agentService.Enqueue(claude.AgentTask{
			Type:    claude.AgentTaskProjectSummary,
			TabID:   tabID,
			WorkDir: absDir,
		})
		// Suggest CLAUDE.md if it doesn't exist in the project
		claudeMdPath := filepath.Join(absDir, "CLAUDE.md")
		if _, err := os.Stat(claudeMdPath); os.IsNotExist(err) {
			a.agentService.Enqueue(claude.AgentTask{
				Type:    claude.AgentTaskClaudeMd,
				TabID:   tabID,
				WorkDir: absDir,
			})
		}
	}

	go a.saveState()

	return nil
}

// splitFileWords splits a filename into words at camelCase, _, -, . boundaries.
// "ReservationConfigTools.kt" → ["Reservation", "Config", "Tools", "kt"]
// "CURRENT_ISSUES_SUMMARY.md" → ["CURRENT", "ISSUES", "SUMMARY", "md"]
func splitFileWords(name string) []string {
	var words []string
	var current []rune

	flush := func() {
		if len(current) > 0 {
			words = append(words, string(current))
			current = nil
		}
	}

	runes := []rune(name)
	for i, r := range runes {
		if r == '_' || r == '-' || r == '.' || r == ' ' {
			flush()
			continue
		}

		if i > 0 {
			prev := runes[i-1]
			// lowercase → uppercase boundary (camelCase)
			if unicode.IsLower(prev) && unicode.IsUpper(r) {
				flush()
			}
			// UPPER UPPER lower boundary: e.g. "XMLParser" → "XML" + "Parser"
			if i+1 < len(runes) && unicode.IsUpper(prev) && unicode.IsUpper(r) && unicode.IsLower(runes[i+1]) {
				flush()
			}
		}

		current = append(current, r)
	}
	flush()
	return words
}

// splitCamelSegments splits a pattern at lowercase→uppercase boundaries.
// "ReCon" → ["Re", "Con"]
// "Reser" → ["Reser"]
// "RESER" → ["RESER"]
func splitCamelSegments(s string) []string {
	var segments []string
	var current []rune

	runes := []rune(s)
	for i, r := range runes {
		if i > 0 && unicode.IsLower(runes[i-1]) && unicode.IsUpper(r) {
			if len(current) > 0 {
				segments = append(segments, string(current))
				current = nil
			}
		}
		current = append(current, r)
	}
	if len(current) > 0 {
		segments = append(segments, string(current))
	}
	return segments
}

// abbreviationMatch checks if each pattern segment is a case-insensitive prefix
// of a file word, in order.
func abbreviationMatch(nameWords, patternSegments []string) bool {
	wi := 0
	for _, seg := range patternSegments {
		segLower := strings.ToLower(seg)
		found := false
		for wi < len(nameWords) {
			wordLower := strings.ToLower(nameWords[wi])
			wi++
			if strings.HasPrefix(wordLower, segLower) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// scoreFileMatch scores how well a filename matches a query.
// Returns: 100 (prefix), 80 (substring), 60 (abbreviation), -1 (no match).
func scoreFileMatch(filename, query string) int {
	nameLower := strings.ToLower(filename)
	queryLower := strings.ToLower(query)

	if strings.HasPrefix(nameLower, queryLower) {
		return 100
	}
	if strings.Contains(nameLower, queryLower) {
		return 80
	}

	nameWords := splitFileWords(filename)
	patternSegments := splitCamelSegments(query)
	if abbreviationMatch(nameWords, patternSegments) {
		return 60
	}

	return -1
}

// SearchFiles recursively searches files under baseDir whose names match query.
// Returns up to 20 results sorted by match score (descending).
func (a *App) SearchFiles(baseDir, query string) []string {
	if query == "" {
		return []string{}
	}

	type scored struct {
		path  string
		score int
	}

	skipDirs := map[string]bool{
		".git": true, ".idea": true, ".vscode": true,
		"node_modules": true, "vendor": true, "build": true,
		"dist": true, "target": true, "__pycache__": true,
		".gradle": true, ".next": true, ".svelte-kit": true,
		".turbo": true, ".cache": true,
	}

	const maxDepth = 10
	baseParts := strings.Count(filepath.Clean(baseDir), string(filepath.Separator))

	var results []scored

	filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}

		name := d.Name()

		// Skip hidden files/dirs
		if strings.HasPrefix(name, ".") && path != baseDir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			// Skip known non-useful directories
			if skipDirs[name] {
				return filepath.SkipDir
			}
			// Depth limit
			depth := strings.Count(filepath.Clean(path), string(filepath.Separator)) - baseParts
			if depth > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}

		// Score file name against query
		score := scoreFileMatch(name, query)
		if score > 0 {
			results = append(results, scored{path, score})
		}
		return nil
	})

	// Sort by score descending, then path ascending
	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].path < results[j].path
	})

	if len(results) > 20 {
		results = results[:20]
	}

	paths := make([]string, len(results))
	for i, r := range results {
		paths[i] = r.path
	}
	return paths
}

// CompletePath returns directory entries matching the given partial path for tab-completion
func (a *App) CompletePath(partial string) []string {
	// Expand ~
	if strings.HasPrefix(partial, "~") {
		home, _ := os.UserHomeDir()
		partial = home + partial[1:]
	}

	// Determine the directory to list and the prefix to match
	dir := filepath.Dir(partial)
	prefix := filepath.Base(partial)

	// If partial ends with /, list contents of that directory
	if strings.HasSuffix(partial, "/") {
		dir = partial
		prefix = ""
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}

	type matchEntry struct {
		path  string
		score int // 0 = prefix, 1 = contains, 2 = fuzzy
	}

	var results []matchEntry
	for _, entry := range entries {
		name := entry.Name()
		// Skip hidden files unless prefix starts with .
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}

		fullPath := filepath.Join(dir, name)
		if entry.IsDir() {
			fullPath += "/"
		}

		if prefix == "" {
			results = append(results, matchEntry{fullPath, 0})
		} else {
			nameLower := strings.ToLower(name)
			prefixLower := strings.ToLower(prefix)

			if strings.HasPrefix(nameLower, prefixLower) {
				results = append(results, matchEntry{fullPath, 0})
			} else if strings.Contains(nameLower, prefixLower) {
				results = append(results, matchEntry{fullPath, 1})
			} else if abbreviationMatch(splitFileWords(name), splitCamelSegments(prefix)) {
				results = append(results, matchEntry{fullPath, 2})
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score < results[j].score
		}
		return results[i].path < results[j].path
	})

	// Limit results
	if len(results) > 20 {
		results = results[:20]
	}

	paths := make([]string, len(results))
	for i, r := range results {
		paths[i] = r.path
	}

	return paths
}

// OpenExpandedView opens the given markdown content in a new browser window
// with full-screen support, font size controls, and dark theme styling.
func (a *App) OpenExpandedView(content string) error {
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("failed to encode content: %w", err)
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="ko">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>Claude — 응답 확대 보기</title>
<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/styles/github-dark.min.css">
<script src="https://cdnjs.cloudflare.com/ajax/libs/marked/12.0.0/marked.min.js"></script>
<script src="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.9.0/highlight.min.js"></script>
<style>
*{margin:0;padding:0;box-sizing:border-box}
html,body{height:100%%;background:#1a1a2e;color:#e0e0e0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif}
body{display:flex;flex-direction:column}
.toolbar{display:flex;align-items:center;gap:10px;padding:8px 16px;background:#16213e;border-bottom:1px solid #2a2a4a;flex-shrink:0;-webkit-app-region:drag}
.toolbar *{-webkit-app-region:no-drag}
.toolbar-title{font-size:13px;font-weight:600;color:#8892b0;margin-right:auto}
.font-controls{display:flex;align-items:center;gap:6px}
.font-btn{width:34px;height:30px;border:1px solid #2a2a4a;border-radius:6px;background:#1a1a2e;color:#ccc;font-size:13px;font-weight:700;cursor:pointer;display:flex;align-items:center;justify-content:center;transition:all .15s}
.font-btn:hover{background:#0f3460;color:#fff;border-color:#0f3460}
.font-size-label{font-size:11px;color:#8892b0;font-family:'Menlo','Monaco',monospace;min-width:38px;text-align:center}
.content{flex:1;overflow-y:auto;padding:32px 48px;line-height:1.7}
.content::-webkit-scrollbar{width:10px}
.content::-webkit-scrollbar-track{background:#1a1a2e}
.content::-webkit-scrollbar-thumb{background:#2a2a4a;border-radius:5px}
.content::-webkit-scrollbar-thumb:hover{background:#3a3a5a}
.content h1,.content h2,.content h3,.content h4{color:#e2e8f0;margin:1.2em 0 .6em;font-weight:600}
.content h1{font-size:1.8em;border-bottom:1px solid #2a2a4a;padding-bottom:.4em}
.content h2{font-size:1.4em;border-bottom:1px solid #2a2a4a;padding-bottom:.3em}
.content h3{font-size:1.15em}
.content p{margin:.7em 0}
.content ul,.content ol{margin:.7em 0;padding-left:1.8em}
.content li{margin:.3em 0}
.content a{color:#64b5f6;text-decoration:none}
.content a:hover{text-decoration:underline}
.content code{font-family:'Menlo','Monaco','Courier New',monospace;background:#16213e;padding:2px 6px;border-radius:4px;font-size:.9em;color:#e6db74}
.content pre{background:#0f0f23;border:1px solid #2a2a4a;border-radius:8px;padding:16px;margin:1em 0;overflow-x:auto}
.content pre code{background:none;padding:0;font-size:.85em;color:#e0e0e0}
.content blockquote{border-left:3px solid #0f3460;padding:.5em 1em;margin:1em 0;color:#8892b0;background:rgba(15,52,96,.2);border-radius:0 6px 6px 0}
.content table{border-collapse:collapse;margin:1em 0;width:100%%}
.content th,.content td{border:1px solid #2a2a4a;padding:8px 12px;text-align:left}
.content th{background:#16213e;font-weight:600}
.content tr:nth-child(even){background:rgba(22,33,62,.5)}
.content hr{border:none;border-top:1px solid #2a2a4a;margin:1.5em 0}
.content strong{color:#f0f0f0}
.content img{max-width:100%%;border-radius:8px}
</style>
</head>
<body>
<div class="toolbar">
  <span class="toolbar-title">Claude — 응답 확대 보기</span>
  <div class="font-controls">
    <button class="font-btn" onclick="changeFontSize(-2)" title="글자 축소">A−</button>
    <span class="font-size-label" id="fontLabel">16px</span>
    <button class="font-btn" onclick="changeFontSize(2)" title="글자 확대">A+</button>
  </div>
</div>
<div class="content" id="content"></div>
<script>
var fontSize = 16;
var contentEl = document.getElementById('content');
var fontLabel = document.getElementById('fontLabel');
var rawContent = %s;

marked.setOptions({
  highlight: function(code, lang) {
    if (lang && hljs.getLanguage(lang)) {
      try { return hljs.highlight(code, {language: lang}).value; } catch(e) {}
    }
    return hljs.highlightAuto(code).value;
  },
  breaks: false,
  gfm: true
});
contentEl.innerHTML = marked.parse(rawContent);
contentEl.style.fontSize = fontSize + 'px';

function changeFontSize(delta) {
  fontSize = Math.max(10, Math.min(32, fontSize + delta));
  contentEl.style.fontSize = fontSize + 'px';
  fontLabel.textContent = fontSize + 'px';
}

document.addEventListener('keydown', function(e) {
  if ((e.metaKey || e.ctrlKey) && e.key === '=') { e.preventDefault(); changeFontSize(2); }
  if ((e.metaKey || e.ctrlKey) && e.key === '-') { e.preventDefault(); changeFontSize(-2); }
  if ((e.metaKey || e.ctrlKey) && e.key === '0') { e.preventDefault(); fontSize=16; contentEl.style.fontSize='16px'; fontLabel.textContent='16px'; }
});
</script>
</body>
</html>`, string(contentJSON))

	tmpDir := filepath.Join(os.TempDir(), "claude-gui-expanded")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}

	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("expanded_%d.html", time.Now().UnixNano()))
	if err := os.WriteFile(tmpPath, []byte(html), 0644); err != nil {
		return fmt.Errorf("failed to write HTML: %w", err)
	}

	cmd := exec.Command("open", tmpPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to open browser: %w", err)
	}
	return nil
}

// saveState persists the current application state to disk.
// Safe to call from goroutines; errors are logged but not propagated.
func (a *App) saveState() {
	if a.store == nil {
		return
	}

	a.tabsMu.RLock()
	tabs := make([]*models.TabState, 0, len(a.tabs))
	for _, tab := range a.tabs {
		cp := *tab
		cp.Orchestrator = nil // ephemeral, don't persist
		cp.TeamsState = nil   // ephemeral, don't persist
		if cp.Messages == nil {
			cp.Messages = []models.Message{}
		}
		if cp.ContextFiles == nil {
			cp.ContextFiles = []string{}
		}
		tabs = append(tabs, &cp)
	}
	settings := *a.settings
	a.tabsMu.RUnlock()

	// Export CLI session mappings
	exported := a.claude.ExportSessions()
	sessionMappings := make(map[string]persistence.SessionMapping, len(exported))
	for k, v := range exported {
		sessionMappings[k] = persistence.SessionMapping{
			SessionID: v.SessionID,
			Started:   v.Started,
		}
	}

	state := &persistence.AppState{
		Model:           a.model,
		Settings:        &settings,
		Tabs:            tabs,
		SessionMappings: sessionMappings,
	}

	if err := a.store.Save(state); err != nil {
		fmt.Printf("[App.saveState] Error saving state: %v\n", err)
	}
}

// shutdown is called when the app is closing
func (a *App) shutdown(ctx context.Context) {
	// Stop background agent service
	if a.agentService != nil {
		a.agentService.Stop()
	}

	// Synchronous save before exit
	a.saveState()

	// Close Claude service
	if a.claude != nil {
		if err := a.claude.Close(); err != nil {
			fmt.Printf("Error closing Claude service: %v\n", err)
		}
	}
}

// handleAgentResult dispatches background agent results to the frontend via Wails events
func (a *App) handleAgentResult(result claude.AgentResult) {
	switch result.Type {
	case claude.AgentTaskTabRename:
		runtime.EventsEmit(a.ctx, "agent-tab-rename", map[string]interface{}{
			"tabID": result.TabID,
			"name":  result.Data["name"],
		})
	case claude.AgentTaskProjectSummary:
		runtime.EventsEmit(a.ctx, "agent-project-summary", map[string]interface{}{
			"workDir":   result.WorkDir,
			"summary":   result.Data["summary"],
			"language":  result.Data["language"],
			"framework": result.Data["framework"],
		})
	case claude.AgentTaskClaudeMd:
		runtime.EventsEmit(a.ctx, "agent-claudemd-suggestion", map[string]interface{}{
			"workDir": result.WorkDir,
			"content": result.Data["content"],
		})
	case claude.AgentTaskContextFiles:
		runtime.EventsEmit(a.ctx, "agent-context-recommendation", map[string]interface{}{
			"tabID": result.TabID,
			"files": result.Data["files"],
		})
	case claude.AgentTaskCodeReview:
		runtime.EventsEmit(a.ctx, "agent-code-review", map[string]interface{}{
			"tabID":   result.TabID,
			"issues":  result.Data["issues"],
			"summary": result.Data["summary"],
		})
	}
}

// GetAgentModel returns the current agent model name
func (a *App) GetAgentModel() string {
	return a.settings.AgentModel
}

// SetAgentModel updates the model used for background agent tasks
func (a *App) SetAgentModel(model string) error {
	a.settings.AgentModel = model
	if a.agentService != nil {
		a.agentService.SetModel(model)
	}
	go a.saveState()
	return nil
}

// RequestContextRecommendation triggers a context file recommendation for the given tab
func (a *App) RequestContextRecommendation(tabID string) {
	tab, exists := a.tabs[tabID]
	if !exists || a.agentService == nil {
		return
	}
	a.agentService.Enqueue(claude.AgentTask{
		Type:    claude.AgentTaskContextFiles,
		TabID:   tabID,
		WorkDir: tab.WorkDir,
	})
}

// RequestCodeReview triggers a code review agent for the given tab's git diff
func (a *App) RequestCodeReview(tabID string) error {
	tab, exists := a.tabs[tabID]
	if !exists || a.agentService == nil {
		return fmt.Errorf("tab not found or agent service unavailable")
	}

	// Run git diff in the tab's workDir
	cmd := exec.CommandContext(context.Background(), "git", "diff")
	cmd.Dir = tab.WorkDir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git diff failed: %w", err)
	}
	diff := string(out)

	// Also check staged changes if unstaged diff is empty
	if diff == "" {
		cmd2 := exec.CommandContext(context.Background(), "git", "diff", "--staged")
		cmd2.Dir = tab.WorkDir
		out2, _ := cmd2.Output()
		diff = string(out2)
	}

	if diff == "" {
		return fmt.Errorf("no changes to review")
	}

	a.agentService.Enqueue(claude.AgentTask{
		Type:    claude.AgentTaskCodeReview,
		TabID:   tabID,
		WorkDir: tab.WorkDir,
		GitDiff: diff,
	})
	return nil
}

// SelectFiles opens a file dialog and returns selected file paths
func (a *App) SelectFiles() ([]string, error) {
	files, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "파일 선택",
		Filters: []runtime.FileFilter{
			{
				DisplayName: "코드 파일",
				Pattern:     "*.go;*.js;*.ts;*.tsx;*.jsx;*.py;*.java;*.c;*.cpp;*.h;*.rs;*.rb;*.php;*.swift;*.kt;*.html;*.css;*.scss;*.json;*.yaml;*.yml;*.toml;*.md;*.txt;*.sh;*.sql;*.svelte;*.vue",
			},
			{
				DisplayName: "이미지 파일 (*.jpg, *.png, *.gif, *.webp)",
				Pattern:     "*.jpg;*.jpeg;*.png;*.gif;*.webp",
			},
			{
				DisplayName: "모든 파일",
				Pattern:     "*.*",
			},
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to open file dialog: %w", err)
	}

	return files, nil
}

// GetFileInfo returns information about a file
func (a *App) GetFileInfo(filePath string) (*utils.FileInfo, error) {
	return utils.ProcessFile(filePath, 10) // 10MB max
}

// SaveDroppedImage saves base64 image data from drag-and-drop to a temp file
// Returns the absolute file path
func (a *App) SaveDroppedImage(fileName string, base64Data string) (string, error) {
	// Decode base64
	data, err := utils.DecodeBase64(base64Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode image data: %w", err)
	}

	// Create temp dir if needed
	tmpDir := filepath.Join(os.TempDir(), "claude-gui-drops")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Write to temp file with unique name
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), fileName))
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write temp file: %w", err)
	}

	fmt.Printf("[App.SaveDroppedImage] Saved dropped image: %s\n", tmpPath)
	return tmpPath, nil
}

// SaveDroppedFile saves base64 file data from drag-and-drop to a temp file.
// Supports any file type (code files, images, etc.). Returns the absolute file path.
func (a *App) SaveDroppedFile(fileName string, base64Data string) (string, error) {
	// Decode base64
	data, err := utils.DecodeBase64(base64Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode file data: %w", err)
	}

	// Create temp dir if needed
	tmpDir := filepath.Join(os.TempDir(), "claude-gui-drops")
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Write to temp file with unique name
	tmpPath := filepath.Join(tmpDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), fileName))
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write temp file: %w", err)
	}

	fmt.Printf("[App.SaveDroppedFile] Saved dropped file: %s\n", tmpPath)
	return tmpPath, nil
}

// ReadFileSnippet reads lines from a file for snippet preview
func (a *App) ReadFileSnippet(filePath string, startLine, endLine int) (map[string]interface{}, error) {
	content, totalLines, err := utils.ReadFileLines(filePath, startLine, endLine)
	if err != nil {
		return nil, err
	}
	lang := utils.GetLanguageFromExt(filePath)
	return map[string]interface{}{
		"content":    content,
		"language":   lang,
		"totalLines": totalLines,
		"fileName":   filepath.Base(filePath),
	}, nil
}
