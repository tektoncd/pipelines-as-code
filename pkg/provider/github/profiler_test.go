package github

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWrapAPI(t *testing.T) {
	// Test case: Logger is nil
	t.Run("Logger is nil", func(t *testing.T) {
		p := &Provider{}
		called := false
		call := func() (string, *github.Response, error) {
			called = true
			return "test", nil, nil
		}

		data, resp, err := wrapAPI(p, "test_api_call", call)
		assert.Assert(t, called, "Original call should be made")
		assert.Equal(t, "test", data)
		assert.Assert(t, resp == nil)
		assert.Assert(t, err == nil)
	})

	// Test case: Logger is not nil
	t.Run("Logger is not nil", func(t *testing.T) {
		observedZapCore, observedLogs := observer.New(zap.DebugLevel)
		observedLogger := zap.New(observedZapCore).Sugar()
		p := &Provider{
			Logger:       observedLogger,
			providerName: "github",
			triggerEvent: "pull_request",
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-repo",
				},
			},
		}
		called := false
		reqURL, _ := url.Parse("https://api.github.com/test")
		headers := http.Header{}
		headers.Set("X-RateLimit-Remaining", "99")
		resp := &github.Response{
			Response: &http.Response{
				Request: &http.Request{URL: reqURL},
				Header:  headers,
			},
		}
		call := func() (string, *github.Response, error) {
			called = true
			return "data", resp, fmt.Errorf("error")
		}

		data, r, e := wrapAPI(p, "test_api_call", call)

		assert.Assert(t, called)
		assert.Equal(t, "data", data)
		assert.Equal(t, r, resp)
		assert.Error(t, e, "error")

		logs := observedLogs.All()
		assert.Assert(t, len(logs) > 0, "Should have log entries")

		// Find the API call log entry
		var apiCallLog *observer.LoggedEntry
		for i := range logs {
			if logs[i].Message == "GitHub API call failed" {
				apiCallLog = &logs[i]
				break
			}
		}
		assert.Assert(t, apiCallLog != nil, "Should have API call failed log entry")

		// Check structured fields
		foundOperation := false
		foundProvider := false
		foundRepo := false
		for _, field := range apiCallLog.Context {
			switch field.Key {
			case "operation":
				assert.Equal(t, "test_api_call", field.String)
				foundOperation = true
			case "provider":
				assert.Equal(t, "github", field.String)
				foundProvider = true
			case "repo":
				assert.Equal(t, "test-ns/test-repo", field.String)
				foundRepo = true
			}
		}

		assert.Assert(t, foundOperation, "Should have operation field")
		assert.Assert(t, foundProvider, "Should have provider field")
		assert.Assert(t, foundRepo, "Should have repo field")
	})

	// Test case: Logger is not nil and response is nil
	t.Run("Logger is not nil, response is nil", func(t *testing.T) {
		observedZapCore, observedLogs := observer.New(zap.DebugLevel)
		observedLogger := zap.New(observedZapCore).Sugar()
		p := &Provider{
			Logger:       observedLogger,
			providerName: "github",
			triggerEvent: "pull_request",
		}
		call := func() (string, *github.Response, error) {
			return "data", nil, nil
		}

		_, _, _ = wrapAPI(p, "test_api_call", call)

		logs := observedLogs.All()
		assert.Assert(t, len(logs) > 0, "Should have log entries")

		// Find the API call log entry
		var apiCallLog *observer.LoggedEntry
		for i := range logs {
			if logs[i].Message == "GitHub API call completed" {
				apiCallLog = &logs[i]
				break
			}
		}
		assert.Assert(t, apiCallLog != nil, "Should have API call completed log entry")

		// Check that it has operation but no URL/rate limit info
		foundOperation := false
		foundURL := false
		foundRateLimit := false
		for _, field := range apiCallLog.Context {
			switch field.Key {
			case "operation":
				foundOperation = true
			case "url_path":
				foundURL = true
			case "rate_limit_remaining":
				foundRateLimit = true
			}
		}

		assert.Assert(t, foundOperation, "Should have operation field")
		assert.Assert(t, !foundURL, "Should not have URL field when response is nil")
		assert.Assert(t, !foundRateLimit, "Should not have rate limit field when response is nil")
	})
}

