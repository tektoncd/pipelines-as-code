package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/ptr"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestGetTaskURI(t *testing.T) {
	tests := []struct {
		name       string
		wantErr    bool
		disallowed bool
		eventURL   string
		uri        string
		ret        string
	}{
		{
			name:     "Get Task URI",
			eventURL: "https://foo.com/owner/repo/pull/1",
			uri:      "https://foo.com/owner/repo/blob/main/file",
			wantErr:  false,
			ret:      "hello world",
		},
		{
			name:       "not comparable host",
			eventURL:   "https://foo/owner/repo/pull/1",
			uri:        "https://bar/owner/repo/blob/main/file",
			disallowed: true,
		},
		{
			name:       "bad uri",
			eventURL:   "https://foo/owner/repo/pull/1",
			uri:        "https://foo/owner/aooaooadoodao",
			disallowed: true,
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sha := "sha"
			content := base64.StdEncoding.EncodeToString([]byte(tt.ret))
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			provider := &Provider{ghClient: fakeclient}
			event := info.NewEvent()
			event.HeadBranch = "main"
			event.URL = tt.eventURL
			mux.HandleFunc("/repos/owner/repo/contents/file", func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(rw, `{"sha": "%s"}`, sha)
			})
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/git/blobs/%s", "owner", "repo", sha), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(rw, `{"content": "%s"}`, content)
			})
			allowed, content, err := provider.GetTaskURI(ctx, event, tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetTaskURI() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.disallowed && allowed {
				t.Errorf("GetTaskURI() is allowed and we want it to be disallowed")
				return
			} else if !tt.disallowed {
				return
			}
			if content != tt.ret {
				t.Errorf("GetTaskURI() got = %v, want %v", content, tt.ret)
			}
		})
	}
}

func TestGithubSplitURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantOrg  string
		wantRepo string
		wantRef  string
		wantPath string
		gheURL   string
		wantErr  bool
	}{
		{
			name:     "Split URL",
			url:      "https://github.com/openshift-pipelines/pipelines-as-code/blob/main/testdatas/remote_task.yaml",
			wantOrg:  "openshift-pipelines",
			wantRepo: "pipelines-as-code",
			wantRef:  "main",
			wantPath: "testdatas/remote_task.yaml",
		},
		{
			name:     "Split URL with slash in branch",
			url:      "https://github.com/openshift-pipelines/pipelines-as-code/blob/feature%2Fbranch/testdatas/remote_task.yaml",
			wantOrg:  "openshift-pipelines",
			wantRepo: "pipelines-as-code",
			wantRef:  "feature/branch",
			wantPath: "testdatas/remote_task.yaml",
		},
		{
			name:     "Split URL with encoding emoji in branch",
			url:      "https://github.com/openshift-pipelines/pipelines-as-code/blob/%F0%9F%99%83/filename.yaml",
			wantOrg:  "openshift-pipelines",
			wantRepo: "pipelines-as-code",
			wantRef:  "🙃",
			wantPath: "filename.yaml",
		},
		{
			name:     "Split URL with url encoding emoji in filename",
			url:      "https://github.com/openshift-pipelines/pipelines-as-code/blob/branch/anemoji%F0%9F%99%83.yaml",
			wantOrg:  "openshift-pipelines",
			wantRepo: "pipelines-as-code",
			wantRef:  "branch",
			wantPath: "anemoji🙃.yaml",
		},
		{
			name:     "Split raw URL",
			url:      "https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/main/testdatas/remote_task.yaml",
			wantOrg:  "openshift-pipelines",
			wantRepo: "pipelines-as-code",
			wantRef:  "main",
			wantPath: "testdatas/remote_task.yaml",
		},
		{
			name:     "Split raw URL2",
			url:      "https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/main/remote_task.yaml",
			wantOrg:  "openshift-pipelines",
			wantRepo: "pipelines-as-code",
			wantRef:  "main",
			wantPath: "remote_task.yaml",
		},
		{
			name:    "Too small URL",
			url:     "https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code",
			wantErr: true,
		},
		{
			name:    "Invalid no path URL",
			url:     "https://raw.githubusercontent.com/openshift-pipelines/pipelines-as-code/main",
			wantErr: true,
		},
		{
			name:     "raw GHE URL",
			url:      "https://raw.ghe.domain.com/owner/repo/branch/file?token=TOKEN",
			gheURL:   "https://ghe.domain.com",
			wantOrg:  "owner",
			wantRepo: "repo",
			wantRef:  "branch",
			wantPath: "file",
		},
		{
			name:     "not matching ghe but allowed from public gh",
			url:      fmt.Sprintf("https://%s/owner/repo/branch/file?token=TOKEN", publicRawURLHost),
			gheURL:   "https://foo.com",
			wantOrg:  "owner",
			wantRepo: "repo",
			wantRef:  "branch",
			wantPath: "file",
		},
		{
			name:    "not matching raw",
			url:     "https://bar.com/owner/repo/branch/file?token=TOKEN",
			gheURL:  "https://foo.com",
			wantErr: true,
		},
		{
			name:    "not a full direct url",
			url:     "https://raw.ghe/owner/repo/branch?token=TOKEN",
			gheURL:  "https://raw.ghe",
			wantErr: true,
		},
		{
			name:    "bad formatted ghe url",
			url:     "https://raw.ghe/owner/repo/branch?token=TOKEN",
			gheURL:  "https:raw.ghe",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := info.NewEvent()
			event.GHEURL = tt.gheURL
			org, repo, path, ref, err := splitGithubURL(event, tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("SplitURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, tt.wantOrg, org)
			assert.Equal(t, tt.wantRepo, repo)
			assert.Equal(t, tt.wantRef, ref)
			assert.Equal(t, tt.wantPath, path)
		})
	}
}

func TestGetTektonDir(t *testing.T) {
	testGetTektonDir := []struct {
		treepath             string
		event                *info.Event
		name                 string
		expectedString       string
		provenance           string
		filterMessageSnippet string
		wantErr              string
		expectedGHApiCalls   int64
	}{
		{
			name: "test no subtree on pull request",
			event: &info.Event{
				Organization:  "tekton",
				Repository:    "cat",
				SHA:           "123",
				TriggerTarget: triggertype.PullRequest,
			},
			expectedString:       "PipelineRun",
			treepath:             "testdata/tree/simple",
			filterMessageSnippet: "Using PipelineRun definition from source pull_request tekton/cat#0",
			// 1. Get Repo root objects
			// 2. Get Tekton Dir objects
			// 3. GraphQL batch fetch for 2 files (replaces 2 REST calls)
			expectedGHApiCalls: 3,
		},
		{
			name: "test no subtree on push",
			event: &info.Event{
				Organization:  "tekton",
				Repository:    "cat",
				SHA:           "123",
				TriggerTarget: triggertype.Push,
			},
			expectedString:       "PipelineRun",
			treepath:             "testdata/tree/simple",
			filterMessageSnippet: "Using PipelineRun definition from source push",
			// 1. Get Repo root objects
			// 2. Get Tekton Dir objects
			// 3. GraphQL batch fetch for 2 files (replaces 2 REST calls)
			expectedGHApiCalls: 3,
		},
		{
			name: "test provenance default_branch ",
			event: &info.Event{
				Organization:  "tekton",
				Repository:    "cat",
				DefaultBranch: "main",
			},
			expectedString:       "FROMDEFAULTBRANCH",
			treepath:             "testdata/tree/defaultbranch",
			provenance:           "default_branch",
			filterMessageSnippet: "Using PipelineRun definition from default_branch: main",
			// 1. Resolve default branch to a commit SHA
			// 2. Get Repo root objects
			// 3. Get Tekton Dir objects
			// 4. GraphQL batch fetch for 2 files
			expectedGHApiCalls: 4,
		},
		{
			name: "test with subtree",
			event: &info.Event{
				Organization: "tekton",
				Repository:   "cat",
				SHA:          "123",
			},
			expectedString: "FROMSUBTREE",
			treepath:       "testdata/tree/subdir",
			// 1. Get Repo root objects
			// 2. Get Tekton Dir objects
			// 3-5. Get object content for each object (foo/bar/pipeline.yaml)
			expectedGHApiCalls: 3,
		},
		{
			name: "test with badly formatted yaml",
			event: &info.Event{
				Organization: "tekton",
				Repository:   "cat",
				SHA:          "123",
			},
			expectedString: "FROMSUBTREE",
			treepath:       "testdata/tree/badyaml",
			wantErr:        "error unmarshalling yaml file badyaml.yaml: yaml: line 2: did not find expected key",
			// 1. Get Repo root objects
			// 2. Get Tekton Dir objects
			// 3. Get object content for object (badyaml.yaml)
			expectedGHApiCalls: 3,
		},
		{
			name: "test no tekton directory",
			event: &info.Event{
				Organization:  "tekton",
				Repository:    "cat",
				SHA:           "123",
				TriggerTarget: triggertype.PullRequest,
			},
			expectedString:       "",
			treepath:             "testdata/tree/notektondir",
			filterMessageSnippet: "Using PipelineRun definition from source pull_request tekton/cat#0",
			// 1. Get Repo root objects
			// _. No tekton dir to fetch
			expectedGHApiCalls: 1,
		},
		{
			name: "test tekton directory path is file",
			event: &info.Event{
				Organization: "tekton",
				Repository:   "cat",
				SHA:          "123",
			},
			treepath: "testdata/tree/tektondirisfile",
			wantErr:  ".tekton has been found but is not a directory",
			// 1. Get Repo root objects
			// _. Tekton dir is file, no directory to fetch
			expectedGHApiCalls: 1,
		},
	}
	for _, tt := range testGetTektonDir {
		t.Run(tt.name, func(t *testing.T) {
			prmetrics.ResetRecorder()
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(provider)

			observer, exporter := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			gvcs := Provider{
				ghClient:     fakeclient,
				providerName: "github",
				Logger:       fakelogger,
			}

			shaDir := fmt.Sprintf("%x", sha256.Sum256([]byte(tt.treepath)))
			tt.event.SHA = shaDir
			if tt.provenance == "default_branch" {
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/branches/%s",
					tt.event.Organization, tt.event.Repository, tt.event.DefaultBranch),
					func(rw http.ResponseWriter, _ *http.Request) {
						branch := &github.Branch{
							Name: github.Ptr(tt.event.DefaultBranch),
							Commit: &github.RepositoryCommit{
								SHA: github.Ptr(shaDir),
							},
						}
						b, _ := json.Marshal(branch)
						fmt.Fprint(rw, string(b))
					})
			}
			ghtesthelper.SetupGitTree(t, mux, tt.treepath, tt.event, false)

			got, err := gvcs.GetTektonDir(ctx, tt.event, ".tekton", tt.provenance)
			if tt.wantErr != "" {
				assert.Assert(t, err != nil, "we should have get an error here")
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)

			var rm metricdata.ResourceMetrics
			err = reader.Collect(ctx, &rm)
			assert.NilError(t, err, "error collecting metrics")

			assert.Equal(t, len(rm.ScopeMetrics), 1)
			assert.Equal(t, len(rm.ScopeMetrics[0].Metrics), 1)
			assert.Equal(t, rm.ScopeMetrics[0].Metrics[0].Name, "pipelines_as_code_git_provider_api_request_count")
			count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
			assert.Assert(t, ok)
			assert.Equal(t, count.DataPoints[0].Value, int64(tt.expectedGHApiCalls))

			var gotMatch bool
			if tt.expectedString == "" {
				gotMatch = got == tt.expectedString
			} else {
				gotMatch = strings.Contains(got, tt.expectedString)
			}

			assert.Assert(t, gotMatch, "expected %s, got %s", tt.expectedString, got)
			if tt.filterMessageSnippet != "" {
				gotcha := exporter.FilterMessageSnippet(tt.filterMessageSnippet)
				assert.Assert(t, gotcha.Len() > 0, "expected to find %s in logs, found %v", tt.filterMessageSnippet, exporter.All())
			}
		})
	}
}

