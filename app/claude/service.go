package claude

import (
	"fmt"
	"os"
)

// Service is the main Claude.ai service interface
type Service struct {
	cliWrapper *CLIWrapper
	apiClient  *APIClient
	useAPI     bool // true = use Anthropic API (real streaming), false = use CLI wrapper
	model      string
}

// NewService creates a new Claude service
func NewService() *Service {
	return &Service{
		cliWrapper: NewCLIWrapper(),
		apiClient:  NewAPIClient(),
		useAPI:     false, // Default to CLI, will switch to API if available
		model:      "claude-sonnet-4-20250514",
	}
}

// SetModel sets the model to use for API requests
func (s *Service) SetModel(model string) {
	s.model = model
	fmt.Printf("[Service] Model set to: %s\n", model)
}

// GetModel returns the current model name
func (s *Service) GetModel() string {
	return s.model
}

// Initialize initializes the Claude service
func (s *Service) Initialize(headless bool) error {
	// First, try to initialize API client (preferred for real streaming)
	if s.apiClient.IsInitialized() {
		s.useAPI = true
		fmt.Println("Claude API service initialized (real-time streaming enabled)")
		return nil
	}

	// Check environment for API key
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey != "" {
		s.useAPI = true
		fmt.Println("Claude API service initialized via ANTHROPIC_API_KEY")
		return nil
	}

	// Fallback to CLI wrapper
	_, err := os.Stat("/opt/homebrew/bin/claude")
	if err != nil {
		_, err = os.Stat("/usr/local/bin/claude")
		if err != nil {
			return fmt.Errorf("neither Anthropic API key nor Claude CLI found")
		}
	}

	s.useAPI = false
	fmt.Println("Claude CLI service initialized (no real-time streaming)")
	fmt.Println("Set ANTHROPIC_API_KEY for real-time streaming support")

	return nil
}

// Close closes the service and cleans up resources
func (s *Service) Close() error {
	return s.cliWrapper.Close()
}

// IsInitialized checks if the service is initialized
func (s *Service) IsInitialized() bool {
	return s.apiClient.IsInitialized() || s.cliWrapper != nil
}

// UseAPI returns whether the service is using API (true) or CLI (false)
func (s *Service) UseAPI() bool {
	return s.useAPI
}

// IsSessionStarted returns whether the first message has been sent for a CLI session
func (s *Service) IsSessionStarted(conversationID string) bool {
	return s.cliWrapper.IsSessionStarted(conversationID)
}
