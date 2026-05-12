package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v85/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newTestProvider(baseURL string) (*Provider, *zapobserver.ObservedLogs) {
	client := github.NewClient(nil)
	parsed, _ := url.Parse(baseURL)
	client.BaseURL = parsed

	core, observedLogs := zapobserver.New(zap.DebugLevel)
	logger := zap.New(core).Sugar()

	return &Provider{
		ghClient:     client,
		Logger:       logger,
		providerName: "github",
		triggerEvent: "push",
		repo: &v1alpha1.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-ns",
				Name:      "test-repo",
			},
		},
	}, observedLogs
}

func newTestGraphQLClient(t *testing.T, baseURL string) (*graphQLClient, *zapobserver.ObservedLogs) {
	t.Helper()
	provider, observedLogs := newTestProvider(baseURL)
	c, err := newGraphQLClient(provider)
	assert.NilError(t, err)
	return c, observedLogs
}

func withServer(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func TestBuildGraphQLEndpoint(t *testing.T) {
	cases := []struct {
		name string
		base string
		want string
	}{
		{"public", "https://api.github.com", "https://api.github.com/graphql"},
		{"public slash", "https://api.github.com/", "https://api.github.com/graphql"},
		{"ghe v3", "https://ghe/x/api/v3", "https://ghe/x/api/graphql"},
		{"ghe v3 slash", "https://ghe/x/api/v3/", "https://ghe/x/api/graphql"},
		{"ghe root", "https://ghe", "https://ghe/api/graphql"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := github.NewClient(nil)
			parsed, _ := url.Parse(tc.base)
			client.BaseURL = parsed

			got, err := buildGraphQLEndpoint(&Provider{ghClient: client})
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(tc.want, got))
		})
	}
}

func TestBuildTektonDirQuery(t *testing.T) {
	cases := []struct {
		name     string
		sha      string
		path     string
		wantExpr string
	}{
		{
			name:     "simple path",
			sha:      "abc123",
			path:     ".tekton",
			wantExpr: "abc123:.tekton",
		},
		{
			name:     "nested path",
			sha:      "def456",
			path:     ".tekton/pipelines",
			wantExpr: "def456:.tekton/pipelines",
		},
		{
			name:     "branch name as sha",
			sha:      "main",
			path:     ".tekton",
			wantExpr: "main:.tekton",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, vars := buildTektonDirQuery(tc.sha, tc.path)

			// Verify query contains key GraphQL structure
			assert.Check(t, cmp.Contains(query, "query("))
			assert.Check(t, cmp.Contains(query, "repository("))
			assert.Check(t, cmp.Contains(query, "tektonTree:"))
			assert.Check(t, cmp.Contains(query, "object(expression:"))
			assert.Check(t, cmp.Contains(query, "... on Tree"))
			assert.Check(t, cmp.Contains(query, "entries"))
			assert.Check(t, cmp.Contains(query, "... on Blob"))
			assert.Check(t, cmp.Contains(query, "text"))

			// Verify variables
			assert.Check(t, cmp.Equal(tc.wantExpr, vars["tektonExpr"]))
		})
	}
}

func TestFetchTektonDirGraphQL(t *testing.T) {
	cases := []struct {
		name        string
		handler     http.HandlerFunc
		wantFiles   int
		wantErr     bool
		errContains string
	}{
		{
			name: "http error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-RateLimit-Limit", "5000")
				w.Header().Set("X-RateLimit-Remaining", "4997")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("GraphQL endpoint not available"))
			},
			wantErr:     true,
			errContains: "GraphQL request failed with status 404",
		},
		{
			name: "graphql errors",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-RateLimit-Limit", "5000")
				w.Header().Set("X-RateLimit-Remaining", "4996")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"errors": []map[string]any{
						{"message": "Field 'tektonTree' doesn't exist"},
					},
				})
			},
			wantErr:     true,
			errContains: "GraphQL errors",
		},
		{
			name: "null blob content",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-RateLimit-Limit", "5000")
				w.Header().Set("X-RateLimit-Remaining", "4992")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": map[string]any{
						"repository": map[string]any{
							"tektonTree": map[string]any{
								"entries": []map[string]any{
									{
										"name": "empty.yaml",
										"type": "blob",
										"path": "empty.yaml",
										"oid":  "abc123",
										"object": map[string]any{
											"text": nil, // Null content
										},
									},
									{
										"name": "valid.yaml",
										"type": "blob",
										"path": "valid.yaml",
										"oid":  "def456",
										"object": map[string]any{
											"text": "kind: Pipeline",
										},
									},
								},
							},
						},
					},
				})
			},
			wantFiles: 1, // Only valid.yaml counted
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/graphql", tc.handler)

			srv := withServer(t, mux)
			c, observedLogs := newTestGraphQLClient(t, srv.URL+"/api/v3/")

			result, err := c.fetchTektonDirGraphQL(context.Background(), "owner", "repo", "abc123", ".tekton")

			if tc.wantErr {
				assert.Assert(t, err != nil)
				if tc.errContains != "" {
					assert.Check(t, cmp.Contains(err.Error(), tc.errContains))
				}
				return
			}

			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(tc.wantFiles, len(result.FileContents)))
			assert.Check(t, cmp.Equal("abc123", result.SHA))

			// Verify API call logging from wrapAPI
			entries := observedLogs.FilterMessage("GitHub API call completed").All()
			assert.Check(t, cmp.Len(entries, 1))
			assert.Check(t, cmp.Equal(entries[0].ContextMap()["operation"], "graphql_get_tekton_dir"))
		})
	}
}

func TestFetchTektonDirGraphQLPreservesGitHubHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/graphql", func(w http.ResponseWriter, r *http.Request) {
		// Verify GitHub client headers are preserved
		assert.Assert(t, r.Header.Get("User-Agent") != "")
		assert.Assert(t, r.Header.Get("X-GitHub-Api-Version") != "")
		assert.Check(t, cmp.Equal("application/json", r.Header.Get("Content-Type")))

		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"repository": map[string]any{
					"tektonTree": map[string]any{
						"entries": []map[string]any{
							{
								"name":   "test.yaml",
								"type":   "blob",
								"path":   "test.yaml",
								"oid":    "abc",
								"object": map[string]any{"text": "test"},
							},
						},
					},
				},
			},
		})
	})

	srv := withServer(t, mux)
	c, _ := newTestGraphQLClient(t, srv.URL+"/api/v3/")

	_, err := c.fetchTektonDirGraphQL(context.Background(), "o", "r", "sha", ".tekton")
	assert.NilError(t, err)
}
