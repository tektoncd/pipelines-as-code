package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/go-github/v81/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	providerMetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/providermetrics"
	"go.uber.org/zap"
)

// graphQLClient handles GraphQL API requests for fetching file contents.
type graphQLClient struct {
	httpClient   *http.Client
	ghClient     *github.Client
	endpoint     string
	logger       *zap.SugaredLogger
	provider     *Provider
	triggerEvent string
	repo         *v1alpha1.Repository
}

// newGraphQLClient creates a new GraphQL client from a GitHub provider.
func newGraphQLClient(p *Provider) (*graphQLClient, error) {
	httpClient := p.Client().Client()
	if httpClient == nil {
		return nil, fmt.Errorf("GitHub client HTTP client is nil")
	}

	endpoint, err := buildGraphQLEndpoint(p)
	if err != nil {
		return nil, fmt.Errorf("failed to build GraphQL endpoint: %w", err)
	}

	return &graphQLClient{
		httpClient:   httpClient,
		ghClient:     p.Client(),
		endpoint:     endpoint,
		logger:       p.Logger,
		provider:     p,
		triggerEvent: p.triggerEvent,
		repo:         p.repo,
	}, nil
}

// buildGraphQLEndpoint constructs the GraphQL API endpoint URL from the GitHub client's BaseURL.
func buildGraphQLEndpoint(p *Provider) (string, error) {
	baseURL := p.Client().BaseURL.String()
	baseURL = strings.TrimSuffix(baseURL, "/")

	// For GitHub.com, use standard GraphQL endpoint
	// apiPublicURL has a trailing slash which TrimSuffix above removes,
	// so compare directly with the slash-less form.
	if baseURL == "https://api.github.com" {
		return "https://api.github.com/graphql", nil
	}

	// For GHE and test servers, construct GraphQL endpoint from the base URL
	// BaseURL could be:
	//   - https://ghe.example.com/api/v3/ -> https://ghe.example.com/api/graphql
	//   - http://127.0.0.1:PORT/api/v3/ -> http://127.0.0.1:PORT/api/graphql
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("failed to parse BaseURL: %w", err)
	}

	// Replace /api/v3 with /api/graphql in the path
	path := parsedURL.Path
	if strings.HasSuffix(path, "/api/v3") || strings.HasSuffix(path, "/api/v3/") {
		path = strings.TrimSuffix(path, "/api/v3/")
		path = strings.TrimSuffix(path, "/api/v3")
		path += "/api/graphql"
	} else {
		// Fallback: just use the host with /api/graphql
		path = "/api/graphql"
	}

	parsedURL.Path = path
	return parsedURL.String(), nil
}

