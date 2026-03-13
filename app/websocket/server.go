package websocket

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for local app
	},
}

// StreamMessage represents a streaming message
type StreamMessage struct {
	Type    string `json:"type"`    // "start", "chunk", "end", "error"
	TabID   string `json:"tabId"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Server handles WebSocket connections for streaming
type Server struct {
	clients map[*websocket.Conn]bool
	mu      sync.RWMutex
	writeMu sync.Mutex // serializes all writes to prevent concurrent write panic
	port    int
}

// NewServer creates a new WebSocket server
func NewServer(port int) *Server {
	return &Server{
		clients: make(map[*websocket.Conn]bool),
		port:    port,
	}
}

// Start starts the WebSocket server
func (s *Server) Start() error {
	http.HandleFunc("/ws", s.handleConnection)

	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	fmt.Printf("[WebSocket] Starting server on %s\n", addr)

	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			fmt.Printf("[WebSocket] Server error: %v\n", err)
		}
	}()

	return nil
}

// handleConnection handles new WebSocket connections
func (s *Server) handleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("[WebSocket] Upgrade error: %v\n", err)
		return
	}

	s.mu.Lock()
	s.clients[conn] = true
	s.mu.Unlock()

	fmt.Printf("[WebSocket] Client connected (total: %d)\n", len(s.clients))

	// Keep connection alive and handle disconnection
	go func() {
		defer func() {
			s.mu.Lock()
			delete(s.clients, conn)
			s.mu.Unlock()
			conn.Close()
			fmt.Printf("[WebSocket] Client disconnected (total: %d)\n", len(s.clients))
		}()

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				break
			}
		}
	}()
}

// broadcast sends raw bytes to all connected clients (must hold writeMu)
func (s *Server) broadcast(data []byte) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()

	for conn := range s.clients {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			fmt.Printf("[WebSocket] Write error: %v\n", err)
		}
	}
}

// Broadcast sends a message to all connected clients
func (s *Server) Broadcast(msg StreamMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Printf("[WebSocket] Marshal error: %v\n", err)
		return
	}

	s.broadcast(data)
}

// SendStreamStart sends a streaming start message
func (s *Server) SendStreamStart(tabID string) {
	s.Broadcast(StreamMessage{
		Type:  "start",
		TabID: tabID,
	})
}

// SendStreamChunk sends a streaming chunk
func (s *Server) SendStreamChunk(tabID, content string) {
	fmt.Printf("[WebSocket] Sending chunk at %d (length=%d)\n", time.Now().UnixMilli(), len(content))
	s.Broadcast(StreamMessage{
		Type:    "chunk",
		TabID:   tabID,
		Content: content,
	})
}

// SendStreamEnd sends a streaming end message
func (s *Server) SendStreamEnd(tabID string) {
	s.Broadcast(StreamMessage{
		Type:  "end",
		TabID: tabID,
	})
}

// SendStreamError sends a streaming error message
func (s *Server) SendStreamError(tabID, error string) {
	s.Broadcast(StreamMessage{
		Type:  "error",
		TabID: tabID,
		Error: error,
	})
}

// ToolActivityMessage represents a tool activity event sent during streaming
type ToolActivityMessage struct {
	Type     string `json:"type"` // "tool-activity"
	TabID    string `json:"tabId"`
	ToolName string `json:"toolName"`
	Detail   string `json:"detail"`
}

// SendToolActivity broadcasts a tool activity event to all connected clients
func (s *Server) SendToolActivity(tabID, toolName, detail string) {
	msg := ToolActivityMessage{
		Type:     "tool-activity",
		TabID:    tabID,
		ToolName: toolName,
		Detail:   detail,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Printf("[WebSocket] ToolActivity marshal error: %v\n", err)
		return
	}
	s.broadcast(data)
}

// TokenUsageMessage represents a token usage update sent during streaming
type TokenUsageMessage struct {
	Type         string `json:"type"` // "token-usage"
	TabID        string `json:"tabId"`
	InputTokens  int    `json:"inputTokens"`
	OutputTokens int    `json:"outputTokens"`
}

// SendTokenUsage broadcasts a token usage update to all connected clients
func (s *Server) SendTokenUsage(tabID string, input, output int) {
	msg := TokenUsageMessage{
		Type:         "token-usage",
		TabID:        tabID,
		InputTokens:  input,
		OutputTokens: output,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Printf("[WebSocket] TokenUsage marshal error: %v\n", err)
		return
	}
	s.broadcast(data)
}

// AskUserQuestionMessage represents an AskUserQuestion event sent to the frontend
type AskUserQuestionMessage struct {
	Type      string               `json:"type"`      // "ask-user-question"
	TabID     string               `json:"tabId"`
	ToolUseID string               `json:"toolUseId"`
	Questions []AskUserQuestionItem `json:"questions"`
}

// AskUserQuestionItem represents a single question (mirrors claude.AskQuestion for JSON serialization)
type AskUserQuestionItem struct {
	Question    string                    `json:"question"`
	Header      string                    `json:"header"`
	Options     []AskUserQuestionOption   `json:"options"`
	MultiSelect bool                      `json:"multiSelect"`
}

// AskUserQuestionOption represents a selectable option
type AskUserQuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// SendAskUserQuestion broadcasts an AskUserQuestion event to all connected clients
func (s *Server) SendAskUserQuestion(tabID, toolUseID string, questions []AskUserQuestionItem) {
	msg := AskUserQuestionMessage{
		Type:      "ask-user-question",
		TabID:     tabID,
		ToolUseID: toolUseID,
		Questions: questions,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		fmt.Printf("[WebSocket] AskUserQuestion marshal error: %v\n", err)
		return
	}
	s.broadcast(data)
}

// OrchestratorMessage represents an orchestration event message
type OrchestratorMessage struct {
	Type        string `json:"type"`                  // task-started, task-completed, task-failed, job-completed
	AdminTabID  string `json:"adminTabId"`
	TaskID      string `json:"taskId,omitempty"`
	WorkerTabID string `json:"workerTabId,omitempty"`
	Content     string `json:"content,omitempty"`
	Status      string `json:"status,omitempty"`
}

// SendOrchestratorEvent broadcasts an orchestrator event to all clients
func (s *Server) SendOrchestratorEvent(event OrchestratorMessage) {
	data, err := json.Marshal(event)
	if err != nil {
		fmt.Printf("[WebSocket] OrchestratorEvent marshal error: %v\n", err)
		return
	}

	s.broadcast(data)
}

// GetPort returns the server port
func (s *Server) GetPort() int {
	return s.port
}
