package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/go-github/v85/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
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

// TektonDirResult holds the result of fetching tekton directory via GraphQL.
type TektonDirResult struct {
	SHA          string            // Resolved SHA
	FileContents map[string][]byte // YAML file contents by path (only .yaml/.yml blobs)
}

// buildTektonDirQuery constructs a GraphQL query for fetching tekton directory tree + blob contents.
// Uses the provided SHA to fetch the .tekton tree with inline blob contents.
func buildTektonDirQuery(sha, path string) (query string, variables map[string]any) {
	tektonExpr := sha + ":" + path

	query = `query($owner: String!, $name: String!, $tektonExpr: String!) {
  repository(owner: $owner, name: $name) {
    tektonTree: object(expression: $tektonExpr) {
      ... on Tree {
        entries {
          name
          type
          path
          oid
          object {
            ... on Blob {
              text
            }
          }
        }
      }
    }
  }
}`

	variables = map[string]any{
		"tektonExpr": tektonExpr,
	}

	return query, variables
}

// tektonDirResponse represents the GraphQL response structure for tekton directory fetch.
type tektonDirResponse struct {
	Data struct {
		Repository struct {
			TektonTree *struct {
				// Tree response has entries field
				Entries []struct {
					Name   string `json:"name"`
					Type   string `json:"type"`
					Path   string `json:"path"`
					Oid    string `json:"oid"`
					Object *struct {
						Text *string `json:"text"`
					} `json:"object,omitempty"`
				} `json:"entries,omitempty"`
				// Blob response has text field (indicates .tekton is a file, not directory)
				Text *string `json:"text,omitempty"`
			} `json:"tektonTree"`
		} `json:"repository"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors,omitempty"`
}

// fetchTektonDirGraphQL fetches the tekton directory tree + blob contents using GraphQL.
// Fetches the .tekton tree at the given SHA with inline blob contents in a single request.
func (c *graphQLClient) fetchTektonDirGraphQL(ctx context.Context, owner, repo, sha, path string) (*TektonDirResult, error) {
	query, variables := buildTektonDirQuery(sha, path)
	variables["owner"] = owner
	variables["name"] = repo

	requestBody := map[string]any{
		"query":     query,
		"variables": variables,
	}

	req, err := c.ghClient.NewRequest(http.MethodPost, c.endpoint, requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create GraphQL request: %w", err)
	}

	graphQLResp, resp, err := wrapAPI(c.provider, "graphql_get_tekton_dir", func() (tektonDirResponse, *github.Response, error) {
		var graphQLResp tektonDirResponse
		resp, err := c.ghClient.Do(ctx, req.WithContext(ctx), &graphQLResp)
		return graphQLResp, resp, err
	})
	if err != nil {
		return nil, fmt.Errorf("error fetching tekton directory using GraphQL: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GraphQL request failed with status %d: %w", resp.StatusCode, err)
	}

	if len(graphQLResp.Errors) > 0 {
		errorMsgs := make([]string, len(graphQLResp.Errors))
		for i, e := range graphQLResp.Errors {
			errorMsgs[i] = e.Message
		}
		if c.logger != nil {
			c.logger.Debugw("GraphQL tekton dir returned errors",
				"errors", strings.Join(errorMsgs, "; "),
			)
		}
		return nil, fmt.Errorf("GraphQL errors: %s", strings.Join(errorMsgs, "; "))
	}

	result := &TektonDirResult{
		SHA:          sha,
		FileContents: make(map[string][]byte),
	}

	// Check if tektonTree exists and what type it is
	if graphQLResp.Data.Repository.TektonTree != nil {
		// If TektonTree has Text field, it's a Blob (file), not a Tree (directory)
		if graphQLResp.Data.Repository.TektonTree.Text != nil {
			return nil, fmt.Errorf("%s has been found but is not a directory", path)
		}

		// Extract YAML blob contents from Tree entries (filter for .yaml/.yml files during parsing)
		for _, entry := range graphQLResp.Data.Repository.TektonTree.Entries {
			// Only process yaml/yml blobs
			if entry.Type != "blob" {
				continue
			}
			if !strings.HasSuffix(entry.Path, ".yaml") && !strings.HasSuffix(entry.Path, ".yml") {
				continue
			}

			// Extract blob content
			if entry.Object != nil && entry.Object.Text != nil {
				result.FileContents[entry.Path] = []byte(*entry.Object.Text)
			}
		}
	}

	return result, nil
}
