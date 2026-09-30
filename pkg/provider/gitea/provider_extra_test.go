package gitea

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/test"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
)

func TestProviderAccessorsAndNoopMethods(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "get config",
			run: func(t *testing.T) {
				t.Helper()
				p := &Provider{giteaInstanceURL: "https://gitea.example.test"}
				got := p.GetConfig()
				assert.Equal(t, "gitea", got.Name)
				assert.Equal(t, "https://gitea.example.test", got.APIURL)
				assert.Equal(t, taskStatusTemplate, got.TaskStatusTMPL)
				assert.Equal(t, true, got.SkipEmoji)
			},
		},
		{
			name: "set gitea client",
			run: func(t *testing.T) {
				t.Helper()
				client, _, teardown := tgitea.Setup(t)
				defer teardown()

				p := &Provider{}
				p.SetGiteaClient(client)
				assert.Assert(t, p.giteaClient == client)
			},
		},
		{
			name: "set pac info",
			run: func(t *testing.T) {
				t.Helper()
				pacInfo := &info.PacOpts{}
				p := &Provider{}
				p.SetPacInfo(pacInfo)
				assert.Assert(t, p.pacInfo == pacInfo)
			},
		},
		{
			name: "set logger",
			run: func(t *testing.T) {
				t.Helper()
				core, _ := zapobserver.New(zap.InfoLevel)
				logger := zap.New(core).Sugar()
				p := &Provider{}
				p.SetLogger(logger)
				assert.Assert(t, p.Logger == logger)
			},
		},
		{
			name: "get default clock",
			run: func(t *testing.T) {
				t.Helper()
				p := &Provider{}
				assert.Assert(t, p.getClock() != nil)
			},
		},
		{
			name: "get configured clock",
			run: func(t *testing.T) {
				t.Helper()
				fakeClock := clockwork.NewFakeClock()
				p := &Provider{clock: fakeClock}
				assert.Assert(t, p.getClock() == fakeClock)
			},
		},
		{
			name: "create token",
			run: func(t *testing.T) {
				t.Helper()
				got, err := (&Provider{}).CreateToken(context.Background(), []string{"repo"}, info.NewEvent())
				assert.NilError(t, err)
				assert.Equal(t, "", got)
			},
		},
		{
			name: "get template",
			run: func(t *testing.T) {
				t.Helper()
				got := (&Provider{}).GetTemplate(provider.StartingPipelineType)
				assert.Equal(t, provider.GetHTMLTemplate(provider.StartingPipelineType), got)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestProviderSetClient(t *testing.T) {
	tests := []struct {
		name             string
		event            *info.Event
		password         string
		repo             *v1alpha1.Repository
		setup            func(t *testing.T, mux *http.ServeMux, gotHeaders *http.Header)
		wantAuthPrefix   string
		wantAuthContains string
		wantUserAgent    string
		wantErr          string
		noServer         bool
	}{
		{
			name: "token auth",
			event: &info.Event{
				EventType: "pull_request",
				Provider: &info.Provider{
					Token: "secret-token",
				},
			},
			wantAuthContains: "secret-token",
		},
		{
			name: "basic auth",
			event: &info.Event{
				EventType: "push",
				Provider: &info.Provider{
					User: "pac-user",
				},
			},
			password:       "password",
			wantAuthPrefix: "Basic ",
		},
		{
			name: "custom user agent",
			event: &info.Event{
				EventType: "pull_request",
				Provider: &info.Provider{
					Token: "secret-token",
				},
			},
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						Forgejo: &v1alpha1.ForgejoSettings{
							UserAgent: "custom-forgejo-agent",
						},
					},
				},
			},
			wantUserAgent: "custom-forgejo-agent",
		},
		{
			name: "missing token",
			event: &info.Event{
				Provider: &info.Provider{},
			},
			wantErr:  "no git_provider.secret has been set",
			noServer: true,
		},
		{
			name: "invalid URL",
			event: &info.Event{
				Provider: &info.Provider{
					Token: "secret-token",
					URL:   "http://[::1",
				},
			},
			wantErr:  "missing ']' in host",
			noServer: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, observer := zapobserver.New(zap.InfoLevel)
			logger := zap.New(core).Sugar()
			run := params.New()
			emitter := events.NewEventEmitter(nil, logger)
			gotHeaders := http.Header{}

			if !tt.noServer {
				mux := http.NewServeMux()
				mux.HandleFunc("/version", func(rw http.ResponseWriter, r *http.Request) {
					gotHeaders = r.Header.Clone()
					fmt.Fprint(rw, `{"version": "1.17.0"}`)
				})
				apiHandler := http.NewServeMux()
				apiHandler.Handle("/api/v1/", http.StripPrefix("/api/v1", mux))
				server := httptest.NewServer(apiHandler)
				defer server.Close()
				tt.event.Provider.URL = server.URL
			}

			p := &Provider{
				Logger:   logger,
				Password: tt.password,
			}
			err := p.SetClient(context.Background(), run, tt.event, tt.repo, emitter)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, p.giteaClient != nil)
			assert.Equal(t, tt.event.Provider.URL, p.giteaInstanceURL)
			assert.Assert(t, p.run == run)
			assert.Assert(t, p.eventEmitter == emitter)
			assert.Assert(t, p.repo == tt.repo)
			assert.Equal(t, tt.event.EventType, p.triggerEvent)

			if tt.wantAuthPrefix != "" {
				assert.Assert(t, strings.HasPrefix(gotHeaders.Get("Authorization"), tt.wantAuthPrefix))
			}
			if tt.wantAuthContains != "" {
				assert.Assert(t, strings.Contains(gotHeaders.Get("Authorization"), tt.wantAuthContains))
			}
			if tt.wantUserAgent != "" {
				assert.Equal(t, tt.wantUserAgent, gotHeaders.Get("User-Agent"))
			}

			logs := observer.TakeAll()
			assert.Assert(t, len(logs) > 0)
			assert.Assert(t, strings.Contains(logs[0].Message, "gitea: initialized API client"))
		})
	}
}

