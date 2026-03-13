package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

const AgentSessionPrefix = "__agent__"

// AgentTaskType defines the type of background agent task
type AgentTaskType string

const (
	AgentTaskTabRename      AgentTaskType = "tab-rename"
	AgentTaskProjectSummary AgentTaskType = "project-summary"
	AgentTaskClaudeMd       AgentTaskType = "claudemd-suggestion"
	AgentTaskContextFiles   AgentTaskType = "context-files"
	AgentTaskCodeReview     AgentTaskType = "code-review"
)

// AgentTask represents a background task to be processed
type AgentTask struct {
	Type    AgentTaskType
	TabID   string
	WorkDir string
	// Extra context for the task
	FirstUserMessage string // for tab-rename
	GitDiff          string // for code-review: pre-collected git diff output
}

// AgentResult represents the result of a background agent task
type AgentResult struct {
	Type    AgentTaskType
	TabID   string
	WorkDir string
	Data    map[string]interface{}
	Error   error
}

// AgentService manages background AI tasks using CLI --print mode
type AgentService struct {
	service   *Service
	model     string
	taskQueue chan AgentTask
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	OnResult  func(AgentResult)
}

// NewAgentService creates a new agent service
func NewAgentService(service *Service, model string) *AgentService {
	return &AgentService{
		service:   service,
		model:     model,
		taskQueue: make(chan AgentTask, 20),
	}
}

// Start launches worker goroutines to process agent tasks
func (as *AgentService) Start(numWorkers int) {
	as.ctx, as.cancel = context.WithCancel(context.Background())
	for i := 0; i < numWorkers; i++ {
		as.wg.Add(1)
		go as.worker(i)
	}
	fmt.Printf("[AgentService] Started with %d workers, model=%s\n", numWorkers, as.model)
}

// Stop shuts down the agent service
func (as *AgentService) Stop() {
	if as.cancel != nil {
		as.cancel()
	}
	as.wg.Wait()
	fmt.Println("[AgentService] Stopped")
}

// Enqueue adds a task to the queue. Non-blocking; drops if queue is full.
func (as *AgentService) Enqueue(task AgentTask) {
	select {
	case as.taskQueue <- task:
		fmt.Printf("[AgentService] Enqueued task: %s (tab=%s, workDir=%s)\n", task.Type, task.TabID, task.WorkDir)
	default:
		fmt.Printf("[AgentService] Queue full, dropping task: %s\n", task.Type)
	}
}

// SetModel updates the model used for agent tasks
func (as *AgentService) SetModel(model string) {
	as.model = model
	fmt.Printf("[AgentService] Model changed to: %s\n", model)
}

func (as *AgentService) worker(id int) {
	defer as.wg.Done()
	for {
		select {
		case <-as.ctx.Done():
			return
		case task, ok := <-as.taskQueue:
			if !ok {
				return
			}
			fmt.Printf("[AgentService] Worker %d processing: %s\n", id, task.Type)
			as.processTask(task)
		}
	}
}

