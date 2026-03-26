package github

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/google/go-github/v84/github"
	providerMetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/providermetrics"
)

const (
	// Rate limit warning thresholds.
	rateLimitCritical = 50  // Warn when remaining calls < 50
	rateLimitWarning  = 100 // Warn when remaining calls < 100
	rateLimitInfo     = 500 // Info when remaining calls < 500
)

// checkRateLimit monitors GitHub API rate limits and logs warnings when limits are running low.
func (v *Provider) checkRateLimit(resp *github.Response) (remaining string) {
	if resp == nil || resp.Response == nil {
		return ""
	}

	// Extract rate limit information from headers (using canonical header keys)
	values := resp.Header[http.CanonicalHeaderKey("X-RateLimit-Remaining")]
	if len(values) == 0 {
		return ""
	}

	remaining = values[0]
	if remaining == "" {
		return remaining
	}

	// Parse remaining count
	remainingCount, err := strconv.Atoi(remaining)
	if err != nil {
		v.Logger.Debugf("Failed to parse rate limit remaining: %s", remaining)
		return remaining
	}

	// Get additional rate limit context
	limit := ""
	reset := ""
	if limitValues := resp.Header[http.CanonicalHeaderKey("X-RateLimit-Limit")]; len(limitValues) > 0 {
		limit = limitValues[0]
	}
	if resetValues := resp.Header[http.CanonicalHeaderKey("X-RateLimit-Reset")]; len(resetValues) > 0 {
		reset = resetValues[0]
		// Convert Unix timestamp to human readable time
		if resetTimestamp, err := strconv.ParseInt(reset, 10, 64); err == nil {
			resetTime := time.Unix(resetTimestamp, 0)
			reset = fmt.Sprintf("%s (%s)", reset, resetTime.Format("15:04:05 MST"))
		}
	}

	repoName := ""
	if v.repo != nil {
		repoName = fmt.Sprintf("%s/%s", v.repo.Namespace, v.repo.Name)
	}

	// Log warnings based on thresholds
	logFields := []any{
		"repo", repoName,
		"remaining", remainingCount,
		"limit", limit,
		"reset", reset,
	}
	switch {
	case remainingCount < rateLimitCritical:
		v.Logger.Errorw("GitHub API rate limit critically low", logFields...)
	case remainingCount < rateLimitWarning:
		v.Logger.Warnw("GitHub API rate limit running low", logFields...)
	case remainingCount < rateLimitInfo:
		v.Logger.Infow("GitHub API rate limit moderate", logFields...)
	}

	return remaining
}

// wrapAPI wraps a GitHub API call with logging, metrics, and operation context.
func wrapAPI[T any](v *Provider, operation string, call func() (T, *github.Response, error)) (T, *github.Response, error) {
	// This check ensures we only profile if a logger is available.
	if v.Logger == nil {
		return call()
	}

	start := time.Now()
	data, resp, err := call()
	duration := time.Since(start)

	v.logAPICall(operation, duration, resp, err)

	return data, resp, err
}

func (v *Provider) logAPICall(operation string, duration time.Duration, resp *github.Response, err error) {
	providerMetrics.RecordAPIUsage(v.Logger, v.providerName, v.triggerEvent, v.repo)

	// Build structured logging context
	logFields := []any{
		"operation", operation,
		"duration_ms", duration.Milliseconds(),
		"provider", "github",
	}

	// Add repository context if available
	if v.repo != nil {
		logFields = append(logFields, "repo", fmt.Sprintf("%s/%s", v.repo.Namespace, v.repo.Name))
	}

	// Add response context if available
	if resp != nil {
		remaining := v.checkRateLimit(resp)
		logFields = append(logFields,
			"url_path", resp.Request.URL.Path,
			"rate_limit_remaining", remaining,
			"github_request_id", resp.Header.Get("X-GitHub-Request-Id"),
		)
		if resp.StatusCode > 0 {
			logFields = append(logFields, "status_code", resp.StatusCode)
		}
	}

	// Log based on success/failure with appropriate level
	if err != nil {
		logFields = append(logFields, "error", err.Error())
		v.Logger.Errorw("GitHub API call failed", logFields...)
	} else {
		v.Logger.Debugw("GitHub API call completed", logFields...)
	}
}

// wrapAPIGetContents wraps the GetContents API call with operation context.
func wrapAPIGetContents(v *Provider, operation string, call func() (*github.RepositoryContent, []*github.RepositoryContent, *github.Response, error)) (*github.RepositoryContent, []*github.RepositoryContent, *github.Response, error) {
	// This check ensures we only profile if a logger is available.
	if v.Logger == nil {
		return call()
	}

	start := time.Now()
	file, dir, resp, err := call()
	duration := time.Since(start)

	v.logAPICall(operation, duration, resp, err)

	return file, dir, resp, err
}
