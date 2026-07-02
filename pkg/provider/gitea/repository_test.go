package gitea

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/test"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestProviderGetFiles(t *testing.T) {
	type args struct {
		runevent *info.Event
	}
	tests := []struct {
		name                string
		args                args
		changedFiles        string
		want                changedfiles.ChangedFiles
		wantErr             bool
		wantAPIRequestCount int64
	}{
		{
			name: "pull_request",
			args: args{
				runevent: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					TriggerTarget:     "pull_request",
				},
			},
			want: changedfiles.ChangedFiles{
				All: []string{
					"added.txt",
					"deleted.txt",
					"modified.txt",
					"renamed.txt",
				},
				Added: []string{
					"added.txt",
				},
				Deleted:  []string{"deleted.txt"},
				Modified: []string{"modified.txt"},
				Renamed:  []string{"renamed.txt"},
			},
			changedFiles:        `[{"filename":"added.txt","status":"added"},{"filename":"deleted.txt","status":"deleted"},{"filename":"modified.txt","status":"changed"},{"filename":"renamed.txt","status":"renamed"}]`,
			wantAPIRequestCount: 1,
		},
		{
			name: "push",
			args: args{
				runevent: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: -1,
					TriggerTarget:     "push",
					Request: &info.Request{
						Payload: []byte(`{"ref":"refs/heads/main","commits":[{"added":["added.txt"],"removed":["deleted.txt"],"modified":["modified.txt"]},{"added":[".tekton/pullrequest.yaml",".tekton/push.yaml"],"removed":[],"modified":[]}]}`),
					},
				},
			},
			want: changedfiles.ChangedFiles{
				All: []string{
					".tekton/pullrequest.yaml",
					".tekton/push.yaml",
					"added.txt",
					"deleted.txt",
					"modified.txt",
				},
				Added: []string{
					".tekton/pullrequest.yaml",
					".tekton/push.yaml",
					"added.txt",
				},
				Deleted:  []string{"deleted.txt"},
				Modified: []string{"modified.txt"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()

			prmetrics.ResetRecorder()
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(provider)

			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/pulls/%d/files", tt.args.runevent.Organization, tt.args.runevent.Repository, tt.args.runevent.PullRequestNumber), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, tt.changedFiles)
			})
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			repo := &v1alpha1.Repository{Spec: v1alpha1.RepositorySpec{
				Settings: &v1alpha1.Settings{},
			}}
			giteaInstanceURL := "https://gitea.example.com"
			gprovider := Provider{
				giteaClient:      fakeclient,
				repo:             repo,
				Logger:           logger,
				giteaInstanceURL: giteaInstanceURL,
				triggerEvent:     string(tt.args.runevent.TriggerTarget),
			}

			got, err := gprovider.GetFiles(ctx, tt.args.runevent)

			if (err != nil) != tt.wantErr {
				t.Errorf("Provider.GetFiles() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			sort.Strings(got.All)
			sort.Strings(tt.want.All)

			sort.Strings(got.Added)
			sort.Strings(tt.want.Added)

			sort.Strings(got.Deleted)
			sort.Strings(tt.want.Deleted)

			sort.Strings(got.Modified)
			sort.Strings(tt.want.Modified)

			sort.Strings(got.Renamed)
			sort.Strings(tt.want.Renamed)
			if !reflect.DeepEqual(got.All, tt.want.All) {
				t.Errorf("Provider.GetFiles() All = %v, want %v", got.All, tt.want.All)
			}
			if !reflect.DeepEqual(got.Added, tt.want.Added) {
				t.Errorf("Provider.GetFiles() Added = %v, want %v", got.Added, tt.want.Added)
			}
			if !reflect.DeepEqual(got.Deleted, tt.want.Deleted) {
				t.Errorf("Provider.GetFiles() Deleted = %v, want %v", got.Deleted, tt.want.Deleted)
			}
			if !reflect.DeepEqual(got.Modified, tt.want.Modified) {
				t.Errorf("Provider.GetFiles() Modified = %v, want %v", got.Modified, tt.want.Modified)
			}
			if !reflect.DeepEqual(got.Renamed, tt.want.Renamed) {
				t.Errorf("Provider.GetFiles() Renamed = %v, want %v", got.Renamed, tt.want.Renamed)
			}

			// Verify metrics from first call
			if tt.wantAPIRequestCount > 0 {
				var rm metricdata.ResourceMetrics
				err = reader.Collect(ctx, &rm)
				assert.NilError(t, err, "error collecting metrics")

				assert.Equal(t, len(rm.ScopeMetrics), 1)
				assert.Equal(t, len(rm.ScopeMetrics[0].Metrics), 1)
				assert.Equal(t, rm.ScopeMetrics[0].Metrics[0].Name, "pipelines_as_code_git_provider_api_request_count")
				count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
				assert.Assert(t, ok)
				assert.Equal(t, count.DataPoints[0].Value, int64(tt.wantAPIRequestCount))
			}

			// Verify caching: second call should return cached result without additional API calls
			got2, err2 := gprovider.GetFiles(ctx, tt.args.runevent)
			assert.NilError(t, err2)
			assert.DeepEqual(t, got, got2)

			if tt.wantAPIRequestCount > 0 {
				var rm metricdata.ResourceMetrics
				err = reader.Collect(ctx, &rm)
				assert.NilError(t, err, "error collecting metrics")
				count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
				assert.Assert(t, ok)
				assert.Equal(t, count.DataPoints[0].Value, int64(tt.wantAPIRequestCount))
			}
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
	}{
		{
			name: "test with badly formatted yaml",
			event: &info.Event{
				Organization: "tekton",
				Repository:   "cat",
				SHA:          "123",
			},
			treepath: "testdata/tree/badyaml",
			wantErr:  "error unmarshalling yaml file badyaml.yaml: yaml: line 2: did not find expected key",
		},
	}
	for _, tt := range testGetTektonDir {
		t.Run(tt.name, func(t *testing.T) {
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()
			gvcs := Provider{
				giteaClient: fakeclient,
				Logger:      fakelogger,
			}
			if tt.provenance == "default_branch" {
				tt.event.SHA = tt.event.DefaultBranch
			} else {
				shaDir := fmt.Sprintf("%x", sha256.Sum256([]byte(tt.treepath)))
				tt.event.SHA = shaDir
			}

			tgitea.SetupGitTree(t, mux, tt.treepath, tt.event, false)
			got, err := gvcs.GetTektonDir(ctx, tt.event, ".tekton", tt.provenance)
			if tt.wantErr != "" {
				assert.Assert(t, err != nil, "we should have get an error here")
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, strings.Contains(got, tt.expectedString), "expected %s, got %s", tt.expectedString, got)
		})
	}
}