func TestGetTektonDirGraphQL(t *testing.T) {
	tests := []struct {
		name             string
		event            *info.Event
		treepath         string
		provenance       string
		setup            func(t *testing.T, mux *http.ServeMux, event *info.Event)
		wantErr          string
		wantLogSnippet   string
		expectedAPICount int64
		skipSetupGitTree bool
	}{
		{
			name: "graphql batch fetch reduces api calls",
			event: &info.Event{
				Organization:  "tekton",
				Repository:    "cat",
				TriggerTarget: triggertype.PullRequest,
			},
			treepath:         "testdata/tree/simple",
			wantLogSnippet:   "GraphQL batch fetch",
			expectedAPICount: 3,
		},
		{
			name: "graphql error handling",
			event: &info.Event{
				Organization:  "tekton",
				Repository:    "cat",
				TriggerTarget: triggertype.PullRequest,
			},
			skipSetupGitTree: true,
			setup: func(t *testing.T, mux *http.ServeMux, event *info.Event) {
				t.Helper()
				shaDir := fmt.Sprintf("%x", sha256.Sum256([]byte("testdata/tree/simple")))
				event.SHA = shaDir

				// Setup tree endpoints manually
				mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/git/trees/%v", event.Organization, event.Repository, event.SHA),
					func(rw http.ResponseWriter, _ *http.Request) {
						tree := &github.Tree{
							SHA: &event.SHA,
							Entries: []*github.TreeEntry{
								{
									Path: github.Ptr(".tekton"),
									Type: github.Ptr("tree"),
									SHA:  github.Ptr("tektondirsha"),
								},
							},
						}
						b, _ := json.Marshal(tree)
						fmt.Fprint(rw, string(b))
					})

				// Set up .tekton directory tree
				tektonDirSha := "tektondirsha"
				mux.HandleFunc(fmt.Sprintf("/repos/%v/%v/git/trees/%v", event.Organization, event.Repository, tektonDirSha),
					func(rw http.ResponseWriter, _ *http.Request) {
						tree := &github.Tree{
							SHA: &tektonDirSha,
							Entries: []*github.TreeEntry{
								{
									Path: github.Ptr("pipeline.yaml"),
									Type: github.Ptr("blob"),
									SHA:  github.Ptr("pipelinesha"),
								},
								{
									Path: github.Ptr("pipelinerun.yaml"),
									Type: github.Ptr("blob"),
									SHA:  github.Ptr("pipelinerunsha"),
								},
							},
						}
						b, _ := json.Marshal(tree)
						fmt.Fprint(rw, string(b))
					})

				// Error handler for /api/graphql
				mux.HandleFunc("/api/graphql", func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, "GraphQL endpoint not available", http.StatusNotFound)
				})
			},
			wantErr: "failed to fetch .tekton files via GraphQL",
		},
		{
			name: "default branch uses resolved sha for graphql",
			event: &info.Event{
				Organization:  "tekton",
				Repository:    "cat",
				DefaultBranch: "main",
			},
			provenance:       "default_branch",
			skipSetupGitTree: true,
			setup: func(t *testing.T, mux *http.ServeMux, _ *info.Event) {
				t.Helper()
				resolvedSHA := "resolved-default-branch-sha"
				tektonDirSHA := "tektondirsha"

				mux.HandleFunc("/repos/tekton/cat/branches/main", func(rw http.ResponseWriter, _ *http.Request) {
					branch := &github.Branch{
						Name: github.Ptr("main"),
						Commit: &github.RepositoryCommit{
							SHA: github.Ptr(resolvedSHA),
						},
					}
					b, _ := json.Marshal(branch)
					fmt.Fprint(rw, string(b))
				})
				mux.HandleFunc("/repos/tekton/cat/git/trees/"+resolvedSHA, func(rw http.ResponseWriter, _ *http.Request) {
					tree := &github.Tree{
						SHA: github.Ptr(resolvedSHA),
						Entries: []*github.TreeEntry{
							{
								Path: github.Ptr(".tekton"),
								Type: github.Ptr("tree"),
								SHA:  github.Ptr(tektonDirSHA),
							},
						},
					}
					b, _ := json.Marshal(tree)
					fmt.Fprint(rw, string(b))
				})
				mux.HandleFunc("/repos/tekton/cat/git/trees/"+tektonDirSHA, func(rw http.ResponseWriter, _ *http.Request) {
					tree := &github.Tree{
						SHA: github.Ptr(tektonDirSHA),
						Entries: []*github.TreeEntry{
							{
								Path: github.Ptr("pipeline.yaml"),
								Type: github.Ptr("blob"),
								SHA:  github.Ptr("pipeline-sha"),
							},
							{
								Path: github.Ptr("pipelinerun.yaml"),
								Type: github.Ptr("blob"),
								SHA:  github.Ptr("pipelinerun-sha"),
							},
						},
					}
					b, _ := json.Marshal(tree)
					fmt.Fprint(rw, string(b))
				})
				mux.HandleFunc("/api/graphql", func(w http.ResponseWriter, r *http.Request) {
					var graphQLReq struct {
						Query string `json:"query"`
					}
					assert.NilError(t, json.NewDecoder(r.Body).Decode(&graphQLReq))
					assert.Assert(t, strings.Contains(graphQLReq.Query, resolvedSHA+":.tekton/pipeline.yaml"), graphQLReq.Query)
					assert.Assert(t, !strings.Contains(graphQLReq.Query, `main:.tekton/pipeline.yaml`), graphQLReq.Query)

					_ = json.NewEncoder(w).Encode(map[string]any{
						"data": map[string]any{
							"repository": map[string]any{
								"file0": map[string]any{"text": "kind: Pipeline\nmetadata:\n  name: pipeline\n"},
								"file1": map[string]any{"text": "kind: PipelineRun\nmetadata:\n  name: run\n"},
							},
						},
					})
				})
			},
			expectedAPICount: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Common setup
			prmetrics.ResetRecorder()
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(provider)

			observer, exporter := zapobserver.New(zap.DebugLevel)
			fakelogger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			gvcs := Provider{
				ghClient:     fakeclient,
				providerName: "github",
				Logger:       fakelogger,
			}

			// Custom setup if provided
			if tt.setup != nil {
				tt.setup(t, mux, tt.event)
			}

			// Standard tree setup unless skipped
			if !tt.skipSetupGitTree && tt.treepath != "" {
				shaDir := fmt.Sprintf("%x", sha256.Sum256([]byte(tt.treepath)))
				tt.event.SHA = shaDir
				ghtesthelper.SetupGitTree(t, mux, tt.treepath, tt.event, false)
			}

			// Execute test
			got, err := gvcs.GetTektonDir(ctx, tt.event, ".tekton", tt.provenance)

			// Validate error
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)

			// Validate logs if specified
			if tt.wantLogSnippet != "" {
				logs := exporter.FilterMessageSnippet(tt.wantLogSnippet)
				assert.Assert(t, logs.Len() > 0, "expected log message: %s", tt.wantLogSnippet)
			}

			// Validate metrics if specified
			if tt.expectedAPICount > 0 {
				var rm metricdata.ResourceMetrics
				err = reader.Collect(ctx, &rm)
				assert.NilError(t, err)
				count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
				assert.Assert(t, ok)
				assert.Equal(t, count.DataPoints[0].Value, tt.expectedAPICount)
			}

			// Validate content
			assert.Assert(t, len(got) > 0, "expected non-empty GetTektonDir output")
		})
	}
}

