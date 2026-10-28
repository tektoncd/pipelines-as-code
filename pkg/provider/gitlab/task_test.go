package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	gl "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
)

func TestExtractGitLabInfo(t *testing.T) {
	tests := []struct {
		url      string
		name     string
		expected *gitLabInfo
	}{
		{
			name: "custom host",
			url:  "https://gitlab.chmouel.com/group/subgroup/repo/-/blob/main/README.md?ref_type=heads",
			expected: &gitLabInfo{
				Scheme:      "https",
				Host:        "gitlab.chmouel.com",
				GroupOrUser: "group/subgroup",
				Repository:  "repo",
				Revision:    "main",
				FilePath:    "README.md",
			},
		},
		{
			name: "org repo",
			url:  "https://gitlab.com/org/repo/-/blob/main/README.md",
			expected: &gitLabInfo{
				Scheme:      "https",
				Host:        "gitlab.com",
				GroupOrUser: "org",
				Repository:  "repo",
				Revision:    "main",
				FilePath:    "README.md",
			},
		},
		{
			name: "long group and subgroups",
			url:  "https://gitlab.com/gitlab-com/partners/alliance/corp/sandbox/another/foo-foo/-/raw/main/hello.txt?ref_type=heads",
			expected: &gitLabInfo{
				Scheme:      "https",
				Host:        "gitlab.com",
				GroupOrUser: "gitlab-com/partners/alliance/corp/sandbox/another",
				Repository:  "foo-foo",
				Revision:    "main",
				FilePath:    "hello.txt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := extractGitLabInfo(tt.url)
			assert.NilError(t, err)
			assert.DeepEqual(t, info, tt.expected)
		})
	}
}

