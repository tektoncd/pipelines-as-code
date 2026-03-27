// Package openai is the Client implementation for OpenAI LLM integration.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/ltypes"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/providers"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
	defaultModel   = "gpt-4"

	// Default configuration values.
	defaultTimeoutSeconds = 30
	defaultMaxTokens      = 1000
)

// Config holds the configuration for OpenAI client.
type Config struct {
	APIKey         string
	BaseURL        string
	Model          string
	TimeoutSeconds int
	MaxTokens      int
	HTTPClient     *http.Client
}

// Client implements the LLM interface for OpenAI.
type Client struct {
	config     *Config
	httpClient *http.Client
}

// NewClient creates a new OpenAI client.
func NewClient(config *Config) (*Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config is required")
	}

	if config.APIKey == "" {
		return nil, fmt.Errorf("API key is required")
	}

	commonCfg := &providers.CommonConfig{
		APIKey:         config.APIKey,
		TimeoutSeconds: config.TimeoutSeconds,
		MaxTokens:      config.MaxTokens,
	}
	if err := providers.ApplyDefaults(commonCfg); err != nil {
		return nil, err
	}

	config.TimeoutSeconds = commonCfg.TimeoutSeconds
	config.MaxTokens = commonCfg.MaxTokens

	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	if config.Model == "" {
		config.Model = defaultModel
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	if config.TimeoutSeconds > 0 && httpClient.Timeout == 0 {
		httpClient.Timeout = time.Duration(config.TimeoutSeconds) * time.Second
	}

	client := &Client{
		config:     config,
		httpClient: httpClient,
	}

	return client, nil
}

// Analyze sends an analysis request to OpenAI and returns the response.
func (c *Client) Analyze(ctx context.Context, request *ltypes.AnalysisRequest) (*ltypes.AnalysisResponse, error) {
	startTime := time.Now()

	fullPrompt, err := providers.BuildPrompt(request)
	if err != nil {
		return nil, &ltypes.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "prompt_build_error",
			Message:   fmt.Sprintf("failed to build prompt: %v", err),
			Retryable: false,
		}
	}

	apiRequest := &openaiRequest{
		Model:               c.config.Model,
		MaxCompletionTokens: request.MaxTokens,
		Messages: []openaiMessage{
			{
				Role:    "user",
				Content: fullPrompt,
			},
		},
	}

	requestBody, err := json.Marshal(apiRequest)
	if err != nil {
		return nil, &ltypes.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "request_marshal_error",
			Message:   fmt.Sprintf("failed to marshal request: %v", err),
			Retryable: false,
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/chat/completions", bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, &ltypes.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "http_request_error",
			Message:   fmt.Sprintf("failed to create HTTP request: %v", err),
			Retryable: false,
		}
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, &ltypes.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "http_error",
			Message:   fmt.Sprintf("HTTP request failed: %v", err),
			Retryable: true,
		}
	}
	defer resp.Body.Close()

	var apiResponse openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return nil, &ltypes.AnalysisError{
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
		case resp.StatusCode == http.StatusUnauthorized:
			errorType = "invalid_api_key"
			retryable = false
		case resp.StatusCode >= 500:
			errorType = "server_error"
			retryable = true
		}

		errorMsg := fmt.Sprintf("OpenAI API error (status %d)", resp.StatusCode)
		if apiResponse.Error != nil {
			errorMsg = fmt.Sprintf("OpenAI API error: %s", apiResponse.Error.Message)
		}

		return nil, &ltypes.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      errorType,
			Message:   errorMsg,
			Retryable: retryable,
		}
	}

	if len(apiResponse.Choices) == 0 {
		return nil, &ltypes.AnalysisError{
			Provider:  c.GetProviderName(),
			Type:      "empty_response",
			Message:   "no choices in API response",
			Retryable: false,
		}
	}

	content := apiResponse.Choices[0].Message.Content
	tokensUsed := apiResponse.Usage.TotalTokens

	response := &ltypes.AnalysisResponse{
		Content:    content,
		TokensUsed: tokensUsed,
		Provider:   c.GetProviderName(),
		Timestamp:  time.Now(),
		Duration:   time.Since(startTime),
	}

	return response, nil
}

// GetProviderName returns the provider name.
func (c *Client) GetProviderName() string {
	return string(ltypes.LLMProviderOpenAI)
}

// ValidateConfig validates the client configuration.
func (c *Client) ValidateConfig() error {
	commonCfg := &providers.CommonConfig{
		APIKey:         c.config.APIKey,
		TimeoutSeconds: c.config.TimeoutSeconds,
		MaxTokens:      c.config.MaxTokens,
	}
	if err := providers.ValidateCommonConfig(commonCfg); err != nil {
		return err
	}

	if err := providers.ValidateBaseURL(c.config.BaseURL); err != nil {
		return err
	}

	return nil
}

// OpenAI API request/response structures

type openaiRequest struct {
	Model               string          `json:"model"`
	Messages            []openaiMessage `json:"messages"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openaiChoice `json:"choices"`
	Usage   openaiUsage    `json:"usage"`
	Error   *openaiError   `json:"error,omitempty"`
}

type openaiChoice struct {
	Index        int           `json:"index"`
	Message      openaiMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openaiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}