func TestShouldGetNextPage(t *testing.T) {
	tests := []struct {
		name            string
		header          http.Header
		currentPage     int
		wantNextPage    bool
		wantCurrentPage int
	}{
		{
			name:            "missing page count",
			header:          http.Header{},
			currentPage:     1,
			wantNextPage:    false,
			wantCurrentPage: 0,
		},
		{
			name: "invalid page count",
			header: http.Header{
				"X-Pagecount": []string{"not-a-number"},
			},
			currentPage:     1,
			wantNextPage:    false,
			wantCurrentPage: 0,
		},
		{
			name: "last page reached",
			header: http.Header{
				"X-Pagecount": []string{"2"},
			},
			currentPage:     2,
			wantNextPage:    false,
			wantCurrentPage: 2,
		},
		{
			name: "next page requested",
			header: http.Header{
				"X-Pagecount": []string{"3"},
			},
			currentPage:     2,
			wantNextPage:    true,
			wantCurrentPage: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNextPage, gotCurrentPage := ShouldGetNextPage(&forgejo.Response{
				Response: &http.Response{Header: tt.header},
			}, tt.currentPage)
			assert.Equal(t, tt.wantNextPage, gotNextPage)
			assert.Equal(t, tt.wantCurrentPage, gotCurrentPage)
		})
	}
}

func TestConcatAllYamlFilesBranches(t *testing.T) {
	tests := []struct {
		name     string
		objects  []forgejo.GitEntry
		setup    func(t *testing.T, mux *http.ServeMux)
		want     string
		wantErr  string
		wantJoin bool
	}{
		{
			name: "joins yaml files and inserts separator",
			objects: []forgejo.GitEntry{
				{Path: "README.md", SHA: "readme"},
				{Path: "first.yaml", SHA: "first"},
				{Path: "second.yml", SHA: "second"},
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				muxGiteaBlob(t, mux, "first", http.StatusOK, base64.StdEncoding.EncodeToString([]byte("apiVersion: v1\nkind: ConfigMap\n")))
				muxGiteaBlob(t, mux, "second", http.StatusOK, base64.StdEncoding.EncodeToString([]byte("apiVersion: v1\nkind: ConfigMap\n")))
			},
			want:     "apiVersion: v1",
			wantJoin: true,
		},
		{
			name: "blob API error",
			objects: []forgejo.GitEntry{
				{Path: "bad.yaml", SHA: "bad"},
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				muxGiteaBlob(t, mux, "bad", http.StatusInternalServerError, "")
			},
			wantErr: "500",
		},
		{
			name: "invalid blob base64",
			objects: []forgejo.GitEntry{
				{Path: "bad.yaml", SHA: "bad"},
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				muxGiteaBlob(t, mux, "bad", http.StatusOK, "not base64")
			},
			wantErr: "illegal base64",
		},
		{
			name: "invalid yaml",
			objects: []forgejo.GitEntry{
				{Path: "bad.yaml", SHA: "bad"},
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				muxGiteaBlob(t, mux, "bad", http.StatusOK, base64.StdEncoding.EncodeToString([]byte("foo: [\n")))
			},
			wantErr: "error unmarshalling yaml file bad.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, mux, teardown := tgitea.Setup(t)
			defer teardown()
			tt.setup(t, mux)

			p := &Provider{giteaClient: client}
			got, err := p.concatAllYamlFiles(tt.objects, &info.Event{
				Organization: "org",
				Repository:   "repo",
			})
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, strings.Contains(got, tt.want), "expected %q to contain %q", got, tt.want)
			if tt.wantJoin {
				assert.Assert(t, strings.Contains(got, "\n---"), "expected joined yaml to include document separator: %q", got)
			}
		})
	}
}

