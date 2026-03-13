package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// APIClient handles communication with Anthropic API
type APIClient struct {
	baseURL    string
	apiKey     string
	authToken  string
	httpClient *http.Client
}

// NewAPIClient creates a new Anthropic API client
func NewAPIClient() *APIClient {
	// Read from environment variables
	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}

	authToken := ""

	// Try environment variable first (highest priority)
	authToken = os.Getenv("CLAUDE_SESSION_KEY")
	if authToken != "" {
		fmt.Println("[APIClient] Using CLAUDE_SESSION_KEY from environment")
		return &APIClient{
			baseURL:    baseURL,
			authToken:  authToken,
			httpClient: &http.Client{},
		}
	}

	// Try to get Claude session key from config file
	sessionKey, err := GetClaudeSessionKey()
	if err == nil && sessionKey != "" {
		authToken = sessionKey
		fmt.Println("[APIClient] Using Claude session key from config")
		return &APIClient{
			baseURL:    baseURL,
			authToken:  authToken,
			httpClient: &http.Client{},
		}
	}

	// Try ANTHROPIC_API_KEY (standard SDK env var)
	authToken = os.Getenv("ANTHROPIC_API_KEY")
	if authToken != "" {
		fmt.Println("[APIClient] Using ANTHROPIC_API_KEY from environment")
		return &APIClient{
			baseURL:    baseURL,
			authToken:  authToken,
			httpClient: &http.Client{},
		}
	}

	// Fallback to ANTHROPIC_AUTH_TOKEN
	authToken = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	if authToken != "" {
		fmt.Println("[APIClient] Using ANTHROPIC_AUTH_TOKEN from environment")
	} else {
		// Check if using Enterprise plan proxy - if so, use proxy-managed mode
		if baseURL != "" && baseURL != "https://api.anthropic.com" {
			authToken = "proxy-managed"
			fmt.Println("[APIClient] Using proxy-managed authentication")
		} else {
			fmt.Println("[APIClient] WARNING: No authentication token found")
			fmt.Println("[APIClient] Please set CLAUDE_SESSION_KEY environment variable")
			fmt.Println("[APIClient] Or run 'claude login' to authenticate")
		}
	}

	return &APIClient{
		baseURL:    baseURL,
		authToken:  authToken,
		httpClient: &http.Client{},
	}
}

// MessageContent represents content that can be text or image
type MessageContent struct {
	Type   string              `json:"type"`
	Text   string              `json:"text,omitempty"`
	Source *ImageSource        `json:"source,omitempty"`
}

// ImageSource represents an image attachment
type ImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// Message represents a single message in the conversation
type Message struct {
	Role    string           `json:"role"`
	Content interface{}      `json:"content"` // Can be string or []MessageContent
}

// CreateMessageRequest is the request body for creating a message
type CreateMessageRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	MaxTokens int       `json:"max_tokens"`
	Stream    bool      `json:"stream"`
}

// ContentBlock represents a content block in a Claude message (text or tool_use)
type ContentBlock struct {
	Type  string          `json:"type"`            // "text" | "tool_use"
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`    // tool_use block ID
	Name  string          `json:"name,omitempty"`  // tool name, e.g. "AskUserQuestion"
	Input json.RawMessage `json:"input,omitempty"` // tool input (raw JSON)
}

// CreateMessageResponse is the response from creating a message
type CreateMessageResponse struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
	Model   string         `json:"model"`
}

// StreamEvent represents a streaming event
type StreamEvent struct {
	Type         string       `json:"type"`
	Index        int          `json:"index,omitempty"`
	ContentBlock ContentBlock `json:"content_block,omitempty"`
	Delta        struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta,omitempty"`
}

// CreateMessage sends a message to Claude API and returns the response
func (c *APIClient) CreateMessage(ctx context.Context, messages []Message, stream bool, model string, onChunk func(string)) (string, error) {
	fmt.Printf("[APIClient] Creating message with %d messages, stream=%v, model=%s\n", len(messages), stream, model)

	if model == "" {
		model = "claude-sonnet-4-20250514"
	}

	requestBody := CreateMessageRequest{
		Model:     model,
		Messages:  messages,
		MaxTokens: 8096,
		Stream:    stream,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/messages", c.baseURL)
	fmt.Printf("[APIClient] Sending request to: %s\n", url)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	// Use auth token if available, otherwise expect proxy to handle auth
	if c.authToken != "" {
		req.Header.Set("x-api-key", c.authToken)
		fmt.Println("[APIClient] Using auth token")
	} else {
		fmt.Println("[APIClient] No auth token, relying on proxy")
	}

	fmt.Println("[APIClient] Sending HTTP request...")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		fmt.Printf("[APIClient] ERROR: HTTP request failed: %v\n", err)
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("[APIClient] Response status: %d\n", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("[APIClient] ERROR: API error response: %s\n", string(body))
		return "", fmt.Errorf("API request failed with status %d: %s", resp.StatusCode, string(body))
	}

	if stream {
		fmt.Println("[APIClient] Handling streaming response...")
		return c.handleStreamingResponse(resp.Body, onChunk)
	} else {
		fmt.Println("[APIClient] Handling non-streaming response...")
		return c.handleNonStreamingResponse(resp.Body)
	}
}

// handleStreamingResponse handles SSE streaming response
func (c *APIClient) handleStreamingResponse(body io.ReadCloser, onChunk func(string)) (string, error) {
	var fullResponse strings.Builder
	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		line := scanner.Text()

		// SSE format: "data: {...}"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// Check for stream end
		if data == "[DONE]" {
			break
		}

		var event StreamEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		// Handle content_block_delta events
		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			text := event.Delta.Text
			fullResponse.WriteString(text)
			// Send accumulated content (not just delta) for proper UI rendering
			if onChunk != nil {
				onChunk(fullResponse.String())
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading stream: %w", err)
	}

	return fullResponse.String(), nil
}

// handleNonStreamingResponse handles regular JSON response
func (c *APIClient) handleNonStreamingResponse(body io.ReadCloser) (string, error) {
	var response CreateMessageResponse
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	if len(response.Content) == 0 {
		return "", fmt.Errorf("empty response content")
	}

	return response.Content[0].Text, nil
}

// IsInitialized checks if the API client has valid authentication
func (c *APIClient) IsInitialized() bool {
	return c.authToken != "" && c.authToken != "proxy-managed"
}