func (as *AgentService) processTask(task AgentTask) {
	timeout := 30 * time.Second
	if task.Type == AgentTaskCodeReview {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(as.ctx, timeout)
	defer cancel()

	var result AgentResult
	result.Type = task.Type
	result.TabID = task.TabID
	result.WorkDir = task.WorkDir

	switch task.Type {
	case AgentTaskTabRename:
		result = as.handleTabRename(ctx, task)
	case AgentTaskProjectSummary:
		result = as.handleProjectSummary(ctx, task)
	case AgentTaskClaudeMd:
		result = as.handleClaudeMdSuggestion(ctx, task)
	case AgentTaskContextFiles:
		result = as.handleContextFiles(ctx, task)
	case AgentTaskCodeReview:
		result = as.handleCodeReview(ctx, task)
	default:
		result.Error = fmt.Errorf("unknown task type: %s", task.Type)
	}

	if result.Error != nil {
		fmt.Printf("[AgentService] Task %s failed: %v\n", task.Type, result.Error)
	} else {
		fmt.Printf("[AgentService] Task %s completed successfully\n", task.Type)
	}

	if as.OnResult != nil && result.Error == nil {
		as.OnResult(result)
	}
}

// sendAgentMessage sends a single-turn message via CLI --print mode
func (as *AgentService) sendAgentMessage(ctx context.Context, conversationID, message, workDir, systemPrompt string) (string, error) {
	response, _, _, err := as.service.SendMessage(
		ctx,
		AgentSessionPrefix+conversationID,
		message,
		nil,
		workDir,
		1,           // maxTurns: single turn
		systemPrompt,
		"plan",      // read-only permission
		"Read,Glob,Grep", // limited tools
		"",          // no agents
		nil, nil, nil,
	)
	if err != nil {
		return "", err
	}
	return response, nil
}

func (as *AgentService) handleTabRename(ctx context.Context, task AgentTask) AgentResult {
	result := AgentResult{Type: task.Type, TabID: task.TabID, WorkDir: task.WorkDir}

	systemPrompt := `You are a tab naming assistant. Based on the user's first message in a conversation, suggest a short, descriptive tab name (2-5 words).
Respond ONLY with a JSON object: {"name": "suggested name"}
The name should be concise and capture the main topic. Use the same language as the user's message.
Do NOT include any explanation or markdown.`

	message := fmt.Sprintf("Based on this first message, suggest a tab name:\n\n%s", task.FirstUserMessage)

	response, err := as.sendAgentMessage(ctx, fmt.Sprintf("tab-rename-%s-%d", task.TabID, time.Now().UnixMilli()), message, task.WorkDir, systemPrompt)
	if err != nil {
		result.Error = err
		return result
	}

	jsonStr := extractJSON(response)
	if jsonStr == "" {
		result.Error = fmt.Errorf("no JSON found in response: %s", truncate(response, 200))
		return result
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		result.Error = fmt.Errorf("failed to parse JSON: %w", err)
		return result
	}

	name, ok := data["name"].(string)
	if !ok || name == "" {
		result.Error = fmt.Errorf("no 'name' field in response")
		return result
	}

	result.Data = map[string]interface{}{"name": name}
	return result
}

func (as *AgentService) handleProjectSummary(ctx context.Context, task AgentTask) AgentResult {
	result := AgentResult{Type: task.Type, TabID: task.TabID, WorkDir: task.WorkDir}

	systemPrompt := `You are a project analyzer. Analyze the project in the current working directory and provide a brief summary.
Respond ONLY with a JSON object: {"summary": "1-2 sentence summary", "language": "primary language", "framework": "main framework or 'none'"}
Do NOT include any explanation or markdown. Keep the summary concise.`

	message := "Analyze this project directory and provide a brief summary. Look at key files like package.json, go.mod, Cargo.toml, README.md, etc."

	response, err := as.sendAgentMessage(ctx, fmt.Sprintf("project-summary-%d", time.Now().UnixMilli()), message, task.WorkDir, systemPrompt)
	if err != nil {
		result.Error = err
		return result
	}

	jsonStr := extractJSON(response)
	if jsonStr == "" {
		result.Error = fmt.Errorf("no JSON found in response")
		return result
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		result.Error = fmt.Errorf("failed to parse JSON: %w", err)
		return result
	}

	result.Data = data
	return result
}

func (as *AgentService) handleClaudeMdSuggestion(ctx context.Context, task AgentTask) AgentResult {
	result := AgentResult{Type: task.Type, TabID: task.TabID, WorkDir: task.WorkDir}

	systemPrompt := `You are a CLAUDE.md generator. Analyze the project and generate a CLAUDE.md file content.
CLAUDE.md is a project instruction file that helps AI assistants understand the project context.
Respond ONLY with a JSON object: {"content": "the full CLAUDE.md content"}
The content should be under 50 lines and include:
- Project description
- Tech stack
- Key conventions
- Build/test commands
Do NOT include any explanation outside the JSON.`

	message := "Analyze this project and generate a CLAUDE.md file. Look at the project structure, config files, and codebase to understand conventions."

	response, err := as.sendAgentMessage(ctx, fmt.Sprintf("claudemd-%d", time.Now().UnixMilli()), message, task.WorkDir, systemPrompt)
	if err != nil {
		result.Error = err
		return result
	}

	jsonStr := extractJSON(response)
	if jsonStr == "" {
		result.Error = fmt.Errorf("no JSON found in response")
		return result
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		result.Error = fmt.Errorf("failed to parse JSON: %w", err)
		return result
	}

	if _, ok := data["content"].(string); !ok {
		result.Error = fmt.Errorf("no 'content' field in response")
		return result
	}

	result.Data = data
	return result
}

func (as *AgentService) handleContextFiles(ctx context.Context, task AgentTask) AgentResult {
	result := AgentResult{Type: task.Type, TabID: task.TabID, WorkDir: task.WorkDir}

	systemPrompt := `You are a project context analyzer. Analyze the project and recommend 3-8 important files that would be useful as context for AI-assisted development.
Respond ONLY with a JSON object: {"files": ["path/to/file1", "path/to/file2", ...]}
Recommend files like:
- Main entry points
- Configuration files
- Core type definitions
- Important documentation
Use relative paths from the project root. Do NOT include any explanation outside the JSON.`

	message := "Analyze this project and recommend the most important files that should be included as context for understanding the project."

	response, err := as.sendAgentMessage(ctx, fmt.Sprintf("context-files-%s-%d", task.TabID, time.Now().UnixMilli()), message, task.WorkDir, systemPrompt)
	if err != nil {
		result.Error = err
		return result
	}

	jsonStr := extractJSON(response)
	if jsonStr == "" {
		result.Error = fmt.Errorf("no JSON found in response")
		return result
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		result.Error = fmt.Errorf("failed to parse JSON: %w", err)
		return result
	}

	if _, ok := data["files"]; !ok {
		result.Error = fmt.Errorf("no 'files' field in response")
		return result
	}

	result.Data = data
	return result
}

func (as *AgentService) handleCodeReview(ctx context.Context, task AgentTask) AgentResult {
	result := AgentResult{Type: task.Type, TabID: task.TabID, WorkDir: task.WorkDir}

	diff := task.GitDiff
	if diff == "" {
		result.Data = map[string]interface{}{
			"issues":  []interface{}{},
			"summary": "변경사항이 없습니다.",
		}
		return result
	}

	// Truncate very large diffs
	if len(diff) > 50000 {
		diff = diff[:50000] + "\n\n... (diff truncated due to size)"
	}

	systemPrompt := `You are an expert code reviewer. Analyze the provided git diff and find bugs, security vulnerabilities, code style issues, and performance problems.
Respond ONLY with a JSON object in this exact format:
{"issues": [{"severity": "error|warning|info", "file": "path/to/file", "line": 42, "message": "description of the issue", "suggestion": "how to fix it"}], "summary": "1-2 sentence overall review summary"}
- severity must be one of: "error", "warning", "info"
- line can be 0 if not applicable
- Keep the summary concise
- If no issues found, return an empty issues array
- Use the same language as the diff comments (Korean if Korean comments exist, otherwise English)
Do NOT include any explanation or markdown outside the JSON.`

	message := fmt.Sprintf("Please review this git diff:\n\n```diff\n%s\n```", diff)

	response, err := as.sendAgentMessage(ctx, fmt.Sprintf("code-review-%s-%d", task.TabID, time.Now().UnixMilli()), message, task.WorkDir, systemPrompt)
	if err != nil {
		result.Error = err
		return result
	}

	jsonStr := extractJSON(response)
	if jsonStr == "" {
		result.Error = fmt.Errorf("no JSON found in response: %s", truncate(response, 200))
		return result
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		result.Error = fmt.Errorf("failed to parse JSON: %w", err)
		return result
	}

	if _, ok := data["issues"]; !ok {
		data["issues"] = []interface{}{}
	}
	if _, ok := data["summary"]; !ok {
		data["summary"] = ""
	}

	result.Data = data
	return result
}

// extractJSON extracts the first JSON object from a string (handles LLM wrapper text)
func extractJSON(s string) string {
	// Find first '{' and matching '}'
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}

	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
