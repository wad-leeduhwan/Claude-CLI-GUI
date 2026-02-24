package claude

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"claude-gui/app/utils"
)

// buildMultimodalContent creates content blocks with text and images for API.
// Images are sent as image content blocks; code/text files are inlined as text blocks.
func buildMultimodalContent(textContent string, files []string) interface{} {
	if len(files) == 0 {
		return textContent
	}

	var contentBlocks []MessageContent
	contentBlocks = append(contentBlocks, MessageContent{Type: "text", Text: textContent})

	for _, filePath := range files {
		if utils.IsImageFile(filePath) {
			// Image file → base64 image content block
			data, err := utils.ReadFileAsBase64(filePath)
			if err != nil {
				fmt.Printf("[Service] Failed to read image file %s: %v\n", filePath, err)
				continue
			}
			mimeType := utils.GetMimeType(filePath)
			contentBlocks = append(contentBlocks, MessageContent{
				Type: "image",
				Source: &ImageSource{
					Type:      "base64",
					MediaType: mimeType,
					Data:      data,
				},
			})
		} else {
			// Code/text file → inline text content block
			data, err := os.ReadFile(filePath)
			if err != nil {
				fmt.Printf("[Service] Failed to read code file %s: %v\n", filePath, err)
				continue
			}
			lang := utils.GetLanguageFromExt(filePath)
			fileName := filePath[strings.LastIndex(filePath, "/")+1:]
			var block string
			if lang != "" {
				block = fmt.Sprintf("```%s\n// %s\n%s\n```", lang, fileName, string(data))
			} else {
				block = fmt.Sprintf("```\n// %s\n%s\n```", fileName, string(data))
			}
			contentBlocks = append(contentBlocks, MessageContent{
				Type: "text",
				Text: block,
			})
		}
	}

	return contentBlocks
}

// ConversationManager manages conversation contexts for each tab
type ConversationManager struct {
	conversations map[string][]Message
	mu            sync.RWMutex
}

// NewConversationManager creates a new conversation manager
func NewConversationManager() *ConversationManager {
	return &ConversationManager{
		conversations: make(map[string][]Message),
	}
}

// AddMessage adds a message to a conversation (simple text)
func (cm *ConversationManager) AddMessage(conversationID string, role string, content string) {
	cm.AddMessageWithContent(conversationID, role, content)
}

// AddMessageWithContent adds a message with any content type (text or content blocks)
func (cm *ConversationManager) AddMessageWithContent(conversationID string, role string, content interface{}) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, exists := cm.conversations[conversationID]; !exists {
		cm.conversations[conversationID] = []Message{}
	}

	cm.conversations[conversationID] = append(cm.conversations[conversationID], Message{
		Role:    role,
		Content: content,
	})
}

// GetMessages returns all messages for a conversation
func (cm *ConversationManager) GetMessages(conversationID string) []Message {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	messages, exists := cm.conversations[conversationID]
	if !exists {
		return []Message{}
	}

	// Return a copy to avoid concurrent modification
	result := make([]Message, len(messages))
	copy(result, messages)
	return result
}

// ClearConversation clears all messages for a conversation
func (cm *ConversationManager) ClearConversation(conversationID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	delete(cm.conversations, conversationID)
}