func TestGetFileInsideRepo(t *testing.T) {
	testGetTektonDir := []struct {
		name       string
		rets       map[string]func(w http.ResponseWriter, r *http.Request)
		filepath   string
		wantErrStr string
	}{
		{
			name:     "fail/trying to get a subdir",
			filepath: "retdir",
			rets: map[string]func(w http.ResponseWriter, r *http.Request){
				"/repos/tekton/thecat/contents/retdir": func(w http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(w, `[{"name": "directory", "path": "a/directory"}]`)
				},
			},

			wantErrStr: "referenced file inside the Github Repository retdir is a directory",
		},
		{
			name:     "fail/bad json",
			filepath: "retfile",
			rets: map[string]func(w http.ResponseWriter, r *http.Request){
				"/repos/tekton/thecat/contents/retfile": func(w http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(w, `nonono`)
				},
			},

			wantErrStr: "invalid character",
		},
		{
			name:     "fail/bad encoding",
			filepath: "retfile",
			rets: map[string]func(w http.ResponseWriter, r *http.Request){
				"/repos/tekton/thecat/contents/retfile": func(w http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(w, `{"name": "file", "path": "a/file", "sha": "shafile"}`)
				},
				"/repos/tekton/thecat/git/blobs/shafile": func(w http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(w, `{"content": "xxxxxx", "sha": "shafile"}`)
				},
			},

			wantErrStr: "illegal base64 data",
		},
		{
			name:     "error/cannot get blob",
			filepath: "retfile",
			rets: map[string]func(w http.ResponseWriter, r *http.Request){
				"/repos/tekton/thecat/contents/retfile": func(w http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(w, `{"name": "file", "path": "a/file", "sha": "shafile"}`)
				},
			},

			wantErrStr: "404",
		},
	}
	for _, tt := range testGetTektonDir {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			gvcs := Provider{
				ghClient: fakeclient,
			}
			for s, f := range tt.rets {
				mux.HandleFunc(s, f)
			}
			event := &info.Event{
				Organization: "tekton",
				Repository:   "thecat",
				SHA:          "123",
			}
			got, err := gvcs.GetFileInsideRepo(ctx, event, tt.filepath, "")
			if tt.wantErrStr != "" {
				assert.Assert(t, err != nil, "we should have get an error here")
				assert.Assert(t, strings.Contains(err.Error(), tt.wantErrStr), err.Error(), tt.wantErrStr)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, got != "")
		})
	}
}

func TestCheckSenderOrgMembership(t *testing.T) {
	tests := []struct {
		name      string
		apiReturn string
		allowed   bool
		wantErr   bool
		runevent  info.Event
	}{
		{
			name: "Check Sender Org Membership",
			runevent: info.Event{
				Organization: "organization",
				Sender:       "me",
			},
			apiReturn: `[{"login": "me"}]`,
			allowed:   true,
			wantErr:   false,
		},
		{
			name: "Check Sender not in Org Membership",
			runevent: info.Event{
				Organization: "organization",
				Sender:       "me",
			},
			apiReturn: `[{"login": "not"}]`,
			allowed:   false,
		},
		{
			name: "Not found on organization",
			runevent: info.Event{
				Organization: "notfound",
				Sender:       "me",
			},
			allowed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			ctx, _ := rtesting.SetupFakeContext(t)
			gprovider := Provider{
				ghClient: fakeclient,
			}
			mux.HandleFunc(fmt.Sprintf("/orgs/%s/members", tt.runevent.Organization), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, tt.apiReturn)
			})

			allowed, err := gprovider.checkSenderOrgMembership(ctx, &tt.runevent)
			if tt.wantErr && err == nil {
				t.Error("We didn't get an error when we wanted one")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("We got an error when we didn't want it: %s", err)
			}
			assert.Equal(t, tt.allowed, allowed)
		})
	}
}

func TestGetStringPullRequestComment(t *testing.T) {
	tests := []struct {
		name, apiReturn string
		wantErr         bool
		runevent        info.Event
		wantRet         bool
	}{
		{
			name:      "Get String from comments",
			runevent:  info.Event{URL: "http://1"},
			apiReturn: `[{"body": "/ok-to-test"}]`,
			wantRet:   true,
		},
		{
			name:      "Not matching string in comments",
			runevent:  info.Event{URL: "http://1"},
			apiReturn: `[{"body": ""}]`,
			wantRet:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			ctx, _ := rtesting.SetupFakeContext(t)
			repo := &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{},
				},
			}
			gprovider := Provider{
				ghClient: fakeclient,
				repo:     repo,
			}
			mux.HandleFunc(fmt.Sprintf("/repos/issues/%s/comments", filepath.Base(tt.runevent.URL)), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, tt.apiReturn)
			})

			ret, err := gprovider.GetStringPullRequestComment(ctx, &tt.runevent)
			if tt.wantErr && err == nil {
				t.Error("We didn't get an error when we wanted one")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("We got an error when we didn't want it: %s", err)
			}

			if tt.wantRet {
				assert.Assert(t, ret != nil)
			}
		})
	}
}

func TestGithubGetCommitInfo(t *testing.T) {
	tests := []struct {
		name                          string
		event                         *info.Event
		noclient                      bool
		apiReply, wantErr             string
		shaurl, shatitle, message     string
		authorName, authorEmail       string
		committerName, committerEmail string
		authorDate, committerDate     string
		checkExtendedFields           bool
		wantHasSkipCmd                bool
	}{
		{
			name: "good with full commit info",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repository",
				SHA:          "shacommitinfo",
			},
			shaurl:              "https://git.provider/commit/info",
			shatitle:            "My beautiful pony",
			message:             "My beautiful pony\n\nThis is the full commit message with details.",
			authorName:          "John Doe",
			authorEmail:         "john@example.com",
			committerName:       "GitHub",
			committerEmail:      "noreply@github.com",
			authorDate:          "2024-01-15T10:30:00Z",
			committerDate:       "2024-01-15T10:31:00Z",
			checkExtendedFields: true,
		},
		{
			name: "basic fields only",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repository",
				SHA:          "shacommitinfo",
			},
			shaurl:         "https://git.provider/commit/info",
			shatitle:       "My beautiful pony",
			message:        "My beautiful pony",
			wantHasSkipCmd: false,
		},
		{
			name: "commit with skip ci command",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repository",
				SHA:          "shacommitinfo",
			},
			shaurl:         "https://git.provider/commit/info",
			shatitle:       "fix: some bug",
			message:        "fix: some bug\n\n[skip ci]",
			wantHasSkipCmd: true,
		},
		{
			name: "commit with ci skip command in title",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repository",
				SHA:          "shacommitinfo",
			},
			shaurl:         "https://git.provider/commit/info",
			shatitle:       "feat: new feature [ci skip]",
			message:        "feat: new feature [ci skip]",
			wantHasSkipCmd: true,
		},
		{
			name: "commit with skip tkn command in title",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repository",
				SHA:          "shacommitinfo",
			},
			shaurl:         "https://git.provider/commit/info",
			shatitle:       "docs: update [skip tkn]",
			message:        "docs: update [skip tkn]",
			wantHasSkipCmd: true,
		},
		{
			name: "commit with tkn skip command in body",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repository",
				SHA:          "shacommitinfo",
			},
			shaurl:         "https://git.provider/commit/info",
			shatitle:       "chore: deps",
			message:        "chore: deps\n\n[tkn skip]",
			wantHasSkipCmd: true,
		},
		{
			name: "incoming webhook populates DefaultBranch",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repository",
				SHA:          "shacommitinfo",
				EventType:    "incoming",
			},
			shaurl:   "https://git.provider/commit/info",
			shatitle: "My beautiful pony",
			message:  "My beautiful pony",
		},
		{
			name: "DefaultBranch already set is preserved",
			event: &info.Event{
				Organization:  "owner",
				Repository:    "repository",
				SHA:           "shacommitinfo",
				DefaultBranch: "develop",
				EventType:     "incoming",
			},
			shaurl:   "https://git.provider/commit/info",
			shatitle: "My beautiful pony",
			message:  "My beautiful pony",
		},
		{
			name: "error",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repository",
				SHA:          "shacommitinfo",
			},
			apiReply: "hello moto",
			wantErr:  "invalid character",
		},
		{
			name:     "noclient",
			event:    &info.Event{},
			wantErr:  "no github client has been initialized",
			noclient: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			// Mock the repo endpoint so GetCommitInfo can resolve DefaultBranch
			// when it is not already set on the event (e.g. incoming webhooks).
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s",
				tt.event.Organization, tt.event.Repository), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, `{"default_branch": "main"}`)
			})
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/git/commits/%s",
				tt.event.Organization, tt.event.Repository, tt.event.SHA), func(rw http.ResponseWriter, _ *http.Request) {
				if tt.apiReply != "" {
					fmt.Fprintf(rw, "%s", tt.apiReply)
					return
				}

				// Build realistic GitHub commit API response
				type commitResponse struct {
					SHA     string `json:"sha"`
					HTMLURL string `json:"html_url"`
					Message string `json:"message"`
					Author  *struct {
						Name  string `json:"name"`
						Email string `json:"email"`
						Date  string `json:"date"`
					} `json:"author,omitempty"`
					Committer *struct {
						Name  string `json:"name"`
						Email string `json:"email"`
						Date  string `json:"date"`
					} `json:"committer,omitempty"`
				}

				// Use message if provided, otherwise use shatitle as fallback
				message := tt.message
				if message == "" && tt.shatitle != "" {
					message = tt.shatitle
				}

				resp := commitResponse{
					SHA:     "shacommitinfo",
					HTMLURL: tt.shaurl,
					Message: message,
				}

				if tt.checkExtendedFields {
					resp.Author = &struct {
						Name  string `json:"name"`
						Email string `json:"email"`
						Date  string `json:"date"`
					}{
						Name:  tt.authorName,
						Email: tt.authorEmail,
						Date:  tt.authorDate,
					}
					resp.Committer = &struct {
						Name  string `json:"name"`
						Email string `json:"email"`
						Date  string `json:"date"`
					}{
						Name:  tt.committerName,
						Email: tt.committerEmail,
						Date:  tt.committerDate,
					}
				}

				jsonData, _ := json.Marshal(resp)
				fmt.Fprint(rw, string(jsonData))
			})
			ctx, _ := rtesting.SetupFakeContext(t)
			provider := &Provider{ghClient: fakeclient}
			if tt.noclient {
				provider = &Provider{}
			}
			err := provider.GetCommitInfo(ctx, tt.event)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.shatitle, tt.event.SHATitle)
			assert.Equal(t, tt.shaurl, tt.event.SHAURL)

			// Verify new extended commit fields are populated
			if tt.checkExtendedFields {
				assert.Equal(t, tt.message, tt.event.SHAMessage, "SHAMessage should match")
				assert.Equal(t, tt.authorName, tt.event.SHAAuthorName, "SHAAuthorName should match")
				assert.Equal(t, tt.authorEmail, tt.event.SHAAuthorEmail, "SHAAuthorEmail should match")
				assert.Equal(t, tt.committerName, tt.event.SHACommitterName, "SHACommitterName should match")
				assert.Equal(t, tt.committerEmail, tt.event.SHACommitterEmail, "SHACommitterEmail should match")

				// Verify dates are parsed correctly
				expectedAuthorDate, _ := time.Parse(time.RFC3339, tt.authorDate)
				assert.DeepEqual(t, expectedAuthorDate, tt.event.SHAAuthorDate)
				expectedCommitterDate, _ := time.Parse(time.RFC3339, tt.committerDate)
				assert.DeepEqual(t, expectedCommitterDate, tt.event.SHACommitterDate)
			}
			assert.Equal(t, tt.wantHasSkipCmd, tt.event.HasSkipCommand)

			// For incoming events, verify DefaultBranch is populated
			if tt.event.EventType == "incoming" {
				if tt.event.DefaultBranch == "develop" {
					// If it was already set, it should be preserved
					assert.Equal(t, "develop", tt.event.DefaultBranch, "DefaultBranch should be preserved")
				} else {
					assert.Equal(t, "main", tt.event.DefaultBranch, "DefaultBranch should be populated from API")
				}
			}
		})
	}
}

