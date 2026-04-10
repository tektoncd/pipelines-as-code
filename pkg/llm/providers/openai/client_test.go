package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm"
	httptesting "github.com/openshift-pipelines/pipelines-as-code/pkg/test/http"
	"gotest.tools/v3/assert"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name      string
		config    *llm.ProviderConfig
		wantError bool
		errMsg    string
	}{
		{
			name:      "nil config",
			config:    nil,
			wantError: true,
			errMsg:    "config is required",
		},
		{
			name: "empty api key",
			config: &llm.ProviderConfig{
				APIKey: "",
			},
			wantError: true,
			errMsg:    "API key is required",
		},
		{
			name: "valid config with defaults",
			config: &llm.ProviderConfig{
				APIKey: "test-key",
			},
			wantError: false,
		},
		{
			name: "custom config",
			config: &llm.ProviderConfig{
				APIKey:         "test-key",
				BaseURL:        "https://custom.url",
				Model:          "gpt-3.5-turbo",
				TimeoutSeconds: 60,
				MaxTokens:      2000,
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := newClient(tt.config)

			if tt.wantError {
				assert.Assert(t, err != nil)
				assert.ErrorContains(t, err, tt.errMsg)
				assert.Assert(t, c == nil)
			} else {
				assert.NilError(t, err)
				assert.Assert(t, c != nil)
				assert.Equal(t, c.GetProviderName(), "openai")
				client, ok := c.(*Client)
				assert.Assert(t, ok)
				if tt.config.BaseURL == "" {
					assert.Equal(t, client.config.BaseURL, defaultBaseURL)
				}
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    *llm.ProviderConfig
		wantError bool
		errMsg    string
	}{
		{
			name: "valid config",
			config: &llm.ProviderConfig{
				APIKey:         "valid-key",
				BaseURL:        "https://api.openai.com/v1",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: false,
		},
		{
			name: "empty api key",
			config: &llm.ProviderConfig{
				APIKey:         "",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: true,
			errMsg:    "API key is required",
		},
		{
			name: "negative timeout",
			config: &llm.ProviderConfig{
				APIKey:         "valid-key",
				BaseURL:        "https://api.openai.com/v1",
				TimeoutSeconds: -1,
				MaxTokens:      1000,
			},
			wantError: true,
			errMsg:    "timeout seconds must be non-negative",
		},
		{
			name: "negative max tokens",
			config: &llm.ProviderConfig{
				APIKey:         "valid-key",
				BaseURL:        "https://api.openai.com/v1",
				TimeoutSeconds: 30,
				MaxTokens:      -1,
			},
			wantError: true,
			errMsg:    "max tokens must be non-negative",
		},
		{
			name: "invalid URL - no scheme",
			config: &llm.ProviderConfig{
				APIKey:         "valid-key",
				BaseURL:        "api.openai.com",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: true,
			errMsg:    "URL scheme must be",
		},
		{
			name: "invalid URL - wrong scheme",
			config: &llm.ProviderConfig{
				APIKey:         "valid-key",
				BaseURL:        "ftp://api.openai.com",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: true,
			errMsg:    "URL scheme must be",
		},
		{
			name: "invalid URL - has whitespace",
			config: &llm.ProviderConfig{
				APIKey:         "valid-key",
				BaseURL:        "https://api.openai.com /v1",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: true,
			errMsg:    "URL contains invalid whitespace",
		},
		{
			name: "invalid URL - no host",
			config: &llm.ProviderConfig{
				APIKey:         "valid-key",
				BaseURL:        "https://",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: true,
			errMsg:    "URL must contain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := newClient(tt.config)
			if tt.config.APIKey == "" {
				assert.Assert(t, err != nil)
				assert.ErrorContains(t, err, "API key is required")
				return
			}
			assert.NilError(t, err)
			err = c.ValidateConfig()

			if tt.wantError {
				assert.Assert(t, err != nil)
				assert.ErrorContains(t, err, tt.errMsg)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestGetProviderName(t *testing.T) {
	c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
	assert.Equal(t, c.GetProviderName(), "openai")
}

func TestAnalyzeSuccess(t *testing.T) {
	c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
	client, ok := c.(*Client)
	assert.Assert(t, ok)

	mockResponse := openaiResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-5.4-mini",
		Choices: []openaiChoice{
			{
				Index: 0,
				Message: openaiMessage{
					Role:    "assistant",
					Content: "This is the analysis result",
				},
				FinishReason: "stop",
			},
		},
		Usage: openaiUsage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	client.httpClient = &http.Client{
		Transport: httptesting.RoundTripFunc(func(req *http.Request) *http.Response {
			assert.Equal(t, req.Method, "POST")
			assert.Assert(t, strings.Contains(req.URL.String(), "/chat/completions"))

			body, err := json.Marshal(mockResponse)
			assert.NilError(t, err)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
			}
		}),
	}

	request := &llm.AnalysisRequest{
		Prompt:    "Analyze this",
		MaxTokens: 100,
	}

	response, err := client.Analyze(context.Background(), request)

	assert.NilError(t, err)
	assert.Equal(t, response.Content, "This is the analysis result")
	assert.Equal(t, response.Provider, "openai")
	assert.Equal(t, response.TokensUsed, 15)
	assert.Assert(t, response.Duration > 0)
}

func TestAnalyzePromptBuildError(t *testing.T) {
	c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
	client, ok := c.(*Client)
	assert.Assert(t, ok)

	request := &llm.AnalysisRequest{
		Prompt: "Test",
		Context: map[string]any{
			"nested": map[string]any{
				"bad": make(chan int),
			},
		},
	}

	response, err := client.Analyze(context.Background(), request)

	assert.Assert(t, err != nil)
	assert.Assert(t, response == nil)
}

func TestAnalyzeErrors(t *testing.T) {
	tests := []struct {
		name            string
		httpResponse    *http.Response
		expectedErrType string
	}{
		{
			name: "HTTP Error (empty response)",
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("{}")),
			},
			expectedErrType: "empty_response",
		},
		{
			name: "Response Parse Error",
			httpResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("invalid json")),
			},
			expectedErrType: "response_parse_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
			client, ok := c.(*Client)
			assert.Assert(t, ok)

			client.httpClient = &http.Client{
				Transport: httptesting.RoundTripFunc(func(_ *http.Request) *http.Response {
					return tt.httpResponse
				}),
			}

			request := &llm.AnalysisRequest{
				Prompt:    "Analyze",
				MaxTokens: 100,
			}

			response, err := client.Analyze(context.Background(), request)

			assert.Assert(t, err != nil)
			assert.Assert(t, response == nil)
			var analysisErr *llm.AnalysisError
			assert.Assert(t, errors.As(err, &analysisErr))
			assert.Equal(t, analysisErr.Type, tt.expectedErrType)
		})
	}
}

func TestAnalyzeAPIError(t *testing.T) {
	tests := []struct {
		name            string
		mockResponse    openaiResponse
		statusCode      int
		expectedErrType string
		retryable       bool
		checkMessage    bool
		messageContains string
	}{
		{
			name: "Rate Limit Exceeded",
			mockResponse: openaiResponse{
				Error: &openaiError{Code: "rate_limit_exceeded", Message: "Rate limit exceeded", Type: "server_error"},
			},
			statusCode:      http.StatusTooManyRequests,
			expectedErrType: "rate_limit_exceeded",
			retryable:       true,
		},
		{
			name: "Internal Server Error",
			mockResponse: openaiResponse{
				Error: &openaiError{Code: "server_error", Message: "Internal server error", Type: "server_error"},
			},
			statusCode:      http.StatusInternalServerError,
			expectedErrType: "server_error",
			retryable:       true,
		},
		{
			name: "Unauthorized",
			mockResponse: openaiResponse{
				Error: &openaiError{Code: "invalid_api_key", Message: "Invalid API key", Type: "invalid_request_error"},
			},
			statusCode:      http.StatusUnauthorized,
			expectedErrType: "invalid_api_key",
			retryable:       false,
		},
		{
			name: "Generic API Error",
			mockResponse: openaiResponse{
				Error: &openaiError{Code: "unknown_error", Message: "Some error occurred", Type: "unknown"},
			},
			statusCode:      http.StatusBadRequest,
			expectedErrType: "api_error",
			retryable:       false,
		},
		{
			name:            "API Error without body",
			mockResponse:    openaiResponse{},
			statusCode:      http.StatusBadRequest,
			expectedErrType: "api_error",
			retryable:       false,
			checkMessage:    true,
			messageContains: "status 400",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
			client, ok := c.(*Client)
			assert.Assert(t, ok)

			client.httpClient = &http.Client{
				Transport: httptesting.RoundTripFunc(func(_ *http.Request) *http.Response {
					body, err := json.Marshal(tt.mockResponse)
					assert.NilError(t, err)
					return &http.Response{
						StatusCode: tt.statusCode,
						Body:       io.NopCloser(bytes.NewReader(body)),
					}
				}),
			}

			request := &llm.AnalysisRequest{
				Prompt:    "Analyze",
				MaxTokens: 100,
			}

			response, err := client.Analyze(context.Background(), request)

			assert.Assert(t, err != nil)
			assert.Assert(t, response == nil)
			var analysisErr *llm.AnalysisError
			assert.Assert(t, errors.As(err, &analysisErr))

			if tt.checkMessage {
				assert.Assert(t, strings.Contains(analysisErr.Message, tt.messageContains))
			} else {
				assert.Equal(t, analysisErr.Type, tt.expectedErrType)
			}
			if !tt.checkMessage {
				assert.Equal(t, analysisErr.Retryable, tt.retryable)
			}
		})
	}
}

func TestAnalyzeEmptyResponse(t *testing.T) {
	c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
	client, ok := c.(*Client)
	assert.Assert(t, ok)

	mockResponse := openaiResponse{
		Choices: []openaiChoice{},
	}

	client.httpClient = &http.Client{
		Transport: httptesting.RoundTripFunc(func(_ *http.Request) *http.Response {
			body, err := json.Marshal(mockResponse)
			assert.NilError(t, err)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
			}
		}),
	}

	request := &llm.AnalysisRequest{
		Prompt:    "Analyze",
		MaxTokens: 100,
	}

	response, err := client.Analyze(context.Background(), request)

	assert.Assert(t, err != nil)
	assert.Assert(t, response == nil)
	var analysisErr *llm.AnalysisError
	assert.Assert(t, errors.As(err, &analysisErr))
	assert.Equal(t, analysisErr.Type, "empty_response")
}

func TestAnalyzeTimeout(t *testing.T) {
	c, _ := newClient(&llm.ProviderConfig{
		APIKey:         "test-key",
		TimeoutSeconds: 1,
	})
	client, ok := c.(*Client)
	assert.Assert(t, ok)

	client.httpClient = &http.Client{
		Transport: httptesting.RoundTripFunc(func(_ *http.Request) *http.Response {
			time.Sleep(200 * time.Millisecond)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("")),
			}
		}),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	request := &llm.AnalysisRequest{
		Prompt:    "Analyze",
		MaxTokens: 100,
	}

	response, err := client.Analyze(ctx, request)

	assert.Assert(t, err != nil)
	assert.Assert(t, response == nil)
}

func TestAnalyzeWithContext(t *testing.T) {
	c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
	client, ok := c.(*Client)
	assert.Assert(t, ok)

	mockResponse := openaiResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-5.4-mini",
		Choices: []openaiChoice{
			{
				Index: 0,
				Message: openaiMessage{
					Role:    "assistant",
					Content: "Analysis with context",
				},
				FinishReason: "stop",
			},
		},
		Usage: openaiUsage{
			PromptTokens:     20,
			CompletionTokens: 5,
			TotalTokens:      25,
		},
	}

	client.httpClient = &http.Client{
		Transport: httptesting.RoundTripFunc(func(req *http.Request) *http.Response {
			var reqBody openaiRequest
			err := json.NewDecoder(req.Body).Decode(&reqBody)
			assert.NilError(t, err)
			assert.Equal(t, reqBody.Model, "gpt-5.4-mini")
			assert.Equal(t, len(reqBody.Messages), 1)

			body, err := json.Marshal(mockResponse)
			assert.NilError(t, err)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
			}
		}),
	}

	request := &llm.AnalysisRequest{
		Prompt:    "Analyze",
		MaxTokens: 100,
		Context: map[string]any{
			"logs": "some error logs",
		},
	}

	response, err := client.Analyze(context.Background(), request)

	assert.NilError(t, err)
	assert.Equal(t, response.TokensUsed, 25)
}

func TestConfigDefaults(t *testing.T) {
	c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
	client, ok := c.(*Client)
	assert.Assert(t, ok)

	assert.Equal(t, client.config.BaseURL, defaultBaseURL)
	assert.Equal(t, client.config.Model, defaultModel)
	assert.Equal(t, client.config.TimeoutSeconds, llm.DefaultTimeoutSeconds)
	assert.Equal(t, client.config.MaxTokens, llm.DefaultMaxTokens)
}

func TestRequestMarshaling(t *testing.T) {
	c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
	client, ok := c.(*Client)
	assert.Assert(t, ok)

	client.httpClient = &http.Client{
		Transport: httptesting.RoundTripFunc(func(req *http.Request) *http.Response {
			var reqBody openaiRequest
			err := json.NewDecoder(req.Body).Decode(&reqBody)
			assert.NilError(t, err)
			assert.Equal(t, reqBody.Model, "gpt-5.4-mini")
			assert.Equal(t, len(reqBody.Messages), 1)
			assert.Equal(t, reqBody.Messages[0].Role, "user")
			assert.Equal(t, reqBody.MaxCompletionTokens, 100)

			resp := openaiResponse{
				Choices: []openaiChoice{
					{
						Message: openaiMessage{
							Content: "Response",
						},
					},
				},
				Usage: openaiUsage{TotalTokens: 50},
			}
			body, err := json.Marshal(resp)
			assert.NilError(t, err)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
			}
		}),
	}

	request := &llm.AnalysisRequest{
		Prompt:    "Test prompt",
		MaxTokens: 100,
	}

	response, err := client.Analyze(context.Background(), request)

	assert.NilError(t, err)
	assert.Equal(t, response.Content, "Response")
}
