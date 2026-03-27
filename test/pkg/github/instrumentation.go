package github

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tlogs "github.com/openshift-pipelines/pipelines-as-code/test/pkg/podlogs"
)

// InstrumentationAPICall represents a single GitHub API call with its metadata.
type InstrumentationAPICall struct {
	Operation          string `json:"operation"`
	DurationMs         int64  `json:"duration_ms"`
	URLPath            string `json:"url_path"`
	RateLimitRemaining string `json:"rate_limit_remaining"`
	StatusCode         int    `json:"status_code"`
	Provider           string `json:"provider"`
	Repo               string `json:"repo"`
}

// TestResult represents the complete test result with GitHub API calls.
type TestResult struct {
	TestName         string                   `json:"test_name"`
	Timestamp        string                   `json:"timestamp"`
	Controller       string                   `json:"controller"`
	PRNumber         int                      `json:"pr_number"`
	SHA              string                   `json:"sha"`
	TargetNamespace  string                   `json:"target_namespace"`
	GitHubAPICalls   []InstrumentationAPICall `json:"github_api_calls"`
	TotalCalls       int                      `json:"total_calls"`
	OAuth2MarkerLine int                      `json:"oauth2_marker_line"`
}

// collectGitHubAPICalls collects GitHub API calls from the controller logs.
// It retrieves logs from the controller pod, searches for the last OAuth2 initialization marker,
// the OAuth2 marker shows even if the controller is not using github app, so
// we can use it to find the start of the API calls.
// TODO(chmouel): Fix Oauth2 marker to be more specific to GitHub App usage and
// not github webhook. use another marker for github webhook.
// TODO(chmouel): Add support for Gitlab.
// TODO(chmouel): Add support for Bitbucket.
// TODO(chmouel): Add support for Gitea.
func (g *PRTest) collectGitHubAPICalls(ctx context.Context, _ *testing.T) {
	if g.Cnx == nil || g.Cnx.Clients.Kube == nil || g.Logger == nil {
		return
	}

	numLines := int64(100)
	controllerName := "controller"
	if g.GHE {
		controllerName = "ghe-controller"
	}

	var logContent string
	var source tlogs.ControllerLogSource
	var err error
	var foundNS string

	for _, installNS := range info.InstallNamespaces {
		g.Logger.Infof(
			"Attempting to collect GitHub API calls from controller: %s in namespace: %s",
			controllerName, installNS,
		)

		logContent, source, err = tlogs.GetControllerLogByName(
			ctx, g.Cnx.Clients.Kube.CoreV1(), installNS, controllerName, &numLines, nil,
		)
		if err == nil {
			foundNS = installNS
			break
		}
		g.Logger.Debugf("Controller not found in namespace %s: %v", installNS, err)
	}

	if err != nil {
		g.Logger.Warnf("Failed to get controller logs from any installation namespace: %v", err)
		return
	}

	g.Logger.Infof("Found controller in namespace: %s", foundNS)

	g.Logger.Infof(
		"Collected controller logs using label selector %q and container %q",
		source.LabelSelector, source.ContainerName,
	)

	logLines := strings.Split(logContent, "\n")
	g.Logger.Infof("Collected %d lines from controller logs", len(logLines))

	// Find the last OAuth2 initialization marker
	oauth2Marker := "github-app: initialized OAuth2 client"
	lastOAuth2Index := -1
	for i := len(logLines) - 1; i >= 0; i-- {
		if strings.Contains(logLines[i], oauth2Marker) {
			lastOAuth2Index = i
			break
		}
	}

	if lastOAuth2Index == -1 {
		g.Logger.Infof("OAuth2 initialization marker not found in the last %d lines", numLines)
		return
	}

	g.Logger.Infof("Found OAuth2 initialization marker at line %d (from end)", len(logLines)-lastOAuth2Index)

	// Extract GitHub API calls only from the last OAuth2 marker onwards
	apiCallRegex := regexp.MustCompile(`GitHub API call completed.*`)
	var apiCalls []string

	for i := lastOAuth2Index; i < len(logLines); i++ {
		line := logLines[i]
		if apiCallRegex.MatchString(line) {
			apiCalls = append(apiCalls, strings.TrimSpace(line))
		}
	}

	g.Logger.Infof("Found %d GitHub API calls after the last OAuth2 initialization", len(apiCalls))

	// Log each API call for debugging
	for i, match := range apiCalls {
		g.Logger.Infof("API Call %d: %s", i+1, match)
	}

	// Check if we should output to file
	outputDir := os.Getenv("PAC_API_INSTRUMENTATION_DIR")
	if outputDir != "" {
		g.outputTestResultToFile(outputDir, apiCalls, lastOAuth2Index, len(logLines))
	}
}