func TestGetTaskURI(t *testing.T) {
	ctx := context.Background()
	expectedContent := "apiVersion: tekton.dev/v1beta1\nkind: Pipeline\nmetadata:\n  name: test-pipeline\nspec:\n  tasks:\n    - name: echo-task\n      taskSpec:\n        steps:\n          - name: echo\n            image: ubuntu\n            script: |\n              echo \"Hello from remote pipeline!\"\n"

	tests := []struct {
		name            string
		setup           func(t *testing.T) (*httptest.Server, string, string)
		wantFound       bool
		wantErr         bool
		wantErrContains string
		wantContent     string
	}{
		{
			name: "success",
			setup: func(t *testing.T) (*httptest.Server, string, string) {
				projectID := 12345
				projectSlug := "chmouel/dazgo"
				revision := "main"
				filePath := "task.yaml"

				mux := http.NewServeMux()
				mux.HandleFunc(fmt.Sprintf("/api/v4/projects/%s", gl.PathEscape(projectSlug)), func(w http.ResponseWriter, _ *http.Request) {
					fmt.Fprintf(w, `{"id": %d, "path_with_namespace": "%s"}`, projectID, projectSlug)
				})

				mux.HandleFunc(fmt.Sprintf("/api/v4/projects/%d/repository/files/%s/raw", projectID, gl.PathEscape(filePath)), func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "token", r.Header.Get("Private-Token"), "Expected Private-Token header to be 'token'")
					assert.Equal(t, revision, r.URL.Query().Get("ref"), "Expected 'ref' query parameter to be 'main'")
					fmt.Fprint(w, expectedContent)
				})

				server := httptest.NewServer(mux)
				remotePipelineURL := fmt.Sprintf("%s/%s/-/raw/%s/%s", server.URL, projectSlug, revision, filePath)
				return server, server.URL, remotePipelineURL
			},
			wantFound:   true,
			wantErr:     false,
			wantContent: expectedContent,
		},
		{
			name: "file not found (404)",
			setup: func(_ *testing.T) (*httptest.Server, string, string) {
				mux := http.NewServeMux()
				projectID := 12345
				projectSlug := "chmouel/dazgo"
				filePath := "nonexistent.yaml"

				mux.HandleFunc(fmt.Sprintf("/api/v4/projects/%s", gl.PathEscape(projectSlug)), func(w http.ResponseWriter, _ *http.Request) {
					fmt.Fprintf(w, `{"id": %d, "path_with_namespace": "%s"}`, projectID, projectSlug)
				})

				mux.HandleFunc(fmt.Sprintf("/api/v4/projects/%d/repository/files/%s/raw", projectID, gl.PathEscape(filePath)), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, `{"message": "404 File Not Found"}`)
				})

				server := httptest.NewServer(mux)
				remotePipelineURL := fmt.Sprintf("%s/%s/-/raw/main/%s", server.URL, projectSlug, filePath)
				return server, server.URL, remotePipelineURL
			},
			wantFound: false,
			wantErr:   false,
		},
		{
			name: "project not found (404)",
			setup: func(_ *testing.T) (*httptest.Server, string, string) {
				mux := http.NewServeMux()
				projectSlug := "nonexistent/project"

				mux.HandleFunc(fmt.Sprintf("/api/v4/projects/%s", gl.PathEscape(projectSlug)), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
					fmt.Fprint(w, `{"message": "404 Project Not Found"}`)
				})

				server := httptest.NewServer(mux)
				remotePipelineURL := fmt.Sprintf("%s/%s/-/raw/main/task.yaml", server.URL, projectSlug)
				return server, server.URL, remotePipelineURL
			},
			wantFound: false,
			wantErr:   false,
		},
		{
			name: "invalid gitlab URL format",
			setup: func(_ *testing.T) (*httptest.Server, string, string) {
				return nil, "https://example.com", "https://example.com/invalid/url/format"
			},
			wantFound:       false,
			wantErr:         true,
			wantErrContains: "URL does not match the expected GitLab pattern",
		},
		{
			name: "different host - should return not found",
			setup: func(_ *testing.T) (*httptest.Server, string, string) {
				return nil, "https://gitlab.com", "https://different-host.com/chmouel/dazgo/-/raw/main/task.yaml"
			},
			wantFound: false,
			wantErr:   false,
		},
		{
			name: "API error on GetProject",
			setup: func(_ *testing.T) (*httptest.Server, string, string) {
				mux := http.NewServeMux()
				projectSlug := "chmouel/dazgo"

				mux.HandleFunc(fmt.Sprintf("/api/v4/projects/%s", gl.PathEscape(projectSlug)), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, `{"message": "Internal Server Error"}`)
				})

				server := httptest.NewServer(mux)
				remotePipelineURL := fmt.Sprintf("%s/%s/-/raw/main/task.yaml", server.URL, projectSlug)
				return server, server.URL, remotePipelineURL
			},
			wantFound:       false,
			wantErr:         true,
			wantErrContains: "failed to get project ID",
		},
		{
			name: "API error on GetRawFile",
			setup: func(_ *testing.T) (*httptest.Server, string, string) {
				mux := http.NewServeMux()
				projectID := 12345
				projectSlug := "chmouel/dazgo"
				filePath := "task.yaml"

				mux.HandleFunc(fmt.Sprintf("/api/v4/projects/%s", gl.PathEscape(projectSlug)), func(w http.ResponseWriter, _ *http.Request) {
					fmt.Fprintf(w, `{"id": %d, "path_with_namespace": "%s"}`, projectID, projectSlug)
				})

				mux.HandleFunc(fmt.Sprintf("/api/v4/projects/%d/repository/files/%s/raw", projectID, gl.PathEscape(filePath)), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(w, `{"message": "Internal Server Error"}`)
				})

				server := httptest.NewServer(mux)
				remotePipelineURL := fmt.Sprintf("%s/%s/-/raw/main/%s", server.URL, projectSlug, filePath)
				return server, server.URL, remotePipelineURL
			},
			wantFound:       false,
			wantErr:         true,
			wantErrContains: "failed to get file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, eventURL, remotePipelineURL := tt.setup(t)
			if server != nil {
				defer server.Close()
			}

			v := &Provider{}
			event := info.NewEvent()
			event.URL = eventURL
			event.Provider = &info.Provider{
				Token: "token",
			}

			if server != nil {
				client, err := gl.NewClient("token", gl.WithBaseURL(server.URL), gl.WithoutRetries())
				assert.NilError(t, err)
				v.gitlabClient = client
			}

			found, content, err := v.GetTaskURI(ctx, event, remotePipelineURL)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
				if tt.wantErrContains != "" {
					assert.ErrorContains(t, err, tt.wantErrContains)
				}
			} else {
				assert.NilError(t, err)
			}

			assert.Equal(t, tt.wantFound, found)
			assert.Equal(t, tt.wantContent, content)
		})
	}
}