func TestGithubSetClient(t *testing.T) {
	tests := []struct {
		name           string
		event          *info.Event
		expectedURL    string
		isGHE          bool
		installationID int64
	}{
		{
			name: "api url set",
			event: &info.Event{
				Provider: &info.Provider{
					URL: "foo.com",
				},
			},
			expectedURL:    "https://foo.com",
			isGHE:          true,
			installationID: 0,
		},
		{
			name:           "default to public github",
			expectedURL:    fmt.Sprintf("%s/", keys.PublicGithubAPIURL),
			event:          info.NewEvent(),
			installationID: 12345,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.event.InstallationID = tt.installationID
			ctx, _ := rtesting.SetupFakeContext(t)
			core, observer := zapobserver.New(zap.InfoLevel)
			testLog := zap.New(core).Sugar()
			fakeRun := &params.Run{
				Clients: clients.Clients{
					Log: testLog,
				},
			}
			v := Provider{}
			err := v.SetClient(ctx, fakeRun, tt.event, nil, nil)
			assert.NilError(t, err)
			assert.Equal(t, tt.expectedURL, *v.APIURL)
			assert.Equal(t, "https", v.Client().BaseURL.Scheme)
			if tt.isGHE {
				assert.Equal(t, "/api/v3/", v.Client().BaseURL.Path)
			} else {
				assert.Equal(t, "/", v.Client().BaseURL.Path)
			}

			logs := observer.TakeAll()
			assert.Assert(t, len(logs) == 1, "expected exactly one log entry, got %d", len(logs))

			prefix := "github-webhook"
			if tt.installationID != 0 {
				prefix = "github-app"
			}
			wantStart := fmt.Sprintf("%s: initialized OAuth2 client", prefix)
			got := logs[0].Message
			assert.Assert(t, strings.HasPrefix(got, wantStart), "log entry should start with %q, got %q", wantStart, got)

			// Determine expected providerName based on whether it's GHE or public GitHub.
			expectedProviderName := "github"
			if tt.isGHE {
				expectedProviderName = "github-enterprise"
			}

			// Build the full expected log message.
			fullExpected := fmt.Sprintf(
				"%s: initialized OAuth2 client for providerName=%s providerURL=%s",
				prefix,
				expectedProviderName,
				tt.event.Provider.URL,
			)

			assert.Equal(t, fullExpected, logs[0].Message)
		})
	}
}