// sanitizeFilename converts a test name to a safe filename.
func sanitizeFilename(name string) string {
	// Replace spaces and special characters with underscores
	re := regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	sanitized := re.ReplaceAllString(name, "_")
	// Convert to lowercase
	return strings.ToLower(sanitized)
}

// parseAPICallLog parses a GitHub API call log line and extracts structured data.
func parseAPICallLog(logLine string) *InstrumentationAPICall {
	// The log line contains a JSON object, so we need to extract and parse it
	// Example: "API Call 1: {\"level\":\"debug\",\"ts\":\"2025-08-05T16:12:17.508Z\",...}"

	// Find the JSON part after "API Call X: "
	jsonStart := strings.Index(logLine, "{")
	if jsonStart == -1 {
		return nil
	}

	jsonStr := logLine[jsonStart:]

	// Parse the JSON object
	var logEntry map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &logEntry); err != nil {
		return nil
	}

	call := &InstrumentationAPICall{}

	// Extract fields from the JSON
	if operation, ok := logEntry["operation"].(string); ok {
		call.Operation = operation
	}

	if durationMs, ok := logEntry["duration_ms"].(float64); ok {
		call.DurationMs = int64(durationMs)
	}

	if urlPath, ok := logEntry["url_path"].(string); ok {
		call.URLPath = urlPath
	}

	if rateLimit, ok := logEntry["rate_limit_remaining"].(string); ok {
		call.RateLimitRemaining = rateLimit
	}

	if statusCode, ok := logEntry["status_code"].(float64); ok {
		call.StatusCode = int(statusCode)
	}

	if provider, ok := logEntry["provider"].(string); ok {
		call.Provider = provider
	}

	if repo, ok := logEntry["repo"].(string); ok {
		call.Repo = repo
	}

	return call
}

// outputTestResultToFile writes the test result to a JSON file.
func (g *PRTest) outputTestResultToFile(outputDir string, apiCalls []string, lastOAuth2Index, totalLines int) {
	// Create output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		g.Logger.Warnf("Failed to create output directory %s: %v", outputDir, err)
		return
	}

	// Parse API calls into structured data
	var structuredCalls []InstrumentationAPICall
	for _, call := range apiCalls {
		if parsed := parseAPICallLog(call); parsed != nil {
			structuredCalls = append(structuredCalls, *parsed)
		}
	}

	// Create test result
	controllerName := "controller"
	if g.GHE {
		controllerName = "ghe-controller"
	}

	result := TestResult{
		TestName:         g.Label,
		Timestamp:        time.Now().Format(time.RFC3339),
		Controller:       controllerName,
		PRNumber:         g.PRNumber,
		SHA:              g.SHA,
		TargetNamespace:  g.TargetNamespace,
		GitHubAPICalls:   structuredCalls,
		TotalCalls:       len(structuredCalls),
		OAuth2MarkerLine: totalLines - lastOAuth2Index,
	}

	// Generate filename
	timestamp := time.Now().Format("2006-01-02T15-04-05")
	sanitizedTestName := sanitizeFilename(g.Label)
	filename := fmt.Sprintf("%s_%s.json", timestamp, sanitizedTestName)
	filePath := filepath.Join(outputDir, filename)

	// Write JSON file
	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		g.Logger.Warnf("Failed to marshal test result to JSON: %v", err)
		return
	}

	if err := os.WriteFile(filePath, jsonData, 0o600); err != nil {
		g.Logger.Warnf("Failed to write test result to file %s: %v", filePath, err)
		return
	}

	g.Logger.Infof("Test result written to: %s", filePath)
}
