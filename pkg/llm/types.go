package llm

import (
	"context"
	"time"
)

const (
	// DefaultTimeoutSeconds is the default timeout for LLM API calls.
	DefaultTimeoutSeconds = 30

	// DefaultMaxTokens is the default maximum tokens for LLM responses.
	DefaultMaxTokens = 1000
)

// Client defines the interface for LLM providers.
type Client interface {
	Analyze(ctx context.Context, request *AnalysisRequest) (*AnalysisResponse, error)
	GetProviderName() string
	ValidateConfig() error
}

// AnalysisRequest represents a request to analyze CI/CD pipeline data.
type AnalysisRequest struct {
	Prompt         string                 `json:"prompt"`
	Context        map[string]interface{} `json:"context"`
	MaxTokens      int                    `json:"max_tokens"`
	TimeoutSeconds int                    `json:"timeout_seconds"`
}

// AnalysisResponse represents the response from an LLM analysis.
type AnalysisResponse struct {
	Content    string        `json:"content"`
	TokensUsed int           `json:"tokens_used"`
	Provider   string        `json:"provider"`
	Timestamp  time.Time     `json:"timestamp"`
	Duration   time.Duration `json:"duration"`
}

// AnalysisError represents an error from LLM analysis.
type AnalysisError struct {
	Provider  string `json:"provider"`
	Type      string `json:"type"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *AnalysisError) Error() string {
	return e.Message
}

// AIProvider represents a supported LLM provider.
type AIProvider string

const (
	ProviderOpenAI AIProvider = "openai"
	ProviderGemini AIProvider = "gemini"
)
