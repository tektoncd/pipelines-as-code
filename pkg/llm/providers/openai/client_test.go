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

	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/ltypes"
	httptesting "github.com/openshift-pipelines/pipelines-as-code/pkg/test/http"
	"gotest.tools/v3/assert"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
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
			config: &Config{
				APIKey: "",
			},
			wantError: true,
			errMsg:    "API key is required",
		},
		{
			name: "valid config with defaults",
			config: &Config{
				APIKey: "test-key",
			},
			wantError: false,
		},
		{
			name: "custom config",
			config: &Config{
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
			client, err := NewClient(tt.config)

			if tt.wantError {
				assert.Assert(t, err != nil)
				assert.ErrorContains(t, err, tt.errMsg)
				assert.Assert(t, client == nil)
			} else {
				assert.NilError(t, err)
				assert.Assert(t, client != nil)
				assert.Equal(t, client.GetProviderName(), "openai")
				if tt.config.BaseURL == "" {
					assert.Equal(t, client.config.BaseURL, defaultBaseURL)
				}
			}
		})
	}
}

func TestNewClientWithCustomHTTPClient(t *testing.T) {
	customHTTPClient := &http.Client{
		Timeout: 15 * time.Second,
	}

	config := &Config{
		APIKey:     "test-key",
		HTTPClient: customHTTPClient,
	}

	client, err := NewClient(config)

	assert.NilError(t, err)
	assert.Assert(t, client.httpClient == customHTTPClient)
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
		wantError bool
		errMsg    string
	}{
		{
			name: "valid config",
			config: &Config{
				APIKey:         "valid-key",
				BaseURL:        "https://api.openai.com/v1",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: false,
		},
		{
			name: "empty api key",
			config: &Config{
				APIKey:         "",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: true,
			errMsg:    "API key is required",
		},
		{
			name: "negative timeout",
			config: &Config{
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
			config: &Config{
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
			config: &Config{
				APIKey:         "valid-key",
				BaseURL:        "api.openai.com",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: true,
			errMsg:    "base URL must use http or https scheme",
		},
		{
			name: "invalid URL - wrong scheme",
			config: &Config{
				APIKey:         "valid-key",
				BaseURL:        "ftp://api.openai.com",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: true,
			errMsg:    "base URL must use http or https scheme",
		},
		{
			name: "invalid URL - has whitespace",
			config: &Config{
				APIKey:         "valid-key",
				BaseURL:        "https://api.openai.com /v1",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: true,
			errMsg:    "invalid base URL",
		},
		{
			name: "invalid URL - no host",
			config: &Config{
				APIKey:         "valid-key",
				BaseURL:        "https://",
				TimeoutSeconds: 30,
				MaxTokens:      1000,
			},
			wantError: true,
			errMsg:    "base URL must have a valid host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.config)
			if tt.config.APIKey == "" {
				assert.Assert(t, err != nil)
				assert.ErrorContains(t, err, "API key is required")
				return
			}
			assert.NilError(t, err)
			err = client.ValidateConfig()

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
	config := &Config{APIKey: "test-key"}
	client, _ := NewClient(config)
	assert.Equal(t, client.GetProviderName(), "openai")
}

func TestAnalyzeSuccess(t *testing.T) {
	config := &Config{APIKey: "test-key"}
	client, _ := NewClient(config)

	mockResponse := openaiResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-4",
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

	request := &ltypes.AnalysisRequest{
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
	config := &Config{APIKey: "test-key"}
	client, _ := NewClient(config)

	request := &ltypes.AnalysisRequest{
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
			config := &Config{APIKey: "test-key"}
			client, _ := NewClient(config)

			client.httpClient = &http.Client{
				Transport: httptesting.RoundTripFunc(func(_ *http.Request) *http.Response {
					return tt.httpResponse
				}),
			}

			request := &ltypes.AnalysisRequest{
				Prompt:    "Analyze",
				MaxTokens: 100,
			}

			response, err := client.Analyze(context.Background(), request)

			assert.Assert(t, err != nil)
			assert.Assert(t, response == nil)
			var analysisErr *ltypes.AnalysisError
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
			config := &Config{APIKey: "test-key"}
			client, _ := NewClient(config)

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

			request := &ltypes.AnalysisRequest{
				Prompt:    "Analyze",
				MaxTokens: 100,
			}

			response, err := client.Analyze(context.Background(), request)

			assert.Assert(t, err != nil)
			assert.Assert(t, response == nil)
			var analysisErr *ltypes.AnalysisError
			assert.Assert(t, errors.As(err, &analysisErr))

			if tt.checkMessage {
				assert.Assert(t, strings.Contains(analysisErr.Message, tt.messageContains))
			} else {
				assert.Equal(t, analysisErr.Type, tt.expectedErrType)
			}
			// Only check retryable if not checking message since that case doesn't have a specific retryable flag
			if !tt.checkMessage {
				assert.Equal(t, analysisErr.Retryable, tt.retryable)
			}
		})
	}
}

func TestAnalyzeEmptyResponse(t *testing.T) {
	config := &Config{APIKey: "test-key"}
	client, _ := NewClient(config)

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

	request := &ltypes.AnalysisRequest{
		Prompt:    "Analyze",
		MaxTokens: 100,
	}

	response, err := client.Analyze(context.Background(), request)

	assert.Assert(t, err != nil)
	assert.Assert(t, response == nil)
	var analysisErr *ltypes.AnalysisError
	assert.Assert(t, errors.As(err, &analysisErr))
	assert.Equal(t, analysisErr.Type, "empty_response")
}

func TestAnalyzeTimeout(t *testing.T) {
	config := &Config{
		APIKey:         "test-key",
		TimeoutSeconds: 1,
	}
	client, _ := NewClient(config)

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

	request := &ltypes.AnalysisRequest{
		Prompt:    "Analyze",
		MaxTokens: 100,
	}

	response, err := client.Analyze(ctx, request)

	assert.Assert(t, err != nil)
	assert.Assert(t, response == nil)
}

func TestAnalyzeWithContext(t *testing.T) {
	config := &Config{APIKey: "test-key"}
	client, _ := NewClient(config)

	mockResponse := openaiResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion",
		Created: 1234567890,
		Model:   "gpt-4",
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
			assert.Equal(t, reqBody.Model, "gpt-4")
			assert.Equal(t, len(reqBody.Messages), 1)

			body, err := json.Marshal(mockResponse)
			assert.NilError(t, err)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(body)),
			}
		}),
	}

	request := &ltypes.AnalysisRequest{
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
	config := &Config{APIKey: "test-key"}
	client, _ := NewClient(config)

	assert.Equal(t, client.config.BaseURL, defaultBaseURL)
	assert.Equal(t, client.config.Model, defaultModel)
	assert.Equal(t, client.config.TimeoutSeconds, defaultTimeoutSeconds)
	assert.Equal(t, client.config.MaxTokens, defaultMaxTokens)
}

func TestRequestMarshaling(t *testing.T) {
	config := &Config{APIKey: "test-key"}
	client, _ := NewClient(config)

	client.httpClient = &http.Client{
		Transport: httptesting.RoundTripFunc(func(req *http.Request) *http.Response {
			var reqBody openaiRequest
			err := json.NewDecoder(req.Body).Decode(&reqBody)
			assert.NilError(t, err)
			assert.Equal(t, reqBody.Model, "gpt-4")
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

	request := &ltypes.AnalysisRequest{
		Prompt:    "Test prompt",
		MaxTokens: 100,
	}

	response, err := client.Analyze(context.Background(), request)

	assert.NilError(t, err)
	assert.Equal(t, response.Content, "Response")
}
