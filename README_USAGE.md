# Claude GUI Tool - 사용 가이드

## 아키텍처 개요

Wails v2 기반의 데스크톱 애플리케이션으로, Go 백엔드 + Svelte 프론트엔드 구조.
Claude CLI(`claude` 명령어)를 래핑하여 GUI에서 에이전틱 코딩을 수행한다.

> **API 모드는 사용하지 않는다.** 모든 Claude 호출은 CLI 래퍼를 통해 이루어진다.

---

## 프로젝트 구조

```
awesomeProject1/
├── app.go                        # 앱 서비스 (탭 관리, 메시지 전송, UI 이벤트)
├── main.go                       # Wails 엔트리포인트
├── app/
│   ├── claude/
│   │   ├── cli_wrapper.go        # Claude CLI 프로세스 관리, 세션 지속성
│   │   ├── conversation.go       # 대화 컨텍스트 파싱, 멀티모달 콘텐츠
│   │   ├── service.go            # 서비스 인터페이스 (CLI 래퍼 위임)
│   │   └── api_client.go         # (미사용) Anthropic API 클라이언트
│   ├── models/                   # 데이터 모델 (TabState, Message 등)
│   ├── utils/                    # 파일 처리, base64, MIME 타입
│   └── websocket/                # 실시간 스트리밍용 WebSocket 서버
├── frontend/                     # Svelte 프론트엔드
│   └── src/lib/components/
│       ├── DynamicSplitLayout.svelte
│       ├── SplitPane.svelte
│       └── ConversationTab.svelte
└── wails.json
```

---

## 핵심 메커니즘: CLI 세션 지속성

### 문제 (이전 구현)

매 메시지마다 새 프로세스 + 새 세션을 생성하고, `--no-session-persistence` 플래그를 사용했기 때문에:
- Claude가 이전 도구 사용 결과(파일 읽기, 코드 수정 등)를 전혀 기억하지 못함
- CLAUDE.md, 프로젝트 컨텍스트가 세션 간 유지되지 않음
- 대화 이력을 `"User: ...\nAssistant: ..."` 평문으로 매번 재구성하여 전달

### 해결 (현재 구현)

`--no-session-persistence` 제거 + `--resume SESSION_ID`로 세션 이어가기.

```
첫 메시지:  claude --print --session-id UUID ...  (새 세션 생성)
이후 메시지: claude --print --resume UUID ...      (기존 세션 이어가기)
```

Claude CLI가 내부적으로 전체 도구 호출 히스토리를 유지하므로, **새 메시지만 전달하면 된다.**

---

## 파일별 상세 스펙

### 1. `app/claude/cli_wrapper.go` — CLI 프로세스 관리

#### 구조체

```go
type CLIWrapper struct {
    sessionIDs     map[string]string // conversationID -> UUID
    sessionStarted map[string]bool   // conversationID -> 첫 메시지 전송 완료 여부
    mu             sync.RWMutex
}
```

- `sessionIDs`: 대화별 고정 세션 UUID. `getOrCreateSessionID()`로 최초 1회 생성, 이후 재사용.
- `sessionStarted`: 해당 세션으로 첫 메시지가 성공적으로 전송되었는지 추적.

#### `SendMessage()` 핵심 흐름

1. `getOrCreateSessionID(conversationID)`로 세션 UUID 획득 (기존 있으면 재사용)
2. `IsSessionStarted(conversationID)`로 resume 여부 판단
3. 인자 구성:
   - 첫 메시지: `--session-id UUID`
   - 이후 메시지: `--resume UUID`
   - `--no-session-persistence` **없음**
4. `claude` 프로세스 실행, stdout으로 `stream-json` 파싱
5. 성공 완료 시 `markSessionStarted(conversationID)` 호출

#### CLI 인자 구성

```
claude --print
       --output-format stream-json
       --verbose
       [--session-id UUID | --resume UUID]
       --permission-mode bypassPermissions
       --model <model>
       [--max-turns N]
       [--append-system-prompt "..."]
       [--tools "Read,Glob,Grep,..."]
       -- <message>
```

#### `--resume` 실패 시 에러 복구