func TestGetCommitInfo(t *testing.T) {
	tests := []struct {
		name                string
		event               *info.Event
		mockCommitResponse  string
		wantErr             bool
		wantSHATitle        string
		wantSHAURL          string
		wantSHAMessage      string
		wantAuthorName      string
		wantAuthorEmail     string
		wantAuthorDate      string
		wantCommitterName   string
		wantCommitterEmail  string
		wantCommitterDate   string
		checkExtendedFields bool
		noClient            bool
	}{
		{
			name: "good with full commit info",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repo",
				SHA:          "abc123",
			},
			mockCommitResponse: `{
				"sha": "abc123",
				"html_url": "https://gitea.com/owner/repo/commit/abc123",
				"commit": {
					"message": "feat: add new feature\n\nThis is the full commit message with details.",
					"author": {
						"name": "John Doe",
						"email": "john@example.com",
						"date": "2024-01-15T10:30:00Z"
					},
					"committer": {
						"name": "Gitea",
						"email": "noreply@gitea.com",
						"date": "2024-01-15T10:31:00Z"
					}
				}
			}`,
			wantSHATitle:        "feat: add new feature",
			wantSHAURL:          "https://gitea.com/owner/repo/commit/abc123",
			wantSHAMessage:      "feat: add new feature\n\nThis is the full commit message with details.",
			wantAuthorName:      "John Doe",
			wantAuthorEmail:     "john@example.com",
			wantAuthorDate:      "2024-01-15T10:30:00Z",
			wantCommitterName:   "Gitea",
			wantCommitterEmail:  "noreply@gitea.com",
			wantCommitterDate:   "2024-01-15T10:31:00Z",
			checkExtendedFields: true,
		},
		{
			name: "basic fields only",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repo",
				SHA:          "def456",
			},
			mockCommitResponse: `{
				"sha": "def456",
				"html_url": "https://gitea.com/owner/repo/commit/def456",
				"commit": {
					"message": "fix: simple fix"
				}
			}`,
			wantSHATitle:   "fix: simple fix",
			wantSHAURL:     "https://gitea.com/owner/repo/commit/def456",
			wantSHAMessage: "fix: simple fix",
		},
		{
			name: "no client error",
			event: &info.Event{
				Organization: "owner",
				Repository:   "repo",
				SHA:          "abc123",
			},
			noClient: true,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			var provider *Provider
			if !tt.noClient {
				client, mux, tearDown := tgitea.Setup(t)
				defer tearDown()

				// Mock the GetSingleCommit API endpoint
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/git/commits/%s", tt.event.Organization, tt.event.Repository, tt.event.SHA),
					func(rw http.ResponseWriter, _ *http.Request) {
						fmt.Fprint(rw, tt.mockCommitResponse)
					})

				provider = &Provider{giteaClient: client}
			} else {
				provider = &Provider{}
			}

			err := provider.GetCommitInfo(ctx, tt.event)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
				return
			}

			assert.NilError(t, err)
			assert.Equal(t, tt.wantSHATitle, tt.event.SHATitle, "SHATitle should match")
			assert.Equal(t, tt.wantSHAURL, tt.event.SHAURL, "SHAURL should match")
			assert.Equal(t, tt.wantSHAMessage, tt.event.SHAMessage, "SHAMessage should match")

			if tt.checkExtendedFields {
				assert.Equal(t, tt.wantAuthorName, tt.event.SHAAuthorName, "SHAAuthorName should match")
				assert.Equal(t, tt.wantAuthorEmail, tt.event.SHAAuthorEmail, "SHAAuthorEmail should match")
				assert.Equal(t, tt.wantCommitterName, tt.event.SHACommitterName, "SHACommitterName should match")
				assert.Equal(t, tt.wantCommitterEmail, tt.event.SHACommitterEmail, "SHACommitterEmail should match")

				// Verify dates are parsed correctly
				if tt.wantAuthorDate != "" {
					expectedAuthorDate, _ := time.Parse(time.RFC3339, tt.wantAuthorDate)
					assert.DeepEqual(t, expectedAuthorDate, tt.event.SHAAuthorDate)
				}
				if tt.wantCommitterDate != "" {
					expectedCommitterDate, _ := time.Parse(time.RFC3339, tt.wantCommitterDate)
					assert.DeepEqual(t, expectedCommitterDate, tt.event.SHACommitterDate)
				}
			}
		})
	}
}