func TestGetTektonDirBranches(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, mux *http.ServeMux)
		want    string
		wantErr string
	}{
		{
			name: "missing tekton directory returns empty content",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				muxGiteaTree(t, mux, "root", http.StatusOK, []forgejo.GitEntry{{Path: "docs", Type: "tree", SHA: "docs"}})
			},
		},
		{
			name: "tekton path is not directory",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				muxGiteaTree(t, mux, "root", http.StatusOK, []forgejo.GitEntry{{Path: ".tekton", Type: "blob", SHA: "file"}})
			},
			wantErr: ".tekton has been found but is not a directory",
		},
		{
			name: "root tree API error",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				muxGiteaTree(t, mux, "root", http.StatusInternalServerError, nil)
			},
			wantErr: "500",
		},
		{
			name: "recursive tree API error",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				muxGiteaTree(t, mux, "root", http.StatusOK, []forgejo.GitEntry{{Path: ".tekton", Type: "tree", SHA: "dir"}})
				muxGiteaTree(t, mux, "dir", http.StatusInternalServerError, nil)
			},
			wantErr: "500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, mux, teardown := tgitea.Setup(t)
			defer teardown()
			tt.setup(t, mux)

			core, _ := zapobserver.New(zap.InfoLevel)
			p := &Provider{
				giteaClient: client,
				Logger:      zap.New(core).Sugar(),
			}
			got, err := p.GetTektonDir(context.Background(), &info.Event{
				Organization: "org",
				Repository:   "repo",
				SHA:          "root",
			}, ".tekton", "")
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFetchChangedFilesErrors(t *testing.T) {
	tests := []struct {
		name     string
		event    *info.Event
		setup    func(t *testing.T, mux *http.ServeMux)
		wantErr  string
		wantFile string
	}{
		{
			name: "pull request API error",
			event: &info.Event{
				Organization:      "org",
				Repository:        "repo",
				PullRequestNumber: 1,
				TriggerTarget:     "pull_request",
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/repos/org/repo/pulls/1/files", func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusInternalServerError)
				})
			},
			wantErr: "500",
		},
		{
			name: "push invalid JSON",
			event: &info.Event{
				TriggerTarget: "push",
				Request:       &info.Request{Payload: []byte("not-json")},
			},
			wantErr: "failed to unmarshal the push payload",
		},
		{
			name: "unknown trigger target",
			event: &info.Event{
				TriggerTarget: "unknown",
			},
			wantErr: "Unknown trigger type",
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
			_, err := p.fetchChangedFiles(context.Background(), tt.event)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestFormatPipelineCommentAdditionalEmoji(t *testing.T) {
	tests := []struct {
		name       string
		conclusion providerstatus.Conclusion
		wantEmoji  string
	}{
		{
			name:       "cancelled conclusion",
			conclusion: providerstatus.ConclusionCancelled,
			wantEmoji:  "⚠️",
		},
		{
			name:       "success conclusion",
			conclusion: providerstatus.ConclusionSuccess,
			wantEmoji:  "✅",
		},
		{
			name:       "neutral conclusion",
			conclusion: providerstatus.ConclusionNeutral,
			wantEmoji:  "ℹ️",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Provider{pacInfo: &info.PacOpts{}}
			got := p.formatPipelineComment("abc123", providerstatus.StatusOpts{
				Conclusion:              tt.conclusion,
				Title:                   "Title",
				OriginalPipelineRunName: "pipeline",
				Text:                    "details",
				DetailsURL:              "https://example.test/log",
			})
			assert.Assert(t, strings.HasPrefix(got, tt.wantEmoji+" "), "expected prefix %q in %q", tt.wantEmoji, got)
		})
	}
}

func muxGiteaBlob(t *testing.T, mux *http.ServeMux, sha string, statusCode int, content string) {
	t.Helper()
	mux.HandleFunc(fmt.Sprintf("/repos/org/repo/git/blobs/%s", sha), func(rw http.ResponseWriter, _ *http.Request) {
		if statusCode != http.StatusOK {
			rw.WriteHeader(statusCode)
			return
		}
		fmt.Fprintf(rw, `{"sha": %q, "encoding": "base64", "content": %q}`, sha, content)
	})
}

func muxGiteaTree(t *testing.T, mux *http.ServeMux, sha string, statusCode int, entries []forgejo.GitEntry) {
	t.Helper()
	mux.HandleFunc(fmt.Sprintf("/repos/org/repo/git/trees/%s", sha), func(rw http.ResponseWriter, _ *http.Request) {
		if statusCode != http.StatusOK {
			rw.WriteHeader(statusCode)
			return
		}
		body := forgejo.GitTreeResponse{
			SHA:     sha,
			Entries: entries,
		}
		b, err := json.Marshal(body)
		assert.NilError(t, err)
		fmt.Fprint(rw, string(b))
	})
}