func TestValidate(t *testing.T) {
	header := http.Header{}
	header.Set(github.SHA256SignatureHeader, "hello")
	tests := []struct {
		name         string
		wantErr      bool
		secret       string
		payload      string
		hashFunc     func() hash.Hash
		header       string
		prefixheader string
	}{
		{
			name:         "good/SHA256Signature",
			secret:       "secrete",
			payload:      `{"hello": "moto"}`,
			hashFunc:     sha256.New,
			header:       github.SHA256SignatureHeader,
			prefixheader: "sha256",
		},
		{
			name:         "good/SHA1Signature",
			secret:       "secrete",
			payload:      `{"ola": "amigo"}`,
			hashFunc:     sha1.New,
			header:       github.SHA1SignatureHeader,
			prefixheader: "sha1",
		},
		{
			name:         "bad/signature",
			payload:      `{"ciao": "ragazzo"}`,
			hashFunc:     sha256.New,
			header:       github.SHA1SignatureHeader,
			prefixheader: "sha1",
			wantErr:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, _ := logger.GetLogger()
			v := &Provider{Logger: logger}

			hm := hmac.New(tt.hashFunc, []byte(tt.secret))
			hm.Write([]byte(tt.payload))
			signature := hex.EncodeToString(hm.Sum(nil))

			httpHeader := http.Header{}
			httpHeader.Add(tt.header, fmt.Sprintf("%s=%s", tt.prefixheader, signature))

			event := info.NewEvent()
			event.Request = &info.Request{
				Header:  httpHeader,
				Payload: []byte(tt.payload),
			}
			event.Provider = &info.Provider{
				WebhookSecret: tt.secret,
			}

			if err := v.Validate(context.TODO(), nil, event); (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestGetFiles(t *testing.T) {
	tests := []struct {
		name                   string
		event                  *info.Event
		commitFiles            []*github.CommitFile
		commit                 *github.RepositoryCommit
		wantAddedFilesCount    int
		wantDeletedFilesCount  int
		wantModifiedFilesCount int
		wantRenamedFilesCount  int
		wantAPIRequestCount    int64
	}{
		{
			name: "pull-request",
			event: &info.Event{
				TriggerTarget:     "pull_request",
				Organization:      "pullrequestowner",
				Repository:        "pullrequestrepository",
				PullRequestNumber: 10,
			},
			commitFiles: []*github.CommitFile{
				{
					Filename: ptr.String("modified.yaml"),
					Status:   ptr.String("modified"),
				}, {
					Filename: ptr.String("added.doc"),
					Status:   ptr.String("added"),
				}, {
					Filename: ptr.String("removed.yaml"),
					Status:   ptr.String("removed"),
				}, {
					Filename: ptr.String("renamed.doc"),
					Status:   ptr.String("renamed"),
				},
			},
			wantAddedFilesCount:    1,
			wantDeletedFilesCount:  1,
			wantModifiedFilesCount: 1,
			wantRenamedFilesCount:  1,
			wantAPIRequestCount:    2,
		},
		{
			name: "push",
			event: &info.Event{
				TriggerTarget: "push",
				Organization:  "pushrequestowner",
				Repository:    "pushrequestrepository",
				SHA:           "shacommitinfo",
			},
			commit: &github.RepositoryCommit{
				Files: []*github.CommitFile{
					{
						Filename: ptr.String("modified.yaml"),
						Status:   ptr.String("modified"),
					}, {
						Filename: ptr.String("added.doc"),
						Status:   ptr.String("added"),
					}, {
						Filename: ptr.String("removed.yaml"),
						Status:   ptr.String("removed"),
					}, {
						Filename: ptr.String("renamed.doc"),
						Status:   ptr.String("renamed"),
					},
				},
			},
			wantAddedFilesCount:    1,
			wantDeletedFilesCount:  1,
			wantModifiedFilesCount: 1,
			wantRenamedFilesCount:  1,
			wantAPIRequestCount:    1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			prmetrics.ResetRecorder()
			reader := sdkmetric.NewManualReader()
			metricProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(metricProvider)

			if tt.event.TriggerTarget == "pull_request" {
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/pulls/%d/files",
					tt.event.Organization, tt.event.Repository, tt.event.PullRequestNumber), func(rw http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
						rw.Header().Add("Link", fmt.Sprintf("<https://api.github.com/repos/%s/%s/pulls/%d/files?page=2>; rel=\"next\"", tt.event.Organization, tt.event.Repository, tt.event.PullRequestNumber))
						fmt.Fprint(rw, "[]")
					} else {
						b, _ := json.Marshal(tt.commitFiles)
						fmt.Fprint(rw, string(b))
					}
				})
			}
			if tt.event.TriggerTarget == "push" {
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/commits/%s",
					tt.event.Organization, tt.event.Repository, tt.event.SHA), func(rw http.ResponseWriter, _ *http.Request) {
					c := &github.RepositoryCommit{
						Files: tt.commit.Files,
					}
					b, _ := json.Marshal(c)
					fmt.Fprint(rw, string(b))
				})
			}

			log, _ := logger.GetLogger()
			ctx, _ := rtesting.SetupFakeContext(t)
			provider := &Provider{
				ghClient:      fakeclient,
				PaginedNumber: 1,

				// necessary for metrics
				providerName: "github",
				triggerEvent: string(tt.event.TriggerTarget),
				Logger:       log,
			}
			changedFiles, err := provider.GetFiles(ctx, tt.event)
			assert.NilError(t, err, nil)
			assert.Equal(t, tt.wantAddedFilesCount, len(changedFiles.Added))
			assert.Equal(t, tt.wantDeletedFilesCount, len(changedFiles.Deleted))
			assert.Equal(t, tt.wantModifiedFilesCount, len(changedFiles.Modified))
			assert.Equal(t, tt.wantRenamedFilesCount, len(changedFiles.Renamed))

			if tt.event.TriggerTarget == "pull_request" {
				for i := range changedFiles.All {
					assert.Equal(t, *tt.commitFiles[i].Filename, changedFiles.All[i])
				}
			}
			if tt.event.TriggerTarget == "push" {
				for i := range changedFiles.All {
					assert.Equal(t, *tt.commit.Files[i].Filename, changedFiles.All[i])
				}
			}

			// Check caching
			var rm metricdata.ResourceMetrics
			err = reader.Collect(ctx, &rm)
			assert.NilError(t, err, "error collecting metrics")

			assert.Equal(t, len(rm.ScopeMetrics), 1)
			assert.Equal(t, len(rm.ScopeMetrics[0].Metrics), 1)
			assert.Equal(t, rm.ScopeMetrics[0].Metrics[0].Name, "pipelines_as_code_git_provider_api_request_count")
			count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
			assert.Assert(t, ok)
			assert.Equal(t, count.DataPoints[0].Value, int64(tt.wantAPIRequestCount))

			_, _ = provider.GetFiles(ctx, tt.event)
			// recollect the metrics after the second call
			err = reader.Collect(ctx, &rm)
			assert.NilError(t, err, "error collecting metrics")
			count, ok = rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
			assert.Assert(t, ok)
			assert.Equal(t, count.DataPoints[0].Value, int64(tt.wantAPIRequestCount))
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestProviderCheckWebhookSecretValidity(t *testing.T) {
	t1 := time.Date(1999, time.February, 3, 4, 5, 6, 7, time.UTC)
	cw := clockwork.NewFakeClockAt(t1)
	tests := []struct {
		name           string
		wantSubErr     string
		remaining      int
		expTime        time.Time
		expHeaderSet   bool
		apiNotEnabled  bool
		wantLogSnippet string
		statusCode     int
		wantNilSCIM    bool
		wantNilResp    bool
	}{
		{
			name:         "remaining scim calls",
			remaining:    1,
			expHeaderSet: true,
			expTime:      cw.Now().Add(1 * time.Minute),
		},
		{
			name:         "no remaining scim calls",
			wantSubErr:   "api rate limit exceeded. Access will be restored at Mon, 01 Jan 0001 00:00:00 UTC",
			remaining:    0,
			expHeaderSet: true,
			expTime:      cw.Now().Add(1 * time.Minute),
		},
		{
			name:         "expired",
			wantSubErr:   "token has expired",
			expTime:      cw.Now().Add(-1 * time.Minute),
			expHeaderSet: true,
		},
		{
			name:      "no header mean unlimited",
			remaining: 5,
		},
		{
			name:       "skipping api rate limit is not enabled",
			remaining:  0,
			statusCode: http.StatusNotFound,
		},
		{
			name:        "skipping because scim is not available",
			remaining:   0,
			wantNilSCIM: true,
		},
		{
			name:        "resp is nil",
			remaining:   0,
			wantNilResp: true,
			wantSubErr:  "error making request to the GitHub API checking rate limit",
		},
		{
			name:       "no header but no remaining scim calls",
			remaining:  0,
			wantSubErr: "api rate limit exceeded. Access will be restored at Mon, 01 Jan 0001 00:00:00 UTC",
		},
		{
			name:       "api error",
			wantSubErr: "error making request to the GitHub API checking rate limit",
			statusCode: http.StatusInternalServerError,
		},
		{
			name:           "not enabled",
			apiNotEnabled:  true,
			wantLogSnippet: "skipping checking",
		},
		{
			name:           "not enabled",
			apiNotEnabled:  true,
			wantLogSnippet: "skipping checking",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			logger, observer := logger.GetLogger()

			if !tt.apiNotEnabled {
				mux.HandleFunc("/rate_limit", func(rw http.ResponseWriter, _ *http.Request) {
					if tt.statusCode != 0 {
						rw.WriteHeader(tt.statusCode)
						return
					}
					s := &github.RateLimits{}
					if !tt.wantNilSCIM {
						s.SCIM = &github.Rate{
							Remaining: tt.remaining,
						}
					}
					st := new(struct {
						Resources *github.RateLimits `json:"resources"`
					})
					st.Resources = s
					b, _ := json.Marshal(st)
					rw.Header().Set("Content-Type", "application/json")
					if tt.expHeaderSet {
						rw.Header().Set("GitHub-Authentication-Token-Expiration", tt.expTime.Format("2006-01-02 03:04:05 MST"))
					}
					fmt.Fprint(rw, string(b))
				})
			}
			defer teardown()

			// create bad round tripper to make response nil and test that it handles that case.
			if tt.wantNilResp {
				errRT := roundTripperFunc(func(*http.Request) (*http.Response, error) {
					return nil, fmt.Errorf("network down")
				})
				httpClient := &http.Client{Transport: errRT}
				fakeclient = github.NewClient(httpClient)
			}

			v := &Provider{
				ghClient: fakeclient,
				Logger:   logger,
			}
			err := v.checkWebhookSecretValidity(ctx, cw)
			if tt.wantSubErr != "" {
				assert.ErrorContains(t, err, tt.wantSubErr)
			} else {
				assert.NilError(t, err)
			}

			if tt.wantLogSnippet != "" {
				assert.Assert(t, observer.FilterMessageSnippet(tt.wantLogSnippet).Len() > 0)
			}
		})
	}
}

func TestParseTS(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
		expTS   string
	}{
		{
			name:  "valid as defined by go-github",
			expTS: "2023-01-31 03:30:00 UTC",
		},
		{
			name:  "valid with UTC and eu time",
			expTS: "2023-01-31 15:30:00 UTC",
		},
		{
			name:  "valid with timezone inside",
			expTS: "2023-04-26 23:23:26 +2000",
		},

		{
			name:    "invalid",
			expTS:   "Mon 2023-04-26 23:23:26 +2000",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseTS(tt.expTS)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.NilError(t, err)
				assert.Assert(t, got.Year() != 0o1)
			}
		})
	}
}

func TestListRepos(t *testing.T) {
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	mux.HandleFunc("user/installations/1/repositories/2", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw)
	})

	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Authorization", "Bearer 12345")
		w.Header().Set("Accept", "application/vnd.github+json")
		if r.URL.Query().Get("page") == "" || r.URL.Query().Get("page") == "1" {
			w.Header().Add("Link", `<https://api.github.com/installation/repositories?page=2&per_page=1>; rel="next"`)
			_, _ = fmt.Fprint(w, `{}`)
		} else {
			_, _ = fmt.Fprint(w, `{"total_count": 1,"repositories": [{"id":1,"html_url": "https://matched/by/incoming"}]}`)
		}
	})

	ctx, _ := rtesting.SetupFakeContext(t)
	provider := &Provider{ghClient: fakeclient, PaginedNumber: 1}
	data, err := ListRepos(ctx, provider)
	assert.NilError(t, err)
	assert.Equal(t, data[0], "https://matched/by/incoming")
}

