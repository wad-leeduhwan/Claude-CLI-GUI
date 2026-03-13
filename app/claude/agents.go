package claude

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// AgentDef represents a single agent definition for the --agents CLI flag
type AgentDef struct {
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Tools       []string `json:"tools,omitempty"`
	Model       string   `json:"model,omitempty"`
	MaxTurns    int      `json:"maxTurns,omitempty"`
}

// WorkerTabInfo holds the minimum info needed to generate an agent definition
type WorkerTabInfo struct {
	ID      string
	Name    string
	WorkDir string
}

// BuildAgentsJSON generates the --agents JSON string from connected worker tab info.
// Each worker tab becomes a named agent that Claude can delegate tasks to.
func BuildAgentsJSON(workerTabs []WorkerTabInfo) (string, error) {
	if len(workerTabs) == 0 {
		return "", fmt.Errorf("no worker tabs provided")
	}

	agents := make(map[string]AgentDef)
	for _, tab := range workerTabs {
		agentName := sanitizeAgentName(tab.Name)
		agents[agentName] = AgentDef{
			Description: fmt.Sprintf("%s 디렉토리의 코드 작업 담당. 이 에이전트에게 %s 관련 작업을 위임하세요.", tab.WorkDir, tab.Name),
			Prompt:      fmt.Sprintf("당신은 %s에서 작업하는 개발자입니다. 작업 디렉토리: %s. 할당된 작업을 정확하고 완전하게 수행하세요.", tab.Name, tab.WorkDir),
			Tools:       []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep"},
		}
	}

	data, err := json.Marshal(agents)
	if err != nil {
		return "", fmt.Errorf("failed to marshal agents JSON: %w", err)
	}

	return string(data), nil
}

// sanitizeAgentName converts a tab name into a valid agent name (alphanumeric + hyphens).
var nonAlphanumRegex = regexp.MustCompile(`[^a-zA-Z0-9가-힣_-]+`)

func sanitizeAgentName(name string) string {
	// Replace spaces with hyphens
	name = strings.ReplaceAll(name, " ", "-")
	// Remove non-alphanumeric characters (keep Korean, Latin, digits, hyphens, underscores)
	name = nonAlphanumRegex.ReplaceAllString(name, "")
	if name == "" {
		name = "worker"
	}
	return name
}