func TestRateLimitWarnings(t *testing.T) {
	tests := []struct {
		name             string
		remaining        string
		limit            string
		reset            string
		expectedLogLevel zapcore.Level
		expectedMessage  string
	}{
		{
			name:             "Critical rate limit",
			remaining:        "25",
			limit:            "5000",
			reset:            "1672531200", // 2023-01-01 00:00:00 UTC
			expectedLogLevel: zap.ErrorLevel,
			expectedMessage:  "GitHub API rate limit critically low",
		},
		{
			name:             "Warning rate limit",
			remaining:        "75",
			limit:            "5000",
			reset:            "1672531200",
			expectedLogLevel: zap.WarnLevel,
			expectedMessage:  "GitHub API rate limit running low",
		},
		{
			name:             "Info rate limit",
			remaining:        "300",
			limit:            "5000",
			reset:            "1672531200",
			expectedLogLevel: zap.InfoLevel,
			expectedMessage:  "GitHub API rate limit moderate",
		},
		{
			name:            "Normal rate limit",
			remaining:       "1000",
			limit:           "5000",
			reset:           "1672531200",
			expectedMessage: "", // No message expected
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observedZapCore, observedLogs := observer.New(zap.DebugLevel)
			observedLogger := zap.New(observedZapCore).Sugar()
			p := &Provider{
				Logger:       observedLogger,
				providerName: "github",
				triggerEvent: "pull_request",
				repo: &v1alpha1.Repository{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "test-ns",
						Name:      "test-repo",
					},
				},
			}

			reqURL, _ := url.Parse("https://api.github.com/test")
			headers := http.Header{}
			headers.Set("X-RateLimit-Remaining", tt.remaining)
			if tt.limit != "" {
				headers.Set("X-RateLimit-Limit", tt.limit)
			}
			if tt.reset != "" {
				headers.Set("X-RateLimit-Reset", tt.reset)
			}

			resp := &github.Response{
				Response: &http.Response{
					Request: &http.Request{URL: reqURL},
					Header:  headers,
				},
			}

			call := func() (string, *github.Response, error) {
				return "data", resp, nil
			}

			_, _, _ = wrapAPI(p, "test_api_call", call)

			if tt.expectedMessage == "" {
				for _, log := range observedLogs.All() {
					assert.Assert(t, !strings.Contains(log.Message, "rate limit"))
				}
				return
			}

			var rateLimitLog *observer.LoggedEntry
			for i := range observedLogs.All() {
				if strings.Contains(observedLogs.All()[i].Message, "rate limit") {
					rateLimitLog = &observedLogs.All()[i]
					break
				}
			}

			assert.Assert(t, rateLimitLog != nil, "expected log message not found")
			assert.Equal(t, tt.expectedLogLevel, rateLimitLog.Level)
			assert.Equal(t, tt.expectedMessage, rateLimitLog.Message)
		})
	}
}

func TestWrapGetContents(t *testing.T) {
	// Test case: Logger is nil
	t.Run("Logger is nil", func(t *testing.T) {
		p := &Provider{}
		called := false
		call := func() (*github.RepositoryContent, []*github.RepositoryContent, *github.Response, error) {
			called = true
			return nil, nil, nil, nil
		}

		_, _, _, _ = wrapAPIGetContents(p, "get_contents", call)
		assert.Assert(t, called, "Original call should be made")
	})

	// Test case: Logger is not nil
	t.Run("Logger is not nil", func(t *testing.T) {
		observedZapCore, observedLogs := observer.New(zap.DebugLevel)
		observedLogger := zap.New(observedZapCore).Sugar()
		p := &Provider{
			Logger:       observedLogger,
			providerName: "github",
			triggerEvent: "pull_request",
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-repo",
				},
			},
		}
		reqURL, _ := url.Parse("https://api.github.com/contents")
		headers := http.Header{}
		headers.Set("X-RateLimit-Remaining", "42")
		resp := &github.Response{
			Response: &http.Response{
				Request: &http.Request{URL: reqURL},
				Header:  headers,
			},
		}
		fileContent := &github.RepositoryContent{Name: github.Ptr("file")}
		dirContent := []*github.RepositoryContent{{Name: github.Ptr("dir_file")}}

		call := func() (*github.RepositoryContent, []*github.RepositoryContent, *github.Response, error) {
			return fileContent, dirContent, resp, fmt.Errorf("contents error")
		}

		file, dir, r, e := wrapAPIGetContents(p, "get_contents", call)

		assert.Equal(t, file, fileContent)
		assert.Equal(t, len(dir), 1)
		assert.Equal(t, r, resp)
		assert.Error(t, e, "contents error")

		logs := observedLogs.All()
		assert.Assert(t, len(logs) > 0, "Should have log entries")

		// Find the API call log entry
		var apiCallLog *observer.LoggedEntry
		for i := range logs {
			if logs[i].Message == "GitHub API call failed" {
				apiCallLog = &logs[i]
				break
			}
		}
		assert.Assert(t, apiCallLog != nil, "Should have API call failed log entry")

		// Check structured fields
		foundOperation := false
		foundProvider := false
		foundRepo := false
		foundRateLimit := false
		for _, field := range apiCallLog.Context {
			switch field.Key {
			case "operation":
				assert.Equal(t, "get_contents", field.String)
				foundOperation = true
			case "provider":
				assert.Equal(t, "github", field.String)
				foundProvider = true
			case "repo":
				assert.Equal(t, "test-ns/test-repo", field.String)
				foundRepo = true
			case "rate_limit_remaining":
				assert.Equal(t, "42", field.String)
				foundRateLimit = true
			}
		}

		assert.Assert(t, foundOperation, "Should have operation field")
		assert.Assert(t, foundProvider, "Should have provider field")
		assert.Assert(t, foundRepo, "Should have repo field")
		assert.Assert(t, foundRateLimit, "Should have rate limit field")
	})
}
