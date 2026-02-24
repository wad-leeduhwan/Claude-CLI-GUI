package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"claude-gui/app/claude"
	"claude-gui/app/models"
	"claude-gui/app/utils"
	"claude-gui/app/websocket"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Allowed models list
var allowedModels = []string{
	"claude-sonnet-4-20250514",
	"claude-haiku-4-20250414",
	"claude-opus-4-20250514",
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
}

// NewApp creates a new App application struct
func NewApp() *App {
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

	homeDir, _ := os.UserHomeDir()

	// Initialize 1 conversation tab
	for i := 1; i <= 1; i++ {
		tabID := fmt.Sprintf("conversation-%d", i)
		a.tabs[tabID] = &models.TabState{
			ID:       tabID,
			Name:     fmt.Sprintf("대화 %d", i),
			Messages: []models.Message{},
			IsActive: false,
			WorkDir:  homeDir,
		}
		// Initialize tab settings
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
}

// GetWebSocketPort returns the WebSocket server port for frontend connection
func (a *App) GetWebSocketPort() int {
	return a.wsServer.GetPort()
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
	// TODO: Persist settings to disk
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
	guiContext := "You are running inside a GUI chat application (not an interactive terminal). " +
		"You CANNOT ask interactive questions mid-execution. If you need clarification or user input, STOP and ask in your response text. " +
		"If the user's request is ambiguous, ask clarifying questions BEFORE taking action. " +
		"Present options and wait for the user's choice when multiple approaches are possible. " +
		"The user will respond in the next message."

	var systemPrompt string
	var tools string // empty = all tools (default)

	if tab.PlanMode {
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
	sendStartTime := time.Now()

	maxTurns := 0 // 0 = unlimited
	if tab.PlanMode {
		maxTurns = 100 // Plan mode: enough turns to thoroughly analyze codebase
	}

	response, turnCount, err := a.claude.SendMessage(ctx, tabID, messageToSend, files, tab.WorkDir, maxTurns, systemPrompt, permissionMode, tools, func(chunk string) {
		chunkCount++
		streamingContent = chunk
		fmt.Printf("[App.SendMessage] Chunk %d received (length=%d) - sending via WebSocket\n", chunkCount, len(chunk))

		// Send chunk via WebSocket for real-time delivery
		a.wsServer.SendStreamChunk(tabID, streamingContent)
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
		Role:        "assistant",
		Content:     response,
		Attachments: []string{},
		Timestamp:   0,
		DurationMs:  durationMs,
	}
	tab.Messages = append(tab.Messages, responseMsg)

	// If max turns were exhausted, add a system message to inform the user
	if maxTurns > 0 && turnCount >= maxTurns {
		fmt.Printf("[App.SendMessage] Max turns exhausted (%d/%d) - adding system notice\n", turnCount, maxTurns)
		turnsMsg := models.Message{
			Role:        "system",
			Content:     fmt.Sprintf("⚠️ 턴 제한(%d턴)에 도달하여 작업이 중단되었습니다. 응답이 완전하지 않을 수 있습니다. 이어서 진행하려면 메시지를 보내주세요.", maxTurns),
			Attachments: []string{},
			Timestamp:   0,
		}
		tab.Messages = append(tab.Messages, turnsMsg)
	}

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

	return nil
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

	return nil
}

// RenameTab renames a tab
func (a *App) RenameTab(tabID, newName string) error {
	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	tab.Name = newName
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
	tab, exists := a.tabs[tabID]
	if !exists {
		return fmt.Errorf("tab not found: %s", tabID)
	}

	tab.PlanMode = enabled
	fmt.Printf("[App.ToggleTabPlanMode] Tab %s plan mode: %v\n", tabID, enabled)
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

	analysisResponse, _, err := a.claude.SendMessage(ctx, adminTabID, analysisPrompt, files, adminTab.WorkDir, 20, "You are analyzing a codebase. Explore thoroughly using available tools.", "bypassPermissions", "", func(chunk string) {
		a.wsServer.SendStreamChunk(adminTabID, chunk)
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

	decompositionResponse, _, err = a.claude.SendMessage(ctx, adminTabID, decompositionPrompt, nil, adminTab.WorkDir, 1, "You are a task orchestrator. Respond ONLY with valid JSON.", "bypassPermissions", "", func(chunk string) {
		a.wsServer.SendStreamChunk(adminTabID, chunk)
	})
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

	synthesisResponse, _, err := a.claude.SendMessage(ctx, adminTabID, synthesisPrompt, nil, adminTab.WorkDir, 1, "You are a helpful assistant summarizing orchestrated task results.", "bypassPermissions", "", func(chunk string) {
		synthStreamContent = chunk
		a.wsServer.SendStreamChunk(adminTabID, chunk)
	})
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
			"type":       "orchestration",
			"jobId":      jobID,
			"phase":      "synthesis",
			"success":    fmt.Sprintf("%d", successCount),
			"failed":     fmt.Sprintf("%d", failCount),
			"synthMs":    fmt.Sprintf("%d", synthDuration),
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
			return nil
		}
	}
	return fmt.Errorf("허용되지 않는 모델입니다: %s", modelName)
}

// GetAvailableModels returns the list of allowed model names
func (a *App) GetAvailableModels() []string {
	return allowedModels
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
		base  string
		scope string
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

// shutdown is called when the app is closing
func (a *App) shutdown(ctx context.Context) {
	// Close Claude service
	if a.claude != nil {
		if err := a.claude.Close(); err != nil {
			fmt.Printf("Error closing Claude service: %v\n", err)
		}
	}
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