func TestCreateToken(t *testing.T) {
	repos := []v1alpha1.Repository{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo1",
			Namespace: "test1",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: nil,
			URL:              "https://github.com/owner/project1",
			Settings: &v1alpha1.Settings{
				GithubAppTokenScopeRepos: []string{"owner/project1", "owner/project2"},
			},
		},
	}, {
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo2",
			Namespace: "test1",
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: nil,
			URL:              "https://github.com/owner/project2",
		},
	}}

	ctx, _ := rtesting.SetupFakeContext(t)
	logger, _ := logger.GetLogger()
	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipelinesascode",
		},
	}
	validSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipelines-as-code-secret",
			Namespace: testNamespace.Name,
		},
		Data: map[string][]byte{
			"github-application-id": []byte("12345"),
			"github-private-key":    []byte(fakePrivateKey),
		},
	}
	ctx = info.StoreNS(ctx, testNamespace.GetName())
	ctx = info.StoreCurrentControllerName(ctx, "default")

	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{testNamespace},
		Secret:     []*corev1.Secret{validSecret},
	}

	stdata, _ := testclient.SeedTestData(t, ctx, tdata)

	run := &params.Run{
		Clients: clients.Clients{
			Log:            logger,
			PipelineAsCode: stdata.PipelineAsCode,
			Kube:           stdata.Kube,
		},
		Info: info.Info{
			Controller: &info.ControllerInfo{
				Secret: validSecret.GetName(),
			},
		},
	}

	info := &info.Event{
		Provider: &info.Provider{
			URL: "",
		},
		InstallationID: int64(1234567),
	}

	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	extraRepoInstallIDs := map[string]string{"owner/project1": "789", "owner/project2": "10112"}
	urlData := []string{}
	for _, r := range repos {
		if r.Spec.Settings != nil {
			urlData = append(urlData, r.Spec.Settings.GithubAppTokenScopeRepos...)
		}
	}
	for _, v := range urlData {
		split := strings.Split(v, "/")
		mux.HandleFunc(fmt.Sprintf("/repos/%s/%s", split[0], split[1]), func(w http.ResponseWriter, _ *http.Request) {
			sid := extraRepoInstallIDs[fmt.Sprintf("%s/%s", split[0], split[1])]
			_, _ = fmt.Fprintf(w, `{"id": %s}`, sid)
		})
	}

	provider := &Provider{ghClient: fakeclient}
	provider.Run = run
	_, err := provider.CreateToken(ctx, urlData, info)
	assert.Assert(t, len(provider.RepositoryIDs) == 2, "found repositoryIDs are %d which is less than expected", len(provider.RepositoryIDs))
	if err != nil {
		assert.ErrorContains(t, err, "could not refresh installation id 1234567's token")
	}
}

func TestGetPullRequest(t *testing.T) {
	const (
		org   = "owner"
		repo  = "repo"
		prNum = 42
	)
	prJSON := `{
		"number": 42,
		"title": "very cool feature pr",
		"html_url": "https://github.com/owner/repo/pull/42",
		"user": {"login": "author"},
		"head": {
			"sha": "headsha123",
			"ref": "feature-branch",
			"repo": {"html_url": "https://github.com/author/repo"}
		},
		"base": {
			"ref": "main",
			"repo": {
				"html_url": "https://github.com/owner/repo",
				"default_branch": "main",
				"id": 9876
			}
		},
		"labels": [{"name": "bug"}, {"name": "enhancement"}]
	}`

	tests := []struct {
		name              string
		event             *info.Event
		apiReturn         string
		wantErr           bool
		wantErrStr        string
		wantSHA           string
		wantSHAURL        string
		wantURL           string
		wantDefaultBranch string
		wantHeadBranch    string
		wantBaseBranch    string
		wantHeadURL       string
		wantBaseURL       string
		wantTitle         string
		wantSender        string
		wantEventType     string
		wantLabels        []string
		wantRepoID        int64
	}{
		{
			name: "populates all event fields from PR response",
			event: &info.Event{
				Organization:      org,
				Repository:        repo,
				PullRequestNumber: prNum,
			},
			apiReturn:         prJSON,
			wantSHA:           "headsha123",
			wantSHAURL:        "https://github.com/owner/repo/pull/42/commit/headsha123",
			wantURL:           "https://github.com/owner/repo",
			wantDefaultBranch: "main",
			wantHeadBranch:    "feature-branch",
			wantBaseBranch:    "main",
			wantHeadURL:       "https://github.com/author/repo",
			wantBaseURL:       "https://github.com/owner/repo",
			wantTitle:         "very cool feature pr",
			wantSender:        "author",
			wantEventType:     triggertype.PullRequest.String(),
			wantLabels:        []string{"bug", "enhancement"},
			wantRepoID:        9876,
		},
		{
			name: "returns error on API failure",
			event: &info.Event{
				Organization:      org,
				Repository:        repo,
				PullRequestNumber: prNum,
			},
			wantErr:    true,
			wantErrStr: "404",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			if tt.apiReturn != "" {
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/pulls/%d", org, repo, prNum),
					func(rw http.ResponseWriter, _ *http.Request) {
						fmt.Fprint(rw, tt.apiReturn)
					})
			}

			provider := &Provider{ghClient: fakeclient}
			got, err := provider.getPullRequest(ctx, tt.event)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				if tt.wantErrStr != "" {
					assert.ErrorContains(t, err, tt.wantErrStr)
				}
				return
			}
			assert.NilError(t, err)

			assert.Equal(t, tt.wantSHA, got.SHA)
			assert.Equal(t, tt.wantSHAURL, got.SHAURL)
			assert.Equal(t, tt.wantURL, got.URL)
			assert.Equal(t, tt.wantDefaultBranch, got.DefaultBranch)
			assert.Equal(t, tt.wantHeadBranch, got.HeadBranch)
			assert.Equal(t, tt.wantBaseBranch, got.BaseBranch)
			assert.Equal(t, tt.wantHeadURL, got.HeadURL)
			assert.Equal(t, tt.wantBaseURL, got.BaseURL)
			assert.Equal(t, tt.wantTitle, got.PullRequestTitle)
			assert.Equal(t, tt.wantSender, got.Sender)
			assert.Equal(t, tt.wantEventType, got.EventType)
			assert.DeepEqual(t, tt.wantLabels, got.PullRequestLabel)
			assert.Equal(t, 1, len(provider.RepositoryIDs))
			assert.Equal(t, tt.wantRepoID, provider.RepositoryIDs[0])
		})
	}
}

func TestGetPullRequestCaching(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	callCount := 0
	mux.HandleFunc("/repos/owner/repo/pulls/1", func(rw http.ResponseWriter, _ *http.Request) {
		callCount++
		fmt.Fprint(rw, `{
			"number": 1,
			"title": "cached pr",
			"html_url": "https://github.com/owner/repo/pull/1",
			"user": {"login": "author"},
			"head": {"sha": "abc", "ref": "head", "repo": {"html_url": "https://github.com/author/repo"}},
			"base": {"ref": "main", "repo": {"html_url": "https://github.com/owner/repo", "default_branch": "main", "id": 1}},
			"labels": [{"name": "bug"}, {"name": "enhancement"}]
		}`)
	})

	event := &info.Event{
		Organization:      "owner",
		Repository:        "repo",
		PullRequestNumber: 1,
	}
	provider := &Provider{ghClient: fakeclient}

	_, err := provider.getPullRequest(ctx, event)
	assert.NilError(t, err)
	assert.Equal(t, 1, callCount, "expected exactly one API call on first invocation")

	_, err = provider.getPullRequest(ctx, event)
	assert.NilError(t, err)
	assert.Equal(t, 1, callCount, "expected no additional API call on second invocation (cache hit)")
}

func TestIsHeadCommitOfBranch(t *testing.T) {
	tests := []struct {
		name       string
		sha        string
		branchName string
		wantErr    bool
	}{{
		name:    "sha exist in the branch",
		sha:     "SHA1",
		wantErr: false,
	}, {
		name:    "sha doesn't exist in the branch",
		sha:     "SHA2",
		wantErr: true,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runEvent := &info.Event{
				Organization:   "pushrequestowner",
				Repository:     "pushrequestrepository",
				SHA:            tt.sha,
				InstallationID: int64(1234567),
			}
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/branches/test1",
				runEvent.Organization, runEvent.Repository), func(rw http.ResponseWriter, _ *http.Request) {
				_, err := fmt.Fprintf(rw, `{
			"name": "test1",
			"commit": {
				"sha": "SHA1"
			}}}`)
				assert.NilError(t, err)
			})

			ctx, _ := rtesting.SetupFakeContext(t)
			provider := &Provider{ghClient: fakeclient}
			err := provider.isHeadCommitOfBranch(ctx, runEvent, "test1")
			assert.Equal(t, err != nil, tt.wantErr)
		})
	}
}