// buildGraphQLQuery constructs a GraphQL query string with aliases for batch fetching multiple files.
func buildGraphQLQuery(ref string, paths []string) string {
	// Escape ref for GraphQL string (escape quotes and backslashes)
	escapedRef := strings.ReplaceAll(ref, `\`, `\\`)
	escapedRef = strings.ReplaceAll(escapedRef, `"`, `\"`)

	aliases := make([]string, 0, len(paths))
	for i, path := range paths {
		// Escape path for GraphQL string (escape quotes and backslashes)
		escapedPath := strings.ReplaceAll(path, `\`, `\\`)
		escapedPath = strings.ReplaceAll(escapedPath, `"`, `\"`)
		aliases = append(aliases, fmt.Sprintf(`    file%d: object(expression: "%s:%s") {
      ... on Blob {
        text
      }
    }`, i, escapedRef, escapedPath))
	}

	query := fmt.Sprintf(`query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
%s
  }
}`, strings.Join(aliases, "\n"))

	return query
}

// graphQLResponse represents the structure of a GraphQL API response.
type graphQLResponse struct {
	Data struct {
		Repository map[string]struct {
			Text *string `json:"text"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

type rateLimitHeaders struct {
	limit     string
	remaining string
	reset     string
}

func getRateLimitHeaders(header http.Header) rateLimitHeaders {
	return rateLimitHeaders{
		limit:     header.Get("X-RateLimit-Limit"),
		remaining: header.Get("X-RateLimit-Remaining"),
		reset:     header.Get("X-RateLimit-Reset"),
	}
}

// fetchFiles fetches multiple file contents using GraphQL batch queries.
// Returns a map of path -> content.
func (c *graphQLClient) fetchFiles(ctx context.Context, owner, repo, ref string, paths []string) (map[string][]byte, error) {
	if len(paths) == 0 {
		return make(map[string][]byte), nil
	}

	// Limit batch size to avoid query complexity issues
	const maxBatchSize = 50
	result := make(map[string][]byte, len(paths))
	for start := 0; start < len(paths); start += maxBatchSize {
		end := min(start+maxBatchSize, len(paths))
		batch := paths[start:end]
		batchResult, err := c.fetchFilesBatch(ctx, owner, repo, ref, batch)
		if err != nil {
			return nil, err
		}
		maps.Copy(result, batchResult)
	}

	return result, nil
}

// fetchFilesBatch fetches multiple file contents in a single GraphQL query.
// Returns a map of path -> content.
func (c *graphQLClient) fetchFilesBatch(ctx context.Context, owner, repo, ref string, paths []string) (map[string][]byte, error) {
	if len(paths) == 0 {
		return make(map[string][]byte), nil
	}

	query := buildGraphQLQuery(ref, paths)
	variables := map[string]any{
		"owner": owner,
		"name":  repo,
	}

	requestBody := map[string]any{
		"query":     query,
		"variables": variables,
	}

	req, err := c.ghClient.NewRequest(http.MethodPost, c.endpoint, requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create GraphQL request: %w", err)
	}
	req = req.WithContext(ctx)

	// Record metrics for GraphQL API call
	if c.logger != nil {
		providerMetrics.RecordAPIUsage(c.logger, c.provider.providerName, c.triggerEvent, c.repo)
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(start)

	if err != nil {
		if c.logger != nil {
			c.logger.Debugw("GraphQL request failed",
				"error", err.Error(),
				"duration_ms", duration.Milliseconds(),
			)
		}
		return nil, fmt.Errorf("GraphQL request failed: %w", err)
	}
	defer resp.Body.Close()

	rateLimit := getRateLimitHeaders(resp.Header)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read GraphQL response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		if c.logger != nil {
			c.logger.Debugw("GraphQL request returned non-200 status",
				"status_code", resp.StatusCode,
				"response", string(body),
				"rate_limit", rateLimit.limit,
				"rate_limit_remaining", rateLimit.remaining,
				"rate_limit_reset", rateLimit.reset,
			)
		}
		return nil, fmt.Errorf("GraphQL request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var graphQLResp graphQLResponse
	if err := json.Unmarshal(body, &graphQLResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal GraphQL response: %w", err)
	}

	// Check for GraphQL errors
	if len(graphQLResp.Errors) > 0 {
		errorMsgs := make([]string, len(graphQLResp.Errors))
		for i, e := range graphQLResp.Errors {
			errorMsgs[i] = e.Message
		}
		if c.logger != nil {
			c.logger.Debugw("GraphQL returned errors",
				"errors", strings.Join(errorMsgs, "; "),
			)
		}
		return nil, fmt.Errorf("GraphQL errors: %s", strings.Join(errorMsgs, "; "))
	}

	// Extract file contents from response
	result := make(map[string][]byte, len(paths))
	for i, path := range paths {
		alias := fmt.Sprintf("file%d", i)
		blobData, ok := graphQLResp.Data.Repository[alias]
		if !ok {
			return nil, fmt.Errorf("file %s (alias %s) not found in GraphQL response", path, alias)
		}

		if blobData.Text == nil {
			return nil, fmt.Errorf("file %s returned null content (may be binary)", path)
		}

		result[path] = []byte(*blobData.Text)
	}

	if c.logger != nil {
		c.logger.Debugw("GraphQL batch fetch completed",
			"files_requested", len(paths),
			"duration_ms", duration.Milliseconds(),
			"rate_limit", rateLimit.limit,
			"rate_limit_remaining", rateLimit.remaining,
			"rate_limit_reset", rateLimit.reset,
		)
	}

	return result, nil
}
