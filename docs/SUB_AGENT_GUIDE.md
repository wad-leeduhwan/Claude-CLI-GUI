# Claude GUI - 서브에이전트(오케스트레이션) 활용 가이드

## 목차

1. [개요](#1-개요)
2. [두 가지 오케스트레이션 모드](#2-두-가지-오케스트레이션-모드)
3. [Admin 모드 - 4단계 파이프라인](#3-admin-모드---4단계-파이프라인)
4. [Teams 모드 - CLI 네이티브 에이전트](#4-teams-모드---cli-네이티브-에이전트)
5. [아키텍처](#5-아키텍처)
6. [사용 방법](#6-사용-방법)
7. [백엔드 API 레퍼런스](#7-백엔드-api-레퍼런스)
8. [프론트엔드 연동](#8-프론트엔드-연동)
9. [데이터 모델](#9-데이터-모델)
10. [WebSocket 이벤트](#10-websocket-이벤트)
11. [활용 시나리오](#11-활용-시나리오)
12. [제한사항 및 주의사항](#12-제한사항-및-주의사항)

---

## 1. 개요

Claude GUI는 하나의 관리 탭이 여러 워커 탭에 작업을 분배하는 **멀티에이전트 오케스트레이션**을 지원합니다. 현재 두 가지 방식을 제공합니다.

| 모드 | 방식 | 특징 |
|------|------|------|
| **Admin 모드** | GUI가 직접 4단계 파이프라인 수행 | 분석→분해→병렬 디스패치→종합. 각 워커가 독립 CLI 세션 |
| **Teams 모드** (Beta) | Claude CLI `--agents` 플래그로 단일 호출 | Claude가 자율적으로 서브에이전트에 위임. 단일 세션 |

두 모드는 **상호 배타적**입니다. 한 탭에서 Admin과 Teams를 동시에 활성화할 수 없습니다.

### 핵심 용어

| 용어 | 설명 |
|------|------|
| **Admin/Team Tab** | 오케스트레이션을 제어하는 관리자 탭 |
| **Worker Tab** | 작업을 수행하는 워커 탭. 각각 독립된 Claude CLI 세션 보유 |
| **OrchestrationJob** | Admin 모드에서 하나의 사용자 요청으로 생성된 작업 묶음 |
| **WorkerTask** | 개별 워커에 배정된 단위 작업 |
| **AgentDef** | Teams 모드에서 `--agents`에 전달되는 에이전트 정의 |

---

## 2. 두 가지 오케스트레이션 모드

### Admin 모드 vs Teams 모드 비교

| | Admin 모드 | Teams 모드 (Beta) |
|---|---|---|
| **CLI 호출** | Phase별 여러 번 (분석, 분해, 워커×N, 종합) | 단일 호출 (`--agents` 플래그) |
| **작업 분배** | GUI가 JSON 파싱 후 goroutine으로 직접 디스패치 | Claude CLI가 Task 도구로 자율 위임 |
| **워커 세션** | 각 워커 탭이 독립 `--session-id` 보유 | 메인 세션 내부의 서브프로세스 |
| **세션 유지** | 워커별 `--resume` 가능 | 메인 탭 세션만 유지 |
| **결과 종합** | Phase 4에서 별도 Claude 호출 | Claude가 서브에이전트 결과를 직접 종합 |
| **진행 추적** | task-started/completed/failed 이벤트 | tool-activity 이벤트에서 Task 도구 감지 |
| **취소** | 워커별 개별 취소 가능 | 메인 세션 취소 |
| **활성화** | `ToggleTabAdminMode()` | `ToggleTabTeamsMode()` |
| **상태 모델** | `OrchestratorState` | `TeamsState` |

### 어떤 모드를 선택해야 하나?

- **Admin 모드**: 각 워커의 진행 상황을 탭별로 확인하고 싶을 때, 워커별 세션 히스토리가 필요할 때
- **Teams 모드**: 간단한 멀티에이전트 위임, Claude가 자율적으로 작업을 분배하도록 맡기고 싶을 때

---

## 3. Admin 모드 - 4단계 파이프라인

### Phase 1a - 코드베이스 분석

Admin 탭의 Claude 세션이 코드베이스를 탐색하여 사용자 요청에 필요한 파일과 패턴을 파악합니다.

```go
// app.go:854-864
analysisPrompt := fmt.Sprintf(`사용자 요청을 실행하기 위해 코드베이스를 분석해주세요.
사용자 요청: %s

다음을 수행하세요:
1. 관련 파일을 찾아 읽으세요 (Glob, Grep, Read 도구 사용)
2. 기존 코드 패턴과 컨벤션을 파악하세요
3. 수정이 필요한 파일과 위치를 정리하세요
4. 여러 파일이 동시에 수정될 때의 충돌 가능성을 확인하세요`, message)
```

- `maxTurns: 20`으로 충분한 도구 사용 허용
- 실시간 스트리밍으로 분석 과정을 Admin 탭에 표시
- `tool-activity`, `token-usage` 이벤트 브로드캐스트

### Phase 1b - 작업 분해

분석 결과를 기반으로 워커별 태스크를 JSON으로 분해합니다.

```go
// app.go:906-932
// Claude에게 워커 탭 목록을 전달하고 JSON 형식으로 작업 분해를 요청
// maxTurns: 1 (추가 도구 사용 없이 즉시 JSON 응답)
```

반환 JSON 구조 (`DecompositionResult`):

```json
{
  "analysis": "분석 요약 텍스트",
  "tasks": [
    {
      "workerTabId": "conversation-2",
      "prompt": "app/models/user.go 파일에 Validate() 메서드를 추가하세요...",
      "description": "사용자 모델 유효성 검증 추가"
    },
    {
      "workerTabId": "conversation-3",
      "prompt": "app/handlers/user_handler.go에서 검증 로직을 추가...",
      "description": "핸들러에 유효성 검증 통합"
    }
  ]
}
```

프롬프트 규칙:
- 사용 가능한 워커 탭 ID만 사용
- 각 prompt에 수정 파일 경로, 기존 코드 패턴, 구현 지침, 의존성 주의사항 포함
- JSON 파싱 실패 시 에러 메시지를 Admin 탭에 표시하고 종료

### Phase 2 - 병렬 디스패치

각 태스크를 해당 워커 탭에 **goroutine으로 병렬 전송**합니다.

```go
// app.go:1032-1119
var wg sync.WaitGroup

for i := range job.Tasks {
    task := &job.Tasks[i]
    wg.Add(1)
    go func(t *models.WorkerTask) {
        defer wg.Done()
        err := a.SendMessage(t.WorkerTabID, t.Prompt, nil)
        // 완료 후 워커 탭의 마지막 assistant 메시지를 Result로 수집
    }(task)
}
```

- 각 워커는 독립된 Claude CLI 세션(`--session-id`/`--resume`)에서 작업
- WebSocket으로 `task-started` → `task-completed`/`task-failed` 이벤트 브로드캐스트
- 각 워커 탭의 스트리밍 UI도 독립적으로 업데이트

### Phase 3 - 결과 수집

```go
// app.go:1122-1150
wg.Wait() // 모든 워커 완료 대기

for _, t := range job.Tasks {
    if t.Status == models.TaskCompleted {
        resultSummary.WriteString(fmt.Sprintf(
            "=== Worker: %s ===\nTask: %s\nResult:\n%s\n\n",
            t.WorkerTabID, t.Description, t.Result))
    }
}
```

### Phase 4 - 종합 보고

수집된 결과를 Claude에게 전달하여 종합 보고서를 생성합니다.

```go
// app.go:1159-1170
synthesisPrompt := fmt.Sprintf(`Original user request: %s

Worker results:
%s

Please synthesize all the results into a comprehensive response.
- Highlight key findings from each worker
- Note any failures or issues
- Provide an overall summary and recommendations
- Use Korean for the response`, message, resultSummary.String())
```

종합 메시지의 Metadata에 성공/실패 카운트와 소요 시간이 기록됩니다:

```go
Metadata: map[string]string{
    "type":    "orchestration",
    "phase":   "synthesis",
    "success": "3",
    "failed":  "0",
    "synthMs": "2500",
}
```

---

## 4. Teams 모드 - CLI 네이티브 에이전트

Teams 모드는 Claude CLI의 `--agents` 플래그를 사용하여 **단일 CLI 호출**로 멀티에이전트 오케스트레이션을 수행합니다.

### 에이전트 정의 생성

연결된 워커 탭 정보로부터 에이전트 JSON을 생성합니다.

```go
// app/claude/agents.go:10-17
type AgentDef struct {
    Description string   `json:"description"`
    Prompt      string   `json:"prompt"`
    Tools       []string `json:"tools,omitempty"`
    Model       string   `json:"model,omitempty"`
    MaxTurns    int      `json:"maxTurns,omitempty"`
}
```

```go
// app/claude/agents.go:28-49
func BuildAgentsJSON(workerTabs []WorkerTabInfo) (string, error) {
    agents := make(map[string]AgentDef)
    for _, tab := range workerTabs {
        agentName := sanitizeAgentName(tab.Name)
        agents[agentName] = AgentDef{
            Description: fmt.Sprintf("%s 디렉토리의 코드 작업 담당.", tab.WorkDir),
            Prompt:      fmt.Sprintf("당신은 %s에서 작업하는 개발자입니다. 작업 디렉토리: %s.", tab.Name, tab.WorkDir),
            Tools:       []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep"},
        }
    }
    data, _ := json.Marshal(agents)
    return string(data), nil
}
```

생성되는 JSON 예시 (`--agents` 플래그에 전달):

```json
{
  "대화-2": {
    "description": "/Users/me/project 디렉토리의 코드 작업 담당.",
    "prompt": "당신은 대화 2에서 작업하는 개발자입니다. 작업 디렉토리: /Users/me/project.",
    "tools": ["Read", "Write", "Edit", "Bash", "Glob", "Grep"]
  },
  "대화-3": {
    "description": "/Users/me/project 디렉토리의 코드 작업 담당.",
    "prompt": "당신은 대화 3에서 작업하는 개발자입니다. 작업 디렉토리: /Users/me/project.",
    "tools": ["Read", "Write", "Edit", "Bash", "Glob", "Grep"]
  }
}
```

### CLI 호출 흐름

```go
// app.go:1478-1504
// 단일 CLI 호출 — Claude가 자율적으로 Task 도구를 통해 에이전트에 위임
response, _, _, err := a.claude.SendMessage(
    ctx, teamTabID, message, files, teamTab.WorkDir,
    0,              // maxTurns: unlimited
    systemPrompt,
    "bypassPermissions",
    "",             // tools: 제한 없음
    agentsJSON,     // --agents 플래그에 전달
    onChunk, onToolActivity, onUsage,
)
```

실제 CLI 명령:

```bash
claude --print --output-format stream-json --verbose \
  --session-id <UUID> \
  --permission-mode bypassPermissions \
  --max-turns 999 \
  --append-system-prompt "You are a team orchestrator..." \
  --agents '{"에이전트명": {...}}' \
  -- "사용자 메시지"
```

### 에이전트 활동 라우팅

Teams 모드에서는 `tool-activity` 콜백에서 Task 도구 사용을 감지하여 워커 탭에 매핑합니다:

```go
// app.go:1488-1499
func(toolName, detail string) {
    a.wsServer.SendToolActivity(teamTabID, toolName, detail)

    if toolName == "Task" {
        workerTabID := matchAgentToTab(detail, agentMapping)
        if workerTabID != "" {
            a.wsServer.SendOrchestratorEvent(websocket.OrchestratorMessage{
                Type:        "task-started",
                AdminTabID:  teamTabID,
                WorkerTabID: workerTabID,
                Content:     detail,
            })
        }
    }
}
```

`matchAgentToTab()`은 detail 문자열에서 에이전트 이름을 찾아 워커 탭 ID로 변환합니다:

```go
// app.go:1555-1563
func matchAgentToTab(detail string, agentMapping map[string]string) string {
    detailLower := strings.ToLower(detail)
    for agentName, tabID := range agentMapping {
        if strings.Contains(detailLower, strings.ToLower(agentName)) {
            return tabID
        }
    }
    return ""
}
```

---

## 5. 아키텍처

### 전체 흐름도

```
┌─────────────────────────────────────────────────────────────┐
│                      Frontend (Svelte)                       │
│                                                              │
│  ┌─────────────┐   ┌──────────┐   ┌──────────┐              │
│  │ Admin/Team  │   │Worker Tab│   │Worker Tab│              │
│  │    Tab      │   │   #1     │   │   #2     │              │
│  └──────┬──────┘   └────▲─────┘   └────▲─────┘              │
│         │               │              │                     │
│     orchestratorStore   │    WebSocket (port 9876)           │
│     streaming.js        │                                    │
└─────────┼───────────────┼──────────────┼─────────────────────┘
          │               │              │
┌─────────▼───────────────┼──────────────┼─────────────────────┐
│                     Backend (Go/Wails)                        │
│                                                               │
│  ┌─ Admin 모드 ─────────────────────────────────────┐        │
│  │  SendAdminMessage()                               │        │
│  │    ├─ Phase 1a: 코드베이스 분석 (maxTurns=20)    │        │
│  │    ├─ Phase 1b: 작업 분해 → JSON (maxTurns=1)    │        │
│  │    ├─ Phase 2:  goroutine 병렬 디스패치           │        │
│  │    │    ├─ SendMessage(worker1) ────► goroutine   │        │
│  │    │    └─ SendMessage(worker2) ────► goroutine   │        │
│  │    ├─ Phase 3:  wg.Wait() 결과 수집              │        │
│  │    └─ Phase 4:  종합 보고 (maxTurns=1)           │        │
│  └───────────────────────────────────────────────────┘        │
│                                                               │
│  ┌─ Teams 모드 ─────────────────────────────────────┐        │
│  │  SendTeamsMessage()                               │        │
│  │    ├─ BuildAgentsJSON() → 에이전트 정의 생성      │        │
│  │    └─ 단일 CLI 호출: --agents JSON                │        │
│  │         Claude가 자율적으로 Task 도구로 위임      │        │
│  └───────────────────────────────────────────────────┘        │
│                                                               │
│  Claude CLI: claude --print --session-id/--resume UUID        │
└───────────────────────────────────────────────────────────────┘
```

### 핵심 파일 구조

```
app.go                                  # 오케스트레이션 메인 로직
├── ToggleTabAdminMode()                #   Admin 모드 전환        (668-706)
├── ConnectWorkerTab()                  #   Admin 워커 연결        (722-747)
├── DisconnectWorkerTab()               #   Admin 워커 해제        (750-774)
├── SendAdminMessage()                  #   4단계 파이프라인 실행  (787-1232)
├── CancelOrchestrationJob()            #   작업 취소              (1235-1262)
├── ToggleTabTeamsMode()                #   Teams 모드 전환        (1265-1309)
├── ConnectTeamsWorker()                #   Teams 워커 연결        (1311-1336)
├── DisconnectTeamsWorker()             #   Teams 워커 해제        (1338-1363)
├── SendTeamsMessage()                  #   단일 CLI 호출 실행     (1367-1551)
├── matchAgentToTab()                   #   에이전트→탭 매핑       (1555-1563)
└── sanitizeAgentNameForMapping()       #   에이전트명 정제        (1566-1574)

app/claude/
├── agents.go                           # AgentDef, BuildAgentsJSON, sanitizeAgentName
├── cli_wrapper.go                      # SendMessage() — agents 파라미터 → --agents 플래그
├── conversation.go                     # Service.SendMessage() 래퍼
└── service.go                          # Service 인터페이스

app/models/
├── orchestration.go                    # TaskStatus, WorkerTask, OrchestrationJob, OrchestratorState
└── tab.go                              # TabState (AdminMode, TeamsMode, Orchestrator, TeamsState)

app/websocket/server.go                 # OrchestratorMessage, SendOrchestratorEvent()

frontend/src/lib/
├── stores/orchestrator.js              # orchestratorStore (Admin + Teams 공유)
├── stores/streaming.js                 # WebSocket 라우팅 (오케스트레이터 이벤트 분기)
├── components/ConversationTab.svelte   # Admin/Teams 토글, 메시지 전송 분기
└── components/SplitPane.svelte         # 워커 연결 UI (Admin/Teams 각각)
```

---

## 6. 사용 방법

### 6.1 공통: 탭 준비

오케스트레이션을 사용하려면 탭을 **최소 2개 이상** 생성합니다 (관리 탭 1 + 워커 N).

### 6.2 Admin 모드

**활성화:**

```javascript
// frontend: ConversationTab.svelte
await ToggleTabAdminMode(tabId, true);
```

활성화 시 동작:
- 다른 모든 탭의 Admin 모드 자동 해제 (동시에 1개만)
- 해당 탭의 Teams 모드가 켜져 있으면 자동 해제 (상호 배타)
- 나머지 탭이 자동으로 워커로 연결됨
- `OrchestratorState` 초기화

**워커 탭 관리:**

```javascript
// 워커 연결/해제 — SplitPane.svelte 워커 칩 클릭으로 토글
await ConnectWorkerTab(adminTabID, workerTabID);
await DisconnectWorkerTab(adminTabID, workerTabID);
```

**작업 요청:**

Admin 탭에서 메시지를 보내면 `SendAdminMessage()`가 자동 호출됩니다:

```javascript
// ConversationTab.svelte:655-658
if (adminMode) {
    await SendAdminMessage(tabId, messageToSend, filesToSend);
}
```

**작업 취소:**

```javascript
// ConversationTab.svelte:689
await CancelOrchestrationJob(tabId);
```

### 6.3 Teams 모드 (Beta)

**활성화:**

```javascript
// frontend: ConversationTab.svelte
await ToggleTabTeamsMode(tabId, true);
```

활성화 시 동작:
- 다른 모든 탭의 Teams 모드 자동 해제 (동시에 1개만)
- 해당 탭의 Admin 모드가 켜져 있으면 자동 해제 (상호 배타)
- 나머지 탭이 자동으로 워커로 연결됨
- `TeamsState` 초기화 (`AgentMapping` 빈 맵, `IsRunning: false`)

**워커 탭 관리:**

```javascript
// SplitPane.svelte에서 Teams 전용 워커 칩 클릭
await ConnectTeamsWorker(teamTabID, workerTabID);
await DisconnectTeamsWorker(teamTabID, workerTabID);
```

**작업 요청:**

Teams 탭에서 메시지를 보내면 `SendTeamsMessage()`가 자동 호출됩니다:

```javascript
// ConversationTab.svelte:650-653
if (teamsMode) {
    await SendTeamsMessage(tabId, messageToSend, filesToSend);
}
```

**취소:**

Teams 모드의 취소는 Admin과 같은 `CancelOrchestrationJob()`을 사용합니다 (동일한 `orchestrationCancel` 맵 공유).

---

## 7. 백엔드 API 레퍼런스

### Admin 모드 API

| 함수 | 시그니처 | 위치 |
|------|---------|------|
| `ToggleTabAdminMode` | `(tabID string, enabled bool) error` | `app.go:670` |
| `ConnectWorkerTab` | `(adminTabID, workerTabID string) error` | `app.go:723` |
| `DisconnectWorkerTab` | `(adminTabID, workerTabID string) error` | `app.go:751` |
| `SendAdminMessage` | `(adminTabID, message string, files []string) error` | `app.go:788` |
| `CancelOrchestrationJob` | `(adminTabID string) error` | `app.go:1236` |

### Teams 모드 API

| 함수 | 시그니처 | 위치 |
|------|---------|------|
| `ToggleTabTeamsMode` | `(tabID string, enabled bool) error` | `app.go:1267` |
| `ConnectTeamsWorker` | `(teamTabID, workerTabID string) error` | `app.go:1312` |
| `DisconnectTeamsWorker` | `(teamTabID, workerTabID string) error` | `app.go:1339` |
| `SendTeamsMessage` | `(teamTabID, message string, files []string) error` | `app.go:1367` |

### Claude 에이전트 API

| 함수 | 시그니처 | 위치 |
|------|---------|------|
| `BuildAgentsJSON` | `(workerTabs []WorkerTabInfo) (string, error)` | `app/claude/agents.go:28` |
| `sanitizeAgentName` | `(name string) string` | `app/claude/agents.go:54` |

### CLI Wrapper 변경사항

`SendMessage()`에 `agents string` 파라미터가 추가되었습니다:

```go
// app/claude/cli_wrapper.go:92
func (w *CLIWrapper) SendMessage(
    ctx context.Context,
    conversationID, message string,
    files []string,
    model string,
    workDir string,
    maxTurns int,
    systemPrompt string,
    permissionMode string,
    tools string,
    agents string,       // ← 신규: --agents 플래그용 JSON
    onChunk func(string),
    onToolActivity func(string, string),
    onUsage func(int, int),
) (string, int, *AskUserQuestionData, error)
```

비어있지 않으면 CLI에 `--agents <JSON>` 인자가 추가됩니다:

```go
// app/claude/cli_wrapper.go:144-147
if agents != "" {
    args = append(args, "--agents", agents)
}
```

---

## 8. 프론트엔드 연동

### orchestratorStore (`frontend/src/lib/stores/orchestrator.js`)

Admin 모드와 Teams 모드 모두 동일한 store를 공유합니다.

```javascript
// Store 구조
{
  [adminTabId]: {
    tasks: {
      [taskId]: {
        taskId: string,
        workerTabId: string,
        description: string,
        status: 'running' | 'completed' | 'failed',
        content: string
      }
    },
    jobComplete: boolean
  }
}
```

### streamingStore의 이벤트 라우팅

```javascript
// frontend/src/lib/stores/streaming.js:36-40
if (msg.adminTabId && ['task-started', 'task-completed', 'task-failed', 'job-completed'].includes(msg.type)) {
    orchestratorStore.handleEvent(msg);
    return;
}
```

### ConversationTab.svelte의 메시지 전송 분기

```javascript
// frontend/src/lib/components/ConversationTab.svelte:650-658
if (teamsMode) {
    await SendTeamsMessage(tabId, messageToSend, filesToSend);
} else if (adminMode) {
    await SendAdminMessage(tabId, messageToSend, filesToSend);
} else {
    await SendMessage(tabId, messageToSend, filesToSend);
}
```

취소 분기:

```javascript
// frontend/src/lib/components/ConversationTab.svelte:686-691
if (teamsMode) {
    await CancelOrchestrationJob(tabId);
} else if (adminMode) {
    await CancelOrchestrationJob(tabId);
} else {
    await CancelMessage(tabId);
}
```

### 모드 토글 (상호 배타)

```javascript
// ConversationTab.svelte:859-881
async function toggleAdminMode() {
    await ToggleTabAdminMode(tabId, adminMode);
    if (adminMode) teamsMode = false; // 상호 배타
}

async function toggleTeamsMode() {
    await ToggleTabTeamsMode(tabId, teamsMode);
    if (teamsMode) adminMode = false; // 상호 배타
}
```

### 워커 상태 대시보드

```svelte
<!-- ConversationTab.svelte:1667-1669 -->
{#if (adminMode || teamsMode) && orchestratorState && Object.keys(orchestratorState.tasks).length > 0}
  <div class="orchestration-dashboard">
    <div class="dashboard-title">
      {teamsMode ? 'Teams 에이전트 상태' : '워커 상태'}
    </div>
    <!-- 각 태스크 상태 표시 -->
  </div>
{/if}
```

### SplitPane.svelte 워커 연결 UI

Admin 모드와 Teams 모드가 별도 섹션으로 렌더링됩니다:

```svelte
<!-- Admin 모드 워커 연결 -->
{#if tab.adminMode && tab.orchestrator}
  <div class="worker-connections">
    {#each tabs.filter(t => !t.adminMode && t.id !== tab.id) as wTab}
      <!-- 워커 칩: 클릭으로 ConnectWorkerTab/DisconnectWorkerTab -->
    {/each}
  </div>
{/if}

<!-- Teams 모드 워커 연결 -->
{#if tab.teamsMode && tab.teamsState}
  <div class="worker-connections teams">
    <span class="teams-badge">Teams</span> 에이전트:
    {#each tabs.filter(t => !t.teamsMode && t.id !== tab.id) as wTab}
      <!-- 워커 칩: 클릭으로 ConnectTeamsWorker/DisconnectTeamsWorker -->
    {/each}
  </div>
{/if}
```

---

## 9. 데이터 모델

### TabState (`app/models/tab.go`)

```go
type TabState struct {
    ID             string             `json:"id"`
    Name           string             `json:"name"`
    Messages       []Message          `json:"messages"`
    ConversationID string             `json:"conversationId"`
    IsActive       bool               `json:"isActive"`
    AdminMode      bool               `json:"adminMode"`
    PlanMode       bool               `json:"planMode"`
    TeamsMode      bool               `json:"teamsMode"`                // Beta
    Orchestrator   *OrchestratorState `json:"orchestrator,omitempty"`   // Admin 전용
    TeamsState     *TeamsState        `json:"teamsState,omitempty"`     // Teams 전용
    WorkDir        string             `json:"workDir"`
    ContextFiles   []string           `json:"contextFiles"`
}
```

### TeamsState (`app/models/tab.go:20-24`)

```go
type TeamsState struct {
    ConnectedTabs []string          `json:"connectedTabs"`          // 연결된 워커 탭 ID
    AgentMapping  map[string]string `json:"agentMapping,omitempty"` // agentName → workerTabID
    IsRunning     bool              `json:"isRunning"`              // 실행 중 여부
}
```

### OrchestratorState (`app/models/orchestration.go:42-46`)

```go
type OrchestratorState struct {
    ConnectedTabs []string           `json:"connectedTabs"`
    CurrentJob    *OrchestrationJob  `json:"currentJob,omitempty"`
    JobHistory    []OrchestrationJob `json:"jobHistory"`
}
```

### OrchestrationJob (`app/models/orchestration.go:32-39`)

```go
type OrchestrationJob struct {
    ID              string       `json:"id"`              // "job-{UnixNano}"
    AdminTabID      string       `json:"adminTabId"`
    UserRequest     string       `json:"userRequest"`
    Tasks           []WorkerTask `json:"tasks"`
    Status          TaskStatus   `json:"status"`
    SynthesisResult string       `json:"synthesisResult,omitempty"`
}
```

### WorkerTask (`app/models/orchestration.go:17-29`)

```go
type WorkerTask struct {
    ID          string     `json:"id"`
    WorkerTabID string     `json:"workerTabId"`
    AdminTabID  string     `json:"adminTabId"`
    Description string     `json:"description"`
    Prompt      string     `json:"prompt"`
    Status      TaskStatus `json:"status"`
    Result      string     `json:"result"`
    Error       string     `json:"error,omitempty"`
    StartedAt   time.Time  `json:"startedAt,omitempty"`
    CompletedAt time.Time  `json:"completedAt,omitempty"`
    DurationMs  int64      `json:"durationMs,omitempty"`
}
```

### TaskStatus (`app/models/orchestration.go:6-14`)

```go
const (
    TaskPending   TaskStatus = "pending"
    TaskRunning   TaskStatus = "running"
    TaskCompleted TaskStatus = "completed"
    TaskFailed    TaskStatus = "failed"
    TaskCancelled TaskStatus = "cancelled"
)
```

### AgentDef (`app/claude/agents.go:11-17`)

```go
type AgentDef struct {
    Description string   `json:"description"`
    Prompt      string   `json:"prompt"`
    Tools       []string `json:"tools,omitempty"`
    Model       string   `json:"model,omitempty"`
    MaxTurns    int      `json:"maxTurns,omitempty"`
}
```

### WorkerTabInfo (`app/claude/agents.go:20-24`)

```go
type WorkerTabInfo struct {
    ID      string
    Name    string
    WorkDir string
}
```

---

## 10. WebSocket 이벤트

### 오케스트레이터 이벤트 (`app/websocket/server.go:240-258`)

Admin 모드와 Teams 모드 모두 동일한 이벤트 구조를 사용합니다.

```go
type OrchestratorMessage struct {
    Type        string `json:"type"`
    AdminTabID  string `json:"adminTabId"`
    TaskID      string `json:"taskId,omitempty"`
    WorkerTabID string `json:"workerTabId,omitempty"`
    Content     string `json:"content,omitempty"`
    Status      string `json:"status,omitempty"`
}
```

### 이벤트 타입별 발생 시점

| 이벤트 | Admin 모드 | Teams 모드 |
|--------|-----------|-----------|
| `task-started` | Phase 2 워커 디스패치 시작 | `tool-activity`에서 Task 도구 감지 시 |
| `task-completed` | 워커 `SendMessage()` 성공 | (자동 발생하지 않음) |
| `task-failed` | 워커 `SendMessage()` 실패 | (자동 발생하지 않음) |
| `job-completed` | Phase 4 종합 보고 완료 | `SendTeamsMessage()` 완료 시 |

### 스트리밍 이벤트

오케스트레이션 중에도 각 단계에서 스트리밍 이벤트가 발생합니다:

| 이벤트 | 설명 |
|--------|------|
| `start` | 스트리밍 시작 |
| `chunk` | 응답 청크 수신 |
| `end` | 스트리밍 종료 |
| `tool-activity` | 도구 사용 감지 (toolName, detail) |
| `token-usage` | 토큰 사용량 업데이트 (inputTokens, outputTokens) |

---

## 11. 활용 시나리오

### 시나리오 1: Admin 모드 - 대규모 리팩토링

```
사용자 요청: "모든 API 핸들러에 에러 핸들링 미들웨어를 추가해주세요"

Phase 1a: Claude가 프로젝트의 핸들러 구조 분석 (maxTurns=20)
Phase 1b: 파일별로 작업 분해 → JSON
  - Worker (conversation-2): user_handler.go 수정
  - Worker (conversation-3): product_handler.go 수정
  - Worker (conversation-4): middleware.go 생성
Phase 2: 3개 워커 goroutine 병렬 실행
Phase 3: 결과 수집 (wg.Wait)
Phase 4: 변경사항 종합 보고
```

### 시나리오 2: Admin 모드 - 멀티모듈 기능 추가

```
사용자 요청: "사용자 프로필 이미지 업로드 기능을 추가해주세요"

Phase 1a: 기존 파일 업로드 패턴, 모델 구조 분석
Phase 1b: 작업 분해
  - Worker 1: 백엔드 API 엔드포인트 + 스토리지 로직
  - Worker 2: 프론트엔드 업로드 컴포넌트
  - Worker 3: 데이터베이스 마이그레이션 + 모델 수정
Phase 2: 3개 워커 병렬 실행
Phase 4: 통합 테스트 계획 + 변경사항 요약
```

### 시나리오 3: Teams 모드 - 빠른 멀티 작업

```
사용자 요청: "README.md를 한국어로 번역하고, 테스트 커버리지 보고서도 만들어줘"

CLI 호출: claude --agents '{"번역-에이전트":{...}, "테스트-에이전트":{...}}' -- "..."
Claude가 자율적으로:
  - 번역 에이전트에게 README 번역 위임
  - 테스트 에이전트에게 커버리지 분석 위임
  - 두 결과를 종합하여 응답
```

### 시나리오 4: Teams 모드 - 코드 리뷰

```
사용자 요청: "최근 변경사항에 대해 보안, 성능, 코드 스타일 관점에서 리뷰해줘"

Claude가 자율적으로 3개 에이전트에 위임:
  - 보안 리뷰 에이전트
  - 성능 리뷰 에이전트
  - 코드 스타일 리뷰 에이전트
단일 응답으로 종합 리뷰 반환
```

---

## 12. 제한사항 및 주의사항

### 공통 제약

- 최대 **6개** 탭 (관리 1 + 워커 최대 5)
- 워커가 0개이면 오케스트레이션 실행 불가
- Admin과 Teams 모드는 **상호 배타** (동일 탭에서 동시 활성화 불가)
- 오케스트레이션 상태(`Orchestrator`, `TeamsState`)는 앱 재시작 시 복원되지 않음 (ephemeral)

### Admin 모드 제약

- **Admin 탭은 동시에 1개만** 활성화 가능
- 각 워커 탭은 한 번에 1개의 태스크만 처리
- 여러 워커가 **같은 파일을 동시에 수정**하면 충돌 가능 → 파일 단위로 워커 분리 권장
- 각 워커 탭은 독립 세션이므로 워커 간 컨텍스트 공유 없음
- Phase 1b JSON 파싱 실패 시 에러 메시지 표시 후 종료
- 개별 워커 실패는 다른 워커에 영향 없음
- Phase 4 종합 실패 시 원본 결과를 폴백으로 표시
- 취소 시 이미 완료된 워커의 변경사항은 **롤백되지 않음**

### Teams 모드 제약

- **Teams 탭은 동시에 1개만** 활성화 가능
- Claude CLI의 `--agents` 플래그 지원이 필요 (최신 버전)
- 에이전트 이름은 탭 이름에서 자동 생성 (알파벳, 한글, 숫자, 하이픈만 허용)
- `task-completed`/`task-failed` 이벤트가 자동 발생하지 않음 (Teams는 `task-started`만 감지)
- 워커 탭의 메시지 히스토리에는 반영되지 않음 (단일 세션 내부 처리)
- `matchAgentToTab()`은 detail 문자열 내 에이전트 이름 포함 여부로 매칭 → 유사한 이름 주의

### 상태 영속성

- `~/.claude-gui/state.json`에 탭, 설정, 세션 매핑 저장
- `Orchestrator`와 `TeamsState`는 ephemeral (재시작 시 `nil`로 초기화)
- Admin 모드의 `JobHistory`는 세션 중에만 유지
- `TeamsState.AgentMapping`은 `SendTeamsMessage()` 호출 시마다 재생성

---

## 부록: 코드 위치 빠른 참조

| 기능 | 파일 | 라인 |
|------|------|------|
| Admin 모드 토글 | `app.go` | 670-706 |
| Admin 워커 연결/해제 | `app.go` | 722-774 |
| Admin 오케스트레이션 로직 | `app.go` | 787-1232 |
| 오케스트레이션 취소 | `app.go` | 1235-1262 |
| Teams 모드 토글 | `app.go` | 1267-1309 |
| Teams 워커 연결/해제 | `app.go` | 1311-1363 |
| Teams 오케스트레이션 로직 | `app.go` | 1367-1551 |
| 에이전트→탭 매핑 | `app.go` | 1555-1574 |
| 에이전트 정의 (AgentDef) | `app/claude/agents.go` | 1-63 |
| CLI --agents 플래그 처리 | `app/claude/cli_wrapper.go` | 144-147 |
| 데이터 모델 (오케스트레이션) | `app/models/orchestration.go` | 1-46 |
| 데이터 모델 (탭/Teams) | `app/models/tab.go` | 1-24 |
| WebSocket 오케스트레이터 이벤트 | `app/websocket/server.go` | 239-258 |
| 프론트엔드 오케스트레이터 스토어 | `frontend/src/lib/stores/orchestrator.js` | 1-82 |
| 스트리밍 이벤트 라우팅 | `frontend/src/lib/stores/streaming.js` | 36-40 |
| 메시지 전송 분기 (Admin/Teams/일반) | `frontend/src/lib/components/ConversationTab.svelte` | 650-658 |
| 모드 토글 UI | `frontend/src/lib/components/ConversationTab.svelte` | 1719-1728 |
| 워커 연결 UI | `frontend/src/lib/components/SplitPane.svelte` | 861-893 |