`cmd.Wait()` 에러 + `fullResponse`가 빈 문자열 + `isResume == true`이면:
1. `ClearSession(conversationID)` 호출 (세션 초기화)
2. `SendMessage()` 재귀 호출 (새 세션으로 재시도)

```go
if fullResponse == "" && isResume {
    w.ClearSession(conversationID)
    return w.SendMessage(ctx, conversationID, message, ...)
}
```

#### 응답 파싱 (`stream-json`)

stdout에서 한 줄씩 읽으며 JSON 파싱:

| `type` | 동작 |
|--------|------|
| `"user"` | `turnCount++` (에이전틱 턴 카운트) |
| `"result"` | `resp.Result`를 최종 응답으로 저장, `onChunk` 콜백 호출 |
| `"assistant"` | `resp.Message.Content[].Text`를 응답으로 저장, `onChunk` 콜백 호출 |

#### 프로세스 관리

- **프로세스 그룹**: `Setpgid: true`로 자식 프로세스 포함 관리
- **킬**: `syscall.Kill(-pgid, SIGKILL)`로 전체 그룹 종료
- **스톨 타임아웃**: 10분간 출력 없으면 프로세스 강제 종료
- **컨텍스트 취소**: `ctx.Done()` 감지 시 프로세스 그룹 종료

#### 헬퍼 메서드

| 메서드 | 역할 |
|--------|------|
| `getOrCreateSessionID(id)` | UUID 생성 or 기존 반환 |
| `IsSessionStarted(id)` | 첫 메시지 전송 완료 여부 (RLock) |
| `markSessionStarted(id)` | 전송 완료 기록 (Lock) |
| `ClearSession(id)` | sessionIDs + sessionStarted 모두 삭제 |

---

### 2. `app/claude/service.go` — 서비스 인터페이스

```go
type Service struct {
    cliWrapper *CLIWrapper
    apiClient  *APIClient  // 미사용
    useAPI     bool        // 항상 false
    model      string
}
```

#### 주요 메서드

| 메서드 | 역할 |
|--------|------|
| `Initialize(headless)` | CLI 존재 확인 (`/opt/homebrew/bin/claude` 또는 `/usr/local/bin/claude`) |
| `SendMessage(...)` | `cliWrapper.SendMessage()` 위임 |
| `IsSessionStarted(id)` | `cliWrapper.IsSessionStarted()` 위임 |
| `CreateNewChat(id)` | `cliWrapper.ClearSession()` 호출 → 세션 리셋 |
| `ClosePage(id)` | `cliWrapper.ClearSession()` 호출 |
| `SetModel(model)` / `GetModel()` | 모델 설정/조회 |

---

### 3. `app.go` — 앱 서비스 (프론트엔드 바인딩)

#### `App` 구조체

```go
type App struct {
    ctx         context.Context
    tabs        map[string]*models.TabState  // tabID -> 탭 상태
    settings    *models.GlobalSettings
    claude      *claude.Service
    wsServer    *websocket.Server            // 포트 9876
    model       string
    cancelFuncs map[string]context.CancelFunc
    cancelMu    sync.Mutex
}
```

#### 허용 모델

```go
var allowedModels = []string{
    "claude-sonnet-4-20250514",
    "claude-haiku-4-20250414",
    "claude-opus-4-20250514",
}
```

#### `SendMessage(tabID, message, files)` — 메시지 전송

1. 탭에 user 메시지 추가 → `user-message-added` 이벤트 발행
2. **메시지 구성** (CLI 전용):
   - 기본: 새 메시지만 전달 (`messageToSend = message`)
   - 세션 리셋 후 이전 대화가 있는 경우: 이전 컨텍스트 요약을 첫 메시지에 포함
3. 시스템 프롬프트 구성:
   - 기본: GUI 환경 안내 (인터랙티브 질문 불가, 명확히 물어보라는 지시)
   - Plan 모드: 읽기 전용 도구만 허용 (`Read,Glob,Grep,WebSearch,WebFetch`)
4. WebSocket으로 스트리밍 청크 전달
5. 응답을 assistant 메시지로 탭에 추가

#### 세션 리셋 후 컨텍스트 복구

`TruncateMessages` 등으로 세션이 리셋되었지만 이전 대화가 남아있는 경우:

