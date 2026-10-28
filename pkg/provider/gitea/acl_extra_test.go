package gitea

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	tgitea "github.com/tektoncd/pipelines-as-code/pkg/provider/gitea/test"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
)

func TestCheckPolicyAllowingSameOrgRepo(t *testing.T) {
	tests := []struct {
		name        string
		event       *info.Event
		wantAllowed bool
		wantReason  string
	}{
		{
			name: "organization repository allows sender",
			event: &info.Event{
				Organization: "personal",
				Repository:   "personal",
				Sender:       "sender",
			},
			wantAllowed: true,
			wantReason:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAllowed, gotReason := (&Provider{}).CheckPolicyAllowing(context.Background(), tt.event, []string{"team"})
			assert.Equal(t, tt.wantAllowed, gotAllowed)
			assert.Equal(t, tt.wantReason, gotReason)
		})
	}
}

func TestIsAllowedOwnersFileBranches(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, mux *http.ServeMux)
		wantAllowed bool
		wantErr     string
	}{
		{
			name: "missing owners file is skipped",
		},
		{
			name: "wrapped owners API error is treated as missing owners",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/repos/org/repo/contents/OWNERS", func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusInternalServerError)
				})
			},
		},
		{
			name: "wrapped owners aliases API error is treated as missing aliases",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				muxGiteaContents(t, mux, "OWNERS", "approvers:\n  - sender\n", http.StatusOK)
				mux.HandleFunc("/repos/org/repo/contents/OWNERS_ALIASES", func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusInternalServerError)
				})
			},
			wantAllowed: true,
		},
		{
			name: "owners file allows sender",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				muxGiteaContents(t, mux, "OWNERS", "approvers:\n  - sender\n", http.StatusOK)
			},
			wantAllowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, mux, teardown := tgitea.Setup(t)
			defer teardown()
			if tt.setup != nil {
				tt.setup(t, mux)
			}

			gotAllowed, err := (&Provider{giteaClient: client}).IsAllowedOwnersFile(context.Background(), &info.Event{
				Organization:  "org",
				Repository:    "repo",
				Sender:        "sender",
				DefaultBranch: "main",
				BaseBranch:    "main",
			})
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantAllowed, gotAllowed)
		})
	}
}

func TestAclCheckAllBranches(t *testing.T) {
	tests := []struct {
		name        string
		event       *info.Event
		setup       func(t *testing.T, mux *http.ServeMux)
		wantAllowed bool
		wantErr     string
	}{
		{
			name: "sender is organization",
			event: &info.Event{
				Organization: "sender",
				Repository:   "repo",
				Sender:       "sender",
			},
			wantAllowed: true,
		},
		{
			name: "collaborator API error",
			event: &info.Event{
				Organization: "org",
				Repository:   "repo",
				Sender:       "sender",
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/repos/org/repo/collaborators/sender/permission", func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusInternalServerError)
				})
			},
			wantErr: "500",
		},
		{
			name: "repository owner permission allows",
			event: &info.Event{
				Organization: "org",
				Repository:   "repo",
				Sender:       "sender",
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/repos/org/repo/collaborators/sender/permission", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"permission": "owner"}`)
				})
			},
			wantAllowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, mux, teardown := tgitea.Setup(t)
			defer teardown()
			if tt.setup != nil {
				tt.setup(t, mux)
			}

			core, _ := zapobserver.New(zap.InfoLevel)
			p := &Provider{
				giteaClient: client,
				Logger:      zap.New(core).Sugar(),
			}
			gotAllowed, err := p.aclCheckAll(context.Background(), tt.event)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantAllowed, gotAllowed)
		})
	}
}

func TestGetStringPullRequestCommentBranches(t *testing.T) {
	tests := []struct {
		name      string
		event     *info.Event
		setup     func(t *testing.T, mux *http.ServeMux)
		wantCount int
		wantErr   string
	}{
		{
			name: "invalid pull request URL",
			event: &info.Event{
				URL: "https://gitea.example.test/org/repo/pulls/not-a-number",
			},
			wantErr: "bad pull request number",
		},
		{
			name: "list comments API error",
			event: &info.Event{
				Organization: "org",
				Repository:   "repo",
				URL:          "https://gitea.example.test/org/repo/pulls/1",
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/repos/org/repo/issues/1/comments", func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusInternalServerError)
				})
			},
			wantErr: "500",
		},
		{
			name: "returns only matching comments",
			event: &info.Event{
				Organization: "org",
				Repository:   "repo",
				URL:          "https://gitea.example.test/org/repo/pulls/1",
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/repos/org/repo/issues/1/comments", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `[{"body":"/ok-to-test","user":{"login":"sender"}},{"body":"hello","user":{"login":"other"}}]`)
				})
			},
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, mux, teardown := tgitea.Setup(t)
			defer teardown()
			if tt.setup != nil {
				tt.setup(t, mux)
			}

			got, err := (&Provider{giteaClient: client}).GetStringPullRequestComment(context.Background(), tt.event, `(?m)^/ok-to-test$`)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantCount, len(got))
		})
	}
}

func muxGiteaContents(t *testing.T, mux *http.ServeMux, path, content string, statusCode int) {
	t.Helper()
	mux.HandleFunc(fmt.Sprintf("/repos/org/repo/contents/%s", path), func(rw http.ResponseWriter, _ *http.Request) {
		if statusCode != http.StatusOK {
			rw.WriteHeader(statusCode)
			return
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(content))
		fmt.Fprintf(rw, `{"type": "file", "content": %q}`, encoded)
	})
}