func TestGetCommitInfoPRLookupPopulatesURLs(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	client, mux, tearDown := tgitea.Setup(t)
	defer tearDown()

	event := &info.Event{
		Organization:      "owner",
		Repository:        "repo",
		PullRequestNumber: 42,
		// SHA intentionally empty to trigger PR lookup path
	}

	// Mock GetPullRequest endpoint
	mux.HandleFunc("/repos/owner/repo/pulls/42", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{
			"head": {
				"ref": "feature-branch",
				"sha": "abc123",
				"repo": {
					"html_url": "https://gitea.com/fork-owner/repo"
				}
			},
			"base": {
				"ref": "main",
				"repo": {
					"html_url": "https://gitea.com/owner/repo"
				}
			}
		}`)
	})

	// Mock GetSingleCommit endpoint
	mux.HandleFunc("/repos/owner/repo/git/commits/abc123", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{
			"sha": "abc123",
			"html_url": "https://gitea.com/owner/repo/commit/abc123",
			"commit": {
				"message": "feat: test commit"
			}
		}`)
	})

	provider := &Provider{giteaClient: client}
	err := provider.GetCommitInfo(ctx, event)
	assert.NilError(t, err)
	assert.Equal(t, "abc123", event.SHA)
	assert.Equal(t, "feature-branch", event.HeadBranch)
	assert.Equal(t, "main", event.BaseBranch)
	assert.Equal(t, "https://gitea.com/fork-owner/repo", event.HeadURL, "HeadURL should be populated from PR lookup")
	assert.Equal(t, "https://gitea.com/owner/repo", event.BaseURL, "BaseURL should be populated from PR lookup")
}

func TestSplitGiteaURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		wantOrg  string
		wantRepo string
		wantRef  string
		wantPath string
		wantErr  bool
	}{
		{
			name:     "src branch URL",
			url:      "https://gitea.example.com/owner/repo/src/branch/main/path/to/task.yaml",
			wantOrg:  "owner",
			wantRepo: "repo",
			wantRef:  "main",
			wantPath: "path/to/task.yaml",
		},
		{
			name:     "raw branch URL",
			url:      "https://gitea.example.com/owner/repo/raw/branch/main/task.yaml",
			wantOrg:  "owner",
			wantRepo: "repo",
			wantRef:  "main",
			wantPath: "task.yaml",
		},
		{
			name:     "src tag URL",
			url:      "https://gitea.example.com/owner/repo/src/tag/v1.0.0/task.yaml",
			wantOrg:  "owner",
			wantRepo: "repo",
			wantRef:  "v1.0.0",
			wantPath: "task.yaml",
		},
		{
			name:     "src commit URL",
			url:      "https://gitea.example.com/owner/repo/src/commit/abc123def/path/task.yaml",
			wantOrg:  "owner",
			wantRepo: "repo",
			wantRef:  "abc123def",
			wantPath: "path/task.yaml",
		},
		{
			name:     "URL encoded branch name",
			url:      "https://gitea.example.com/owner/repo/src/branch/feature%2Fbranch/task.yaml",
			wantOrg:  "owner",
			wantRepo: "repo",
			wantRef:  "feature/branch",
			wantPath: "task.yaml",
		},
		{
			name:     "raw commit URL",
			url:      "https://gitea.example.com/owner/repo/raw/commit/abc123def/task.yaml",
			wantOrg:  "owner",
			wantRepo: "repo",
			wantRef:  "abc123def",
			wantPath: "task.yaml",
		},
		{
			name:     "raw tag URL",
			url:      "https://gitea.example.com/owner/repo/raw/tag/v2.0/path/to/task.yaml",
			wantOrg:  "owner",
			wantRepo: "repo",
			wantRef:  "v2.0",
			wantPath: "path/to/task.yaml",
		},
		{
			name:     "URL encoded path",
			url:      "https://gitea.example.com/owner/repo/src/branch/main/path%2Fto%2Ftask.yaml",
			wantOrg:  "owner",
			wantRepo: "repo",
			wantRef:  "main",
			wantPath: "path/to/task.yaml",
		},
		{
			name:    "too short URL",
			url:     "https://gitea.example.com/owner/repo",
			wantErr: true,
		},
		{
			name:    "invalid action segment",
			url:     "https://gitea.example.com/owner/repo/blob/branch/main/task.yaml",
			wantErr: true,
		},
		{
			name:    "invalid ref type",
			url:     "https://gitea.example.com/owner/repo/src/invalid/main/task.yaml",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			org, repo, path, ref, err := splitGiteaURL(tt.url)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantOrg, org)
			assert.Equal(t, tt.wantRepo, repo)
			assert.Equal(t, tt.wantRef, ref)
			assert.Equal(t, tt.wantPath, path)
		})
	}
}

func TestGetTaskURI(t *testing.T) {
	tests := []struct {
		name       string
		eventURL   string
		uri        string
		wantRet    string
		wantFound  bool
		wantErr    bool
		fileExists bool
	}{
		{
			name:       "fetch task from src branch URL",
			eventURL:   "https://gitea.example.com/owner/repo/pulls/1",
			uri:        "https://gitea.example.com/owner/repo/src/branch/main/task.yaml",
			wantRet:    "hello world",
			wantFound:  true,
			fileExists: true,
		},
		{
			name:       "fetch task from raw branch URL",
			eventURL:   "https://gitea.example.com/owner/repo/pulls/1",
			uri:        "https://gitea.example.com/owner/repo/raw/branch/main/task.yaml",
			wantRet:    "hello world",
			wantFound:  true,
			fileExists: true,
		},
		{
			name:       "fetch task from tag URL",
			eventURL:   "https://gitea.example.com/owner/repo/pulls/1",
			uri:        "https://gitea.example.com/owner/repo/src/tag/v1.0/task.yaml",
			wantRet:    "hello world",
			wantFound:  true,
			fileExists: true,
		},
		{
			name:      "different host returns not found",
			eventURL:  "https://gitea.example.com/owner/repo/pulls/1",
			uri:       "https://other.example.com/owner/repo/src/branch/main/task.yaml",
			wantFound: false,
		},
		{
			name:     "bad URI format",
			eventURL: "https://gitea.example.com/owner/repo/pulls/1",
			uri:      "https://gitea.example.com/owner/repo",
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()

			if tt.fileExists {
				mux.HandleFunc("/repos/owner/repo/raw/", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, tt.wantRet)
				})
			}

			p := &Provider{giteaClient: fakeclient}
			event := info.NewEvent()
			event.URL = tt.eventURL

			found, content, err := p.GetTaskURI(context.Background(), event, tt.uri)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetTaskURI() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, tt.wantFound, found)
			if tt.wantFound {
				assert.Equal(t, tt.wantRet, content)
			}
		})
	}
}