```go
if !a.claude.IsSessionStarted(tabID) && len(tab.Messages) > 1 {
    // 이전 대화를 요약하여 첫 메시지에 포함
    messageToSend = "Previous conversation context:\n" +
        strings.Join(contextParts, "\n\n") +
        "\n\n---\nNew message:\n" + message
}
```

- Assistant 응답은 500자로 truncate하여 토큰 절약

#### 세션이 리셋되는 시점

| 메서드 | 트리거 | 동작 |
|--------|--------|------|
| `ClearConversation(tabID)` | `/clear` 명령 | 메시지 초기화 + `CreateNewChat()` |
| `TruncateMessages(tabID, fromIndex)` | 메시지 재시도 | 메시지 잘라내기 + `CreateNewChat()` |
| `SetWorkDir(tabID, dir)` | 작업 디렉토리 변경 | 디렉토리 변경 + `CreateNewChat()` |
| `RemoveTab(tabID)` | 탭 닫기 | `ClosePage()` → `ClearSession()` |
| `AddNewTab()` | 새 탭 생성 | `CreateNewChat()` |

#### Plan 모드

```go
if tab.PlanMode {
    systemPrompt += "[PLAN MODE] 읽기 전용..."
    tools = "Read,Glob,Grep,WebSearch,WebFetch"  // 쓰기 도구 차단
    maxTurns = 50
}
```

- Plan 모드에서는 코드 수정 도구가 물리적으로 차단됨
- 최대 50턴으로 충분한 코드베이스 분석 허용

#### 기타 기능

| 기능 | 메서드 |
|------|--------|
| 탭 관리 (최대 6개) | `AddNewTab()`, `RemoveTab()`, `RenameTab()` |
| 모델 변경 | `SetModel(name)`, `GetCurrentModel()`, `GetAvailableModels()` |
| 메시지 취소 | `CancelMessage(tabID)` — context cancel → 프로세스 그룹 kill |
| 파일 검색 | `SearchFiles(baseDir, query)` — camelCase/abbreviation 매칭 |
| 경로 자동완성 | `CompletePath(partial)` — 탭 키 자동완성 |
| 파일 첨부 | `SelectFiles()`, `SaveDroppedImage()`, `SaveDroppedFile()` |
| 확대 보기 | `OpenExpandedView(content)` — 마크다운을 브라우저에서 열기 |
| 토큰 사용량 추정 | `GetUsageInfo(tabID)` — ~3 chars/token 기준 |

---

## 세션 라이프사이클

```
[새 탭 / 앱 시작]
    │
    ▼
첫 메시지 ──→ claude --session-id UUID ...
    │              성공 → markSessionStarted = true
    ▼
이후 메시지 ──→ claude --resume UUID ...
    │              세션 내부에서 전체 히스토리 유지
    ▼
  (반복)
    │
    ├── /clear ──────────→ ClearSession() → 다음 메시지는 새 --session-id
    ├── 메시지 재시도 ───→ ClearSession() + 이전 컨텍스트 요약 포함
    ├── WorkDir 변경 ───→ ClearSession()
    ├── 탭 닫기 ────────→ ClearSession()
    └── --resume 실패 ──→ ClearSession() + 자동 재시도 (새 세션)
```

---

## 스트리밍 아키텍처

```
[Claude CLI process]
    │ stdout (stream-json, line by line)
    ▼
[CLIWrapper.SendMessage]
    │ onChunk callback
    ▼
[App.SendMessage]
    │ WebSocket + Wails event
    ▼
[Frontend (Svelte)]
    │ 실시간 마크다운 렌더링
    ▼
[사용자]
```

- WebSocket 서버: 포트 9876 (실시간 전달)
- Wails 이벤트: `streaming-start`, `streaming-end`, `tabs-updated`, `user-message-added` (백업)

---

## 실행

### 개발 모드

```bash
wails dev
```

### 프로덕션 빌드

```bash
wails build
```

빌드된 실행 파일은 `build/bin/` 디렉토리에 생성됩니다.

---

## 의존성

**Go 백엔드:**
- Wails v2
- Go 1.21+
- `github.com/google/uuid`

**프론트엔드:**
- Svelte 4
- Vite

**런타임:**
- Claude CLI (`claude` 명령어) — `/opt/homebrew/bin/claude` 또는 `/usr/local/bin/claude`
