package gemini

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
				Model:          "custom-model",
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
				assert.Equal(t, c.GetProviderName(), "gemini")
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
				BaseURL:        "https://api.example.com",
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
				BaseURL:        "https://api.example.com",
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
				BaseURL:        "https://api.example.com",
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
				BaseURL:        "api.example.com",
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
				BaseURL:        "ftp://api.example.com",
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
				BaseURL:        "https://api.example.com /path",
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
	assert.Equal(t, c.GetProviderName(), "gemini")
}

func TestAnalyzeSuccess(t *testing.T) {
	c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
	client, ok := c.(*Client)
	assert.Assert(t, ok)

	mockResponse := geminiResponse{
		Candidates: []geminiCandidate{
			{
				Content: geminiContent{
					Parts: []geminiPart{
						{Text: "This is the analysis result"},
					},
				},
			},
		},
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
		Prompt:    "Analyze this",
		MaxTokens: 100,
	}

	response, err := client.Analyze(context.Background(), request)

	assert.NilError(t, err)
	assert.Equal(t, response.Content, "This is the analysis result")
	assert.Equal(t, response.Provider, "gemini")
	assert.Assert(t, response.TokensUsed > 0)
	assert.Assert(t, response.Duration > 0)
}

func TestAnalyzeRequestCreationError(t *testing.T) {
	tests := []struct {
		name    string
		context map[string]any
	}{
		{
			name: "Bad context",
			context: map[string]any{
				"bad": make(chan int),
			},
		},
		{
			name: "Nested bad context",
			context: map[string]any{
				"nested": map[string]any{
					"bad": make(chan int),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
			client, ok := c.(*Client)
			assert.Assert(t, ok)

			request := &llm.AnalysisRequest{
				Prompt:    "Test",
				MaxTokens: 100,
				Context:   tt.context,
			}

			response, err := client.Analyze(context.Background(), request)

			assert.Assert(t, err != nil)
			assert.Assert(t, response == nil)
		})
	}
}

func testAnalyzeError(t *testing.T, httpResponse *http.Response, expectedErrType string) {
	t.Helper()
	c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
	client, ok := c.(*Client)
	assert.Assert(t, ok)

	client.httpClient = &http.Client{
		Transport: httptesting.RoundTripFunc(func(_ *http.Request) *http.Response {
			return httpResponse
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
	assert.Equal(t, analysisErr.Type, expectedErrType)
}

func TestAnalyzeHTTPError(t *testing.T) {
	testAnalyzeError(t, &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("{}")),
	}, "empty_response")
}

func TestAnalyzeResponseParseError(t *testing.T) {
	testAnalyzeError(t, &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("invalid json")),
	}, "response_parse_error")
}

func TestAnalyzeAPIError(t *testing.T) {
	tests := []struct {
		name            string
		mockResponse    geminiResponse
		statusCode      int
		expectedErrType string
		retryable       bool
	}{
		{
			name: "Rate Limit Exceeded",
			mockResponse: geminiResponse{
				Error: &geminiError{Code: 429, Message: "Rate limit exceeded", Status: "RESOURCE_EXHAUSTED"},
			},
			statusCode:      http.StatusTooManyRequests,
			expectedErrType: "rate_limit_exceeded",
			retryable:       true,
		},
		{
			name: "Internal Server Error",
			mockResponse: geminiResponse{
				Error: &geminiError{Code: 500, Message: "Internal server error", Status: "INTERNAL"},
			},
			statusCode:      http.StatusInternalServerError,
			expectedErrType: "server_error",
			retryable:       true,
		},
		{
			name: "Unauthorized",
			mockResponse: geminiResponse{
				Error: &geminiError{Code: 401, Message: "Invalid API key", Status: "UNAUTHENTICATED"},
			},
			statusCode:      http.StatusUnauthorized,
			expectedErrType: "invalid_api_key",
			retryable:       false,
		},
		{
			name: "Forbidden",
			mockResponse: geminiResponse{
				Error: &geminiError{Code: 403, Message: "Forbidden", Status: "PERMISSION_DENIED"},
			},
			statusCode:      http.StatusForbidden,
			expectedErrType: "invalid_api_key",
			retryable:       false,
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
			assert.Equal(t, analysisErr.Type, tt.expectedErrType)
			assert.Equal(t, analysisErr.Retryable, tt.retryable)
		})
	}
}

func TestAnalyzeEmptyContent(t *testing.T) {
	tests := []struct {
		name         string
		mockResponse geminiResponse
	}{
		{
			name: "Empty Candidates",
			mockResponse: geminiResponse{
				Candidates: []geminiCandidate{},
			},
		},
		{
			name: "Empty Parts",
			mockResponse: geminiResponse{
				Candidates: []geminiCandidate{
					{
						Content: geminiContent{
							Parts: []geminiPart{},
						},
					},
				},
			},
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
		})
	}
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

func TestConfigDefaults(t *testing.T) {
	c, _ := newClient(&llm.ProviderConfig{APIKey: "test-key"})
	client, ok := c.(*Client)
	assert.Assert(t, ok)

	assert.Equal(t, client.config.BaseURL, defaultBaseURL)
	assert.Equal(t, client.config.Model, defaultModel)
	assert.Equal(t, client.config.TimeoutSeconds, llm.DefaultTimeoutSeconds)
	assert.Equal(t, client.config.MaxTokens, llm.DefaultMaxTokens)
}
