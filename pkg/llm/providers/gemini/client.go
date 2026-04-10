// Package gemini is the Client implementation for Google Gemini LLM integration.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm"
)

const (
	defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	defaultModel   = "gemini-3.1-flash-lite-preview"
)

func init() {
	llm.RegisterProvider(llm.ProviderGemini, newClient)
}

// Client implements the LLM interface for Google Gemini.
type Client struct {
	config     *llm.ProviderConfig
	httpClient *http.Client
}

func newClient(cfg *llm.ProviderConfig) (llm.Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	c := &llm.ProviderConfig{
		APIKey:         cfg.APIKey,
		BaseURL:        cfg.BaseURL,
		Model:          cfg.Model,
		TimeoutSeconds: cfg.TimeoutSeconds,
		MaxTokens:      cfg.MaxTokens,
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = llm.DefaultTimeoutSeconds
	}
	if c.MaxTokens == 0 {
		c.MaxTokens = llm.DefaultMaxTokens
	}
	if c.BaseURL == "" {
		c.BaseURL = defaultBaseURL
	}
	if c.Model == "" {
		c.Model = defaultModel
	}

	return &Client{
		config: c,
		httpClient: &http.Client{
			Timeout: time.Duration(c.TimeoutSeconds) * time.Second,
		},
	}, nil
}

// Analyze sends an analysis request to Gemini and returns the response.
func (c *Client) Analyze(ctx context.Context, request *llm.AnalysisRequest) (*llm.AnalysisResponse, error) {
	startTime := time.Now()

	fullPrompt, err := llm.BuildPrompt(request)
	if err != nil {
		return nil, &llm.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "prompt_build_error",
			Message:   fmt.Sprintf("failed to build prompt: %v", err),
			Retryable: false,
		}
	}

	apiRequest := &geminiRequest{
		Contents: []geminiContent{
			{
				Parts: []geminiPart{
					{Text: fullPrompt},
				},
			},
		},
		GenerationConfig: &geminiGenerationConfig{
			MaxOutputTokens: request.MaxTokens,
		},
	}

	requestBody, err := json.Marshal(apiRequest)
	if err != nil {
		return nil, &llm.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "request_marshal_error",
			Message:   fmt.Sprintf("failed to marshal request: %v", err),
			Retryable: false,
		}
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", c.config.BaseURL, c.config.Model, c.config.APIKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, &llm.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "http_request_error",
			Message:   fmt.Sprintf("failed to create HTTP request: %v", err),
			Retryable: false,
		}
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, &llm.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "http_error",
			Message:   fmt.Sprintf("HTTP request failed: %v", err),
			Retryable: true,
		}
	}
	defer resp.Body.Close()

	var apiResponse geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, &llm.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "response_parse_error",
			Message:   fmt.Sprintf("failed to parse response: %v", err),
			Retryable: false,
		}
	}

	if resp.StatusCode != http.StatusOK {
		errorType := "api_error"
		retryable := false

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			errorType = "rate_limit_exceeded"
			retryable = true
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			errorType = "invalid_api_key"
			retryable = false
		case resp.StatusCode >= 500:
			errorType = "server_error"
			retryable = true
		}

		errorMsg := fmt.Sprintf("Gemini API error (status %d)", resp.StatusCode)
		if apiResponse.Error != nil {
			errorMsg = fmt.Sprintf("Gemini API error: %s", apiResponse.Error.Message)
		}

		return nil, &llm.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      errorType,
			Message:   errorMsg,
			Retryable: retryable,
		}
	}

	if len(apiResponse.Candidates) == 0 {
		return nil, &llm.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "empty_response",
			Message:   "no candidates in API response",
			Retryable: false,
		}
	}

	candidate := apiResponse.Candidates[0]
	if len(candidate.Content.Parts) == 0 {
		return nil, &llm.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "empty_response",
			Message:   "no content parts in API response",
			Retryable: false,
		}
	}

	content := candidate.Content.Parts[0].Text
	tokensUsed := len(strings.Fields(content + fullPrompt))

	return &llm.AnalysisResponse{
		Content:    content,
		TokensUsed: tokensUsed,
		Provider:   c.GetProviderName(),
		Timestamp:  time.Now(),
		Duration:   time.Since(startTime),
	}, nil
}

// GetProviderName returns the provider name.
func (c *Client) GetProviderName() string {
	return string(llm.ProviderGemini)
}

// ValidateConfig validates the client configuration.
func (c *Client) ValidateConfig() error {
	if c.config.APIKey == "" {
		return fmt.Errorf("API key is required")
	}
	if c.config.TimeoutSeconds < 0 {
		return fmt.Errorf("timeout seconds must be non-negative")
	}
	if c.config.MaxTokens < 0 {
		return fmt.Errorf("max tokens must be non-negative")
	}
	if c.config.BaseURL == "" {
		return fmt.Errorf("base URL is required")
	}
	return llm.ValidateURL(c.config.BaseURL)
}

// Gemini API request/response structures.

type geminiRequest struct {
	Contents         []geminiContent         `json:"contents"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type geminiResponse struct {
	Candidates []geminiCandidate `json:"candidates"`
	Error      *geminiError      `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content       geminiContent `json:"content"`
	FinishReason  string        `json:"finishReason"`
	SafetyRatings []any         `json:"safetyRatings"`
}

type geminiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}