// SendMessage sends a message to Claude and returns the response and turn count.
// Turn count tracks how many agentic turns (tool-use cycles) were used. Returns 0 for API mode.
func (s *Service) SendMessage(ctx context.Context, conversationID, message string, files []string, workDir string, maxTurns int, systemPrompt string, permissionMode string, tools string, onChunk func(string)) (string, int, error) {
	// Validate file sizes (10MB max per file)
	for _, filePath := range files {
		if err := utils.ValidateFile(filePath, 10); err != nil {
			return "", 0, fmt.Errorf("file validation failed: %w", err)
		}
	}

	// Use API for real streaming if available
	if s.useAPI && s.apiClient.IsInitialized() {
		fmt.Printf("[Service] Sending message via Anthropic API (conversation: %s)\n", conversationID)

		// Build messages array from conversation context
		messages := parseConversationContext(message)

		// Attach image files to the last user message as multimodal content
		if len(files) > 0 && len(messages) > 0 {
			lastIdx := len(messages) - 1
			if messages[lastIdx].Role == "user" {
				if textContent, ok := messages[lastIdx].Content.(string); ok {
					messages[lastIdx].Content = buildMultimodalContent(textContent, files)
					fmt.Printf("[Service] Attached %d file(s) to the last user message\n", len(files))
				}
			}
		}

		response, err := s.apiClient.CreateMessage(ctx, messages, true, s.GetModel(), onChunk)
		if err != nil {
			return "", 0, fmt.Errorf("failed to get response from Anthropic API: %w", err)
		}

		fmt.Printf("[Service] Response received from Anthropic API (length: %d)\n", len(response))
		return response, 0, nil
	}

	// Fallback to CLI wrapper (no real-time streaming)
	fmt.Printf("[Service] Sending message via Claude CLI (conversation: %s, model: %s, maxTurns: %d)\n", conversationID, s.GetModel(), maxTurns)

	response, turnCount, err := s.cliWrapper.SendMessage(ctx, conversationID, message, files, s.GetModel(), workDir, maxTurns, systemPrompt, permissionMode, tools, onChunk)
	if err != nil {
		return "", turnCount, fmt.Errorf("failed to get response from Claude: %w", err)
	}

	fmt.Printf("[Service] Response received from Claude CLI (length: %d, turns: %d)\n", len(response), turnCount)
	return response, turnCount, nil
}

// parseConversationContext parses the conversation context string into API messages
func parseConversationContext(context string) []Message {
	var messages []Message

	// Split by double newlines to separate messages
	parts := splitConversation(context)

	for _, part := range parts {
		part = trimSpace(part)
		if part == "" {
			continue
		}

		if hasPrefix(part, "User: ") {
			content := trimPrefix(part, "User: ")
			messages = append(messages, Message{
				Role:    "user",
				Content: content,
			})
		} else if hasPrefix(part, "Assistant: ") {
			content := trimPrefix(part, "Assistant: ")
			messages = append(messages, Message{
				Role:    "assistant",
				Content: content,
			})
		}
	}

	// If no structured messages found, treat entire context as user message
	if len(messages) == 0 {
		messages = append(messages, Message{
			Role:    "user",
			Content: context,
		})
	}

	return messages
}

// Helper functions to avoid importing strings package conflicts
func splitConversation(s string) []string {
	var result []string
	current := ""
	lines := splitLines(s)

	for i, line := range lines {
		if line == "" && current != "" {
			result = append(result, current)
			current = ""
		} else {
			if current != "" {
				current += "\n"
			}
			current += line
		}
		// Don't forget the last part
		if i == len(lines)-1 && current != "" {
			result = append(result, current)
		}
	}

	return result
}

func splitLines(s string) []string {
	var result []string
	current := ""
	for _, c := range s {
		if c == '\n' {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func trimSpace(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func trimPrefix(s, prefix string) string {
	if hasPrefix(s, prefix) {
		return s[len(prefix):]
	}
	return s
}

// GetResponse is deprecated - use SendMessage which returns response directly
func (s *Service) GetResponse(conversationID string) (string, error) {
	return "", fmt.Errorf("GetResponse is deprecated with CLI wrapper")
}

// CreateNewChat creates a new conversation (clears session)
func (s *Service) CreateNewChat(conversationID string) error {
	s.cliWrapper.ClearSession(conversationID)
	return nil
}

// ClosePage clears conversation session when a tab is closed
func (s *Service) ClosePage(conversationID string) error {
	s.cliWrapper.ClearSession(conversationID)
	return nil
}