func TestCreateComment(t *testing.T) {
	tests := []struct {
		name         string
		event        *info.Event
		updateMarker string
		commentBody  string
		clientNil    bool
		wantErr      string
		setup        func(t *testing.T, mux *http.ServeMux) func(t *testing.T)
	}{
		{
			name:      "nil client error",
			clientNil: true,
			event:     &info.Event{PullRequestNumber: 123},
			wantErr:   "no github client has been initialized",
		},
		{
			name:    "not a pull request error",
			event:   &info.Event{PullRequestNumber: 0},
			wantErr: "create comment only works on pull requests",
		},
		{
			name:         "create new comment",
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			updateMarker: "",
			setup: func(t *testing.T, mux *http.ServeMux) func(t *testing.T) {
				t.Helper()
				mux.HandleFunc("/repos/org/repo/issues/123/comments", func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodPost)
					rw.WriteHeader(http.StatusCreated)
				})
				return nil
			},
		},
		{
			name:         "update existing comment",
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			updateMarker: "MARKER",
			setup: func(t *testing.T, mux *http.ServeMux) func(t *testing.T) {
				t.Helper()
				mux.HandleFunc("/user", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100, "login": "pac-user"}`)
				},
				)
				mux.HandleFunc("/repos/org/repo/issues/123/comments", func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "MARKER", "user": {"id": 100, "login": "pac-user"}}]`)
						return
					}
				})
				mux.HandleFunc("/repos/org/repo/issues/comments/555", func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodPatch)
					rw.WriteHeader(http.StatusOK)
				})
				return nil
			},
		},
		{
			name:         "no matching comment creates new",
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			updateMarker: "MARKER",
			setup: func(t *testing.T, mux *http.ServeMux) func(t *testing.T) {
				t.Helper()
				mux.HandleFunc("/user", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100, "login": "pac-user"}`)
				})
				mux.HandleFunc("/repos/org/repo/issues/123/comments", func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "NO_MATCH"}]`)
						return
					}
					assert.Equal(t, r.Method, http.MethodPost)
					rw.WriteHeader(http.StatusCreated)
				})
				return nil
			},
		},
		{
			name:         "skip comment from different user and create new",
			event:        &info.Event{Organization: "org", Repository: "repo", PullRequestNumber: 123},
			updateMarker: "MARKER",
			commentBody:  "New Comment MARKER",
			setup: func(t *testing.T, mux *http.ServeMux) func(t *testing.T) {
				t.Helper()
				editEndpointCalled := false
				commentCreated := false
				mux.HandleFunc("/user", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100, "login": "pac-user"}`)
				})
				mux.HandleFunc("/repos/org/repo/issues/123/comments", func(rw http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						fmt.Fprint(rw, `[{"id": 555, "body": "Old MARKER", "user": {"id": 999, "login": "other-user"}}]`)
						return
					}
					assert.Equal(t, r.Method, http.MethodPost)
					commentCreated = true
					rw.WriteHeader(http.StatusCreated)
					fmt.Fprint(rw, `{"id": 556}`)
				})
				mux.HandleFunc("/repos/org/repo/issues/comments/555", func(rw http.ResponseWriter, _ *http.Request) {
					editEndpointCalled = true
					t.Error("should not edit comment from different user")
					rw.WriteHeader(http.StatusOK)
				})
				return func(t *testing.T) {
					t.Helper()
					assert.Assert(t, !editEndpointCalled, "edit endpoint should not be called for comment from different user")
					assert.Assert(t, commentCreated, "new comment should be created")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()

			var provider *Provider
			var postAssert func(t *testing.T)
			if !tt.clientNil {
				fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
				defer teardown()
				provider = &Provider{ghClient: fakeclient, Logger: fakelogger}

				if tt.setup != nil {
					postAssert = tt.setup(t, mux)
				}
			} else {
				provider = &Provider{} // nil client
			}

			body := tt.commentBody
			if body == "" {
				body = "comment body"
			}

			err := provider.CreateComment(ctx, tt.event, body, tt.updateMarker)

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			if postAssert != nil {
				postAssert(t)
			}
		})
	}
}

func TestCreateCommentDedupLogging(t *testing.T) {
	tests := []struct {
		name            string
		commitBody      string
		updateMarker    string
		setup           func(t *testing.T, mux *http.ServeMux)
		expectedPhases  []string
		unexpectedPhase []string
	}{
		{
			name:         "logs full create flow when marker comment does not exist",
			commitBody:   "new body MARKER",
			updateMarker: "MARKER",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/repos/org/repo/issues/123/comments", func(rw http.ResponseWriter, r *http.Request) {
					switch r.Method {
					case http.MethodGet:
						fmt.Fprint(rw, `[]`)
					case http.MethodPost:
						rw.WriteHeader(http.StatusCreated)
						fmt.Fprint(rw, `{"id": 333, "body": "new body MARKER"}`)
					default:
						t.Fatalf("unexpected method %s", r.Method)
					}
				})
			},
			expectedPhases: []string{
				"initial_list",
				"create_comment_start",
				"create_comment_done",
			},
			unexpectedPhase: []string{"no_marker", "jitter_wait", "post_jitter_list", "pre_create_race_window", "post_create_list", "dedup_select_primary", "dedup_complete", "dedup_delete_attempt"},
		},
		{
			name:         "logs edit flow for one existing marker comment",
			commitBody:   "new body MARKER",
			updateMarker: "MARKER",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/user", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100, "login": "pac-user"}`)
				})
				mux.HandleFunc("/repos/org/repo/issues/123/comments", func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					fmt.Fprint(rw, `[{"id": 111, "body": "MARKER old", "created_at": "2024-01-01T00:00:00Z", "user": {"id": 100, "login": "pac-user"}}]`)
				})
				mux.HandleFunc("/repos/org/repo/issues/comments/111", func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodPatch)
					rw.WriteHeader(http.StatusOK)
				})
			},
			expectedPhases: []string{
				"initial_list",
				"edit_comment",
			},
			unexpectedPhase: []string{"create_comment_start", "dedup_select_primary", "dedup_complete", "dedup_delete_attempt"},
		},
		{
			name:         "logs duplicate detection for multiple markers and edits first",
			commitBody:   "new body MARKER",
			updateMarker: "MARKER",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/user", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"id": 100, "login": "pac-user"}`)
				})
				mux.HandleFunc("/repos/org/repo/issues/123/comments", func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					fmt.Fprint(rw, `[{"id": 111, "body": "MARKER old", "created_at": "2024-01-01T00:00:00Z", "user": {"id": 100, "login": "pac-user"}}, {"id": 222, "body": "MARKER old", "created_at": "2024-01-01T00:01:00Z", "user": {"id": 100, "login": "pac-user"}}]`)
				})
				mux.HandleFunc("/repos/org/repo/issues/comments/111", func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodPatch)
					rw.WriteHeader(http.StatusOK)
				})
			},
			expectedPhases: []string{
				"initial_list",
				"duplicate_detected",
				"edit_comment",
			},
			unexpectedPhase: []string{"create_comment_start", "dedup_select_primary", "dedup_delete_attempt", "dedup_delete_done", "dedup_complete"},
		},
		{
			name:         "logs no_marker phase when updateMarker is empty",
			commitBody:   "body without marker",
			updateMarker: "",
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/repos/org/repo/issues/123/comments", func(rw http.ResponseWriter, r *http.Request) {
					switch r.Method {
					case http.MethodPost:
						rw.WriteHeader(http.StatusCreated)
						fmt.Fprint(rw, `{"id": 444, "body": "body without marker"}`)
					default:
						t.Fatalf("unexpected method %s", r.Method)
					}
				})
			},
			expectedPhases: []string{
				"no_marker",
				"create_comment_start",
				"create_comment_done",
			},
			unexpectedPhase: []string{"initial_list", "edit_comment", "duplicate_detected"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreCurrentControllerName(ctx, "controller-test")

			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()
			tt.setup(t, mux)

			core, observedLogs := zapobserver.New(zap.DebugLevel)
			provider := &Provider{
				ghClient: fakeclient,
				Logger:   zap.New(core).Sugar(),
			}
			event := &info.Event{
				Organization:      "org",
				Repository:        "repo",
				PullRequestNumber: 123,
				Request: &info.Request{
					Header: http.Header{
						"X-Github-Delivery": []string{"evt-123"},
					},
				},
			}

			err := provider.CreateComment(ctx, event, tt.commitBody, tt.updateMarker)
			assert.NilError(t, err)

			logEntries := observedLogs.FilterMessage("github comment dedup flow").All()
			assert.Assert(t, len(logEntries) > 0, "expected debug comment logs to be emitted")

			phaseCounts := map[string]int{}
			seenDedupTrace := ""
			for _, entry := range logEntries {
				fields := entry.ContextMap()
				phase := fmt.Sprint(fields["phase"])
				phaseCounts[phase]++

				traceID := fmt.Sprint(fields["dedup_trace"])
				assert.Assert(t, traceID != "" && traceID != "<nil>")
				if seenDedupTrace == "" {
					seenDedupTrace = traceID
				}
				assert.Equal(t, seenDedupTrace, traceID)

				eventID, hasEventID := fields["event_id"]
				assert.Assert(t, hasEventID, "event_id field should be present in %+v", fields)
				assert.Equal(t, "evt-123", fmt.Sprint(eventID), "event_id should match X-GitHub-Delivery header")
				assert.Equal(t, "123", fmt.Sprint(fields["pr"]))
				assert.Equal(t, "controller-test", fmt.Sprint(fields["controller_label"]))

				if phase == "initial_list" {
					_, hasMatchedCount := fields["matched_count"]
					assert.Assert(t, hasMatchedCount, "phase=%s should include matched_count fields=%+v", phase, fields)
				}

				bodyHashPhases := map[string]bool{
					"create_comment_start": true,
					"edit_comment":         true,
					"no_edit_needed":       true,
					"no_marker":            true,
				}
				if bodyHashPhases[phase] {
					_, hasBodyHash := fields["body_hash"]
					assert.Assert(t, hasBodyHash, "phase=%s should include body_hash field=%+v", phase, fields)
				}
			}

			for _, phase := range tt.expectedPhases {
				assert.Assert(t, phaseCounts[phase] > 0, "missing phase %s in logs: %+v", phase, phaseCounts)
			}
			for _, phase := range tt.unexpectedPhase {
				assert.Assert(t, phaseCounts[phase] == 0, "unexpected phase %s in logs: %+v", phase, phaseCounts)
			}
		})
	}
}

