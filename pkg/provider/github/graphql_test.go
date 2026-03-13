package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v81/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

/* ---------------- helpers ---------------- */

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

func graphqlOK(repo map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4999")
		w.Header().Set("X-RateLimit-Reset", "1735689600")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"repository": repo},
		})
	}
}

func graphqlStatus(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4998")
		w.Header().Set("X-RateLimit-Reset", "1735689601")
		w.WriteHeader(code)
	}
}

func mockRepo(n int) map[string]any {
	repo := make(map[string]any)
	for i := range n {
		repo[fmt.Sprintf("file%d", i)] = map[string]any{
			"text": fmt.Sprintf("content-%d", i),
		}
	}
	return repo
}

/* ---------------- tests ---------------- */

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

func TestBuildGraphQLQuery(t *testing.T) {
	cases := []struct {
		name  string
		ref   string
		paths []string
		want  []string
	}{
		{
			name:  "two files",
			ref:   "main",
			paths: []string{"a", "b"},
			want:  []string{"query(", "repository(", "file0:", "file1:"},
		},
		{
			name:  "no files",
			ref:   "main",
			paths: nil,
			want:  []string{"query(", "repository("},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := buildGraphQLQuery(tc.ref, tc.paths)
			for _, s := range tc.want {
				assert.Check(t, strings.Contains(q, s))
			}
		})
	}
}

func TestFetchFilesBatch(t *testing.T) {
	cases := []struct {
		name    string
		paths   []string
		handler http.HandlerFunc
		want    int
		wantErr bool
	}{
		{
			name:    "single batch",
			paths:   []string{"a", "b"},
			handler: graphqlOK(mockRepo(2)),
			want:    2,
		},
		{
			name:    "http error",
			paths:   []string{"a"},
			handler: graphqlStatus(http.StatusNotFound),
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api/graphql", tc.handler)

			srv := withServer(t, mux)
			c, observedLogs := newTestGraphQLClient(t, srv.URL+"/api/v3/")

			res, err := c.fetchFilesBatch(context.Background(), "o", "r", "main", tc.paths)
			if tc.wantErr {
				assert.Assert(t, err != nil)
				entries := observedLogs.FilterMessage("GraphQL request returned non-200 status").All()
				assert.Check(t, cmp.Len(entries, 1))
				assert.Check(t, cmp.Equal(entries[0].ContextMap()["rate_limit"], "5000"))
				assert.Check(t, cmp.Equal(entries[0].ContextMap()["rate_limit_remaining"], "4998"))
				assert.Check(t, cmp.Equal(entries[0].ContextMap()["rate_limit_reset"], "1735689601"))
				return
			}

			assert.NilError(t, err)
			assert.Check(t, cmp.Len(res, tc.want))
			entries := observedLogs.FilterMessage("GraphQL batch fetch completed").All()
			assert.Check(t, cmp.Len(entries, 1))
			assert.Check(t, cmp.Equal(entries[0].ContextMap()["files_requested"], int64(len(tc.paths))))
			assert.Check(t, cmp.Equal(entries[0].ContextMap()["rate_limit"], "5000"))
			assert.Check(t, cmp.Equal(entries[0].ContextMap()["rate_limit_remaining"], "4999"))
			assert.Check(t, cmp.Equal(entries[0].ContextMap()["rate_limit_reset"], "1735689600"))
			_, exists := entries[0].ContextMap()["files_fetched"]
			assert.Check(t, !exists)
		})
	}
}

func TestFetchFilesBatchPreservesGitHubHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/graphql", func(w http.ResponseWriter, r *http.Request) {
		assert.Assert(t, r.Header.Get("User-Agent") != "")
		assert.Assert(t, r.Header.Get("X-GitHub-Api-Version") != "")
		assert.Check(t, cmp.Equal("application/json", r.Header.Get("Content-Type")))

		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"repository": map[string]any{
					"file0": map[string]any{"text": "content-0"},
				},
			},
		})
	})

	srv := withServer(t, mux)
	c, _ := newTestGraphQLClient(t, srv.URL+"/api/v3/")

	res, err := c.fetchFilesBatch(context.Background(), "o", "r", "main", []string{"a"})
	assert.NilError(t, err)
	assert.Check(t, cmp.Len(res, 1))
}