func TestSkipPushEventForPRCommits(t *testing.T) {
	iid := int64(1234)
	tests := []struct {
		name                string
		pacInfoEnabled      bool
		pushEvent           *github.PushEvent
		mockAPIs            map[string]func(rw http.ResponseWriter, r *http.Request)
		isPartOfPR          bool
		wantErr             bool
		wantErrContains     string
		skipWarnLogContains string
	}{
		{
			name:           "skip push event when commit is part of an open PR",
			pacInfoEnabled: true,
			pushEvent: &github.PushEvent{
				Repo: &github.PushEventRepository{
					Name:  github.Ptr("testRepo"),
					Owner: &github.User{Login: github.Ptr("testOrg")},
				},
				HeadCommit: &github.HeadCommit{
					ID: github.Ptr("abc123"),
				},
			},
			mockAPIs: map[string]func(rw http.ResponseWriter, r *http.Request){
				"/repos/testOrg/testRepo/commits/abc123/pulls": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					fmt.Fprint(rw, `[{"number": 42, "state": "open"}]`)
				},
			},
			isPartOfPR:      true,
			wantErr:         false,
			wantErrContains: "",
		},
		{
			name:           "continue processing push event when commit is not part of PR",
			pacInfoEnabled: true,
			pushEvent: &github.PushEvent{
				Repo: &github.PushEventRepository{
					Name:          github.Ptr("testRepo"),
					Owner:         &github.User{Login: github.Ptr("testOrg")},
					DefaultBranch: github.Ptr("main"),
					HTMLURL:       github.Ptr("https://github.com/testOrg/testRepo"),
					ID:            github.Ptr(iid),
				},
				HeadCommit: &github.HeadCommit{
					ID:      github.Ptr("abc123"),
					URL:     github.Ptr("https://github.com/testOrg/testRepo/commit/abc123"),
					Message: github.Ptr("Test commit message"),
				},
				Ref:    github.Ptr("refs/heads/main"),
				Sender: &github.User{Login: github.Ptr("testUser")},
			},
			mockAPIs: map[string]func(rw http.ResponseWriter, r *http.Request){
				"/repos/testOrg/testRepo/pulls": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					assert.Equal(t, r.URL.Query().Get("state"), "open")
					fmt.Fprint(rw, `[{"number": 42}]`)
				},
				"/repos/testOrg/testRepo/pulls/42/commits": func(rw http.ResponseWriter, r *http.Request) {
					assert.Equal(t, r.Method, http.MethodGet)
					fmt.Fprint(rw, `[{"sha": "def456"}, {"sha": "xyz789"}]`)
				},
			},
			isPartOfPR: false,
			wantErr:    false,
		},
		{
			name:           "continue when skip feature is disabled",
			pacInfoEnabled: false,
			pushEvent: &github.PushEvent{
				Repo: &github.PushEventRepository{
					Name:          github.Ptr("testRepo"),
					Owner:         &github.User{Login: github.Ptr("testOrg")},
					DefaultBranch: github.Ptr("main"),
					HTMLURL:       github.Ptr("https://github.com/testOrg/testRepo"),
					ID:            github.Ptr(iid),
				},
				HeadCommit: &github.HeadCommit{
					ID:      github.Ptr("abc123"),
					URL:     github.Ptr("https://github.com/testOrg/testRepo/commit/abc123"),
					Message: github.Ptr("Test commit message"),
				},
				Ref:    github.Ptr("refs/heads/main"),
				Sender: &github.User{Login: github.Ptr("testUser")},
			},
			isPartOfPR: false, // This should not be checked when feature is disabled
			wantErr:    false,
		},
		{
			name:           "log warning when API error occurs",
			pacInfoEnabled: true,
			pushEvent: &github.PushEvent{
				Repo: &github.PushEventRepository{
					Name:  github.Ptr("testRepo"),
					Owner: &github.User{Login: github.Ptr("testOrg")},
				},
				HeadCommit: &github.HeadCommit{
					ID: github.Ptr("1234"),
				},
			},
			mockAPIs: map[string]func(rw http.ResponseWriter, r *http.Request){
				"/repos/testOrg/testRepo/pulls": func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusInternalServerError)
					fmt.Fprint(rw, `{"message": "API error"}`)
				},
			},
			isPartOfPR:          false,
			wantErr:             false,
			skipWarnLogContains: "Error getting pull requests associated with the commit in this push event",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
			defer teardown()

			// Register API endpoints
			for pattern, handler := range tt.mockAPIs {
				mux.HandleFunc(pattern, handler)
			}

			// Create a logger that captures logs
			observer, logs := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			// Create provider with the test configuration
			provider := &Provider{
				ghClient: fakeclient,
				Logger:   logger,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						SkipPushEventForPRCommits: tt.pacInfoEnabled,
					},
				},
			}

			// Create event with the right trigger type
			event := info.NewEvent()
			event.TriggerTarget = triggertype.Push

			// Process the event
			result, err := provider.processEvent(ctx, event, tt.pushEvent)

			// Check errors if expected
			if tt.wantErr {
				assert.Assert(t, err != nil)
				if tt.wantErrContains != "" {
					assert.ErrorContains(t, err, tt.wantErrContains)
				}
				assert.Assert(t, result == nil, "Expected nil result when error occurs")
				return
			}

			// If no error expected, check the result
			assert.NilError(t, err)

			// If push event was skipped (commit is part of PR), result should be nil
			if tt.pacInfoEnabled && tt.isPartOfPR {
				assert.Assert(t, result == nil, "Expected nil result when push event is skipped for PR commit")
				return
			}

			assert.Assert(t, result != nil, "Expected non-nil result when no error occurs")

			// Check event fields were properly processed
			assert.Equal(t, result.Organization, tt.pushEvent.GetRepo().GetOwner().GetLogin())
			assert.Equal(t, result.Repository, tt.pushEvent.GetRepo().GetName())
			assert.Equal(t, result.SHA, tt.pushEvent.GetHeadCommit().GetID())
			assert.Equal(t, result.Sender, tt.pushEvent.GetSender().GetLogin())

			// Check for warning logs if applicable
			if tt.skipWarnLogContains != "" {
				// Look for warning logs
				found := false
				for _, logEntry := range logs.All() {
					if strings.Contains(logEntry.Message, tt.skipWarnLogContains) {
						found = true
						break
					}
				}
				assert.Assert(t, found, "Expected warning log containing: %s", tt.skipWarnLogContains)
			}
		})
	}
}

func TestFetchAppSlug(t *testing.T) {
	testAppID := int64(12345)
	validSlug := "my-github-app"

	tests := []struct {
		name             string
		privateKey       []byte
		applicationID    int64
		setupMux         func(mux *http.ServeMux)
		wantSlug         string
		wantErrSubstring string
	}{
		{
			name:          "success fetch app slug",
			privateKey:    []byte(fakePrivateKey),
			applicationID: testAppID,
			setupMux: func(mux *http.ServeMux) {
				mux.HandleFunc("/app", func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprintf(w, `{"slug": "%s", "name": "My GitHub App"}`, validSlug)
				})
			},
			wantSlug: validSlug,
		},
		{
			name:          "app endpoint returns 404",
			privateKey:    []byte(fakePrivateKey),
			applicationID: testAppID,
			setupMux: func(mux *http.ServeMux) {
				mux.HandleFunc("/app", func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
					_, _ = fmt.Fprint(w, `{"message": "Not Found"}`)
				})
			},
			wantErrSubstring: "failed to get app info",
		},
		{
			name:             "invalid private key",
			privateKey:       []byte("invalid-key"),
			applicationID:    testAppID,
			setupMux:         func(_ *http.ServeMux) {},
			wantErrSubstring: "failed to parse private key",
		},
		{
			name:          "app returns empty slug",
			privateKey:    []byte(fakePrivateKey),
			applicationID: testAppID,
			setupMux: func(mux *http.ServeMux) {
				mux.HandleFunc("/app", func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprint(w, `{"slug": "", "name": "My GitHub App"}`)
				})
			},
			wantSlug: "",
		},
		{
			name:          "app endpoint returns malformed JSON",
			privateKey:    []byte(fakePrivateKey),
			applicationID: testAppID,
			setupMux: func(mux *http.ServeMux) {
				mux.HandleFunc("/app", func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprint(w, `{invalid json`)
				})
			},
			wantErrSubstring: "failed to get app info",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mux, serverURL, teardown := ghtesthelper.SetupGH()
			defer teardown()

			if tt.setupMux != nil {
				tt.setupMux(mux)
			}

			// Set up context with namespace
			ctx, _ := rtesting.SetupFakeContext(t)
			testNamespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-namespace",
				},
			}
			ctx = info.StoreNS(ctx, testNamespace.GetName())

			// Create a secret with the test application ID and private key
			appIDBytes := make([]byte, 0, 20)
			appIDBytes = fmt.Appendf(appIDBytes, "%d", tt.applicationID)
			testSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pac-secret",
					Namespace: testNamespace.GetName(),
				},
				Data: map[string][]byte{
					"github-application-id": appIDBytes,
					"github-private-key":    tt.privateKey,
				},
			}

			// Set up test data with kubernetes client
			tdata := testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{testSecret},
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)

			// Create a provider with mocked kubernetes client
			provider := &Provider{}
			provider.Run = &params.Run{
				Clients: clients.Clients{
					Kube: stdata.Kube,
				},
				Info: info.Info{
					Controller: &info.ControllerInfo{
						Secret: testSecret.GetName(),
					},
				},
			}

			slug, err := provider.fetchAppSlug(ctx, serverURL)

			if tt.wantErrSubstring != "" {
				assert.Assert(t, err != nil, "expected error but got none")
				assert.ErrorContains(t, err, tt.wantErrSubstring)
				return
			}

			assert.NilError(t, err)
			assert.Equal(t, slug, tt.wantSlug)
		})
	}
}
