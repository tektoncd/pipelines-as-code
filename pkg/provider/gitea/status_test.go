package gitea

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/test"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"
)

func TestProviderCreateStatus(t *testing.T) {
	type args struct {
		event      *info.Event
		statusOpts status.StatusOpts
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "Test with success conclusion",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Conclusion: status.ConclusionSuccess,
				},
			},
			wantErr: false,
		},
		{
			name: "Test with failure conclusion",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Conclusion: status.ConclusionFailure,
				},
			},
			wantErr: false,
		},
		{
			name: "Test with pending conclusion",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Conclusion: status.ConclusionPending,
				},
			},
			wantErr: false,
		},
		{
			name: "Test with neutral conclusion",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Conclusion: status.ConclusionNeutral,
				},
			},
			wantErr: false,
		},
		{
			name: "Test with in_progress status",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Status: "in_progress",
				},
			},
			wantErr: false,
		},
		{
			name: "Test with onpr",
			args: args{
				event: &info.Event{},
				statusOpts: status.StatusOpts{
					Status:          "in_progress",
					PipelineRunName: "mypr",
				},
			},
			wantErr: false,
		},
		{
			name: "Test with ok-to-test event",
			args: args{
				event: &info.Event{EventType: triggertype.OkToTest.String()},
				statusOpts: status.StatusOpts{
					Status:          "in_progress",
					PipelineRunName: "mypr",
				},
			},
			wantErr: false,
		},
		{
			name: "Test with oncomment event",
			args: args{
				event: &info.Event{EventType: opscomments.OkToTestCommentEventType.String()},
				statusOpts: status.StatusOpts{
					Status:          "in_progress",
					PipelineRunName: "mypr",
				},
			},
			wantErr: false,
		},
		{
			name: "Test status_text",
			args: args{
				event: &info.Event{EventType: triggertype.PullRequest.String()},
				statusOpts: status.StatusOpts{
					Status:          "in_progress",
					PipelineRunName: "mypr",
					Text:            "mytext",
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()
			run := params.New()
			p := &Provider{
				giteaClient: fakeclient, // Set this to a valid client for the tests where wantErr is false
				run:         run,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						ApplicationName: settings.PACApplicationNameDefaultValue,
					},
				},
			}
			tt.args.event.Organization = "myorg"
			tt.args.event.Repository = "myrepo"

			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/0/comments", tt.args.event.Organization, tt.args.event.Repository), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(rw, `{"state":"success"}`)
			})
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/statuses/", tt.args.event.Organization, tt.args.event.Repository), func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(rw, `{"state":"success"}`)
			})
			if err := p.CreateStatus(context.Background(), tt.args.event, tt.args.statusOpts); (err != nil) != tt.wantErr {
				t.Errorf("Provider.CreateStatus() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProviderCreateStatusCommit(t *testing.T) {
	type args struct {
		event   *info.Event
		pacopts *info.PacOpts
		status  status.StatusOpts
	}
	tests := []struct {
		name                            string
		args                            args
		wantErr                         bool
		wantCommentJSON, wantStatusJSON string
	}{
		{
			name: "success",
			args: args{
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					TriggerTarget:     "pull_request",
					SHA:               "123456",
				},
				status: status.StatusOpts{
					Conclusion: status.ConclusionNeutral,
				},
			},
			wantStatusJSON: `{"state":"success","target_url":"","description":"","context":"myapp"}`,
		},
		{
			name: "pending",
			args: args{
				status: status.StatusOpts{
					Conclusion: status.ConclusionPending,
					Title:      "Pipeline run for myapp has been triggered",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					TriggerTarget:     "pull_request",
					SHA:               "123456",
				},
			},
			wantStatusJSON: `{"state":"pending","target_url":"","description":"Pipeline run for myapp has been triggered","context":"myapp"}`,
		},
		{
			name: "pending from status",
			args: args{
				status: status.StatusOpts{
					Status: "in_progress",
					Title:  "Pipeline run for myapp has been triggered",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					TriggerTarget:     "pull_request",
					SHA:               "123456",
				},
			},
			wantStatusJSON: `{"state":"pending","target_url":"","description":"Pipeline run for myapp has been triggered","context":"myapp"}`,
		},
		{
			name: "ok-to-test",
			args: args{
				status: status.StatusOpts{
					Conclusion: status.ConclusionPending,
					Title:      "Pipeline run for myapp has been triggered",
					Text:       "time to get started",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					EventType:         triggertype.OkToTest.String(),
					SHA:               "123456",
				},
			},
			wantStatusJSON:  `{"state":"pending","target_url":"","description":"Pipeline run for myapp has been triggered","context":"myapp"}`,
			wantCommentJSON: `{"body":"\ntime to get started"}`,
		},
		{
			name: "cancel",
			args: args{
				status: status.StatusOpts{
					Conclusion: status.ConclusionPending,
					Title:      "Pipeline run for myapp has been triggered",
					Text:       "time to get started",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					EventType:         triggertype.Cancel.String(),
					SHA:               "123456",
				},
			},
			wantStatusJSON:  `{"state":"pending","target_url":"","description":"Pipeline run for myapp has been triggered","context":"myapp"}`,
			wantCommentJSON: `{"body":"\ntime to get started"}`,
		},
		{
			name: "retest",
			args: args{
				status: status.StatusOpts{
					Conclusion: status.ConclusionPending,
					Title:      "Pipeline run for myapp has been triggered",
					Text:       "time to get started",
				},
				pacopts: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "myapp",
				}},
				event: &info.Event{
					Organization:      "myorg",
					Repository:        "myrepo",
					PullRequestNumber: 1,
					EventType:         triggertype.Retest.String(),
					SHA:               "123456",
				},
			},
			wantStatusJSON:  `{"state":"pending","target_url":"","description":"Pipeline run for myapp has been triggered","context":"myapp"}`,
			wantCommentJSON: `{"body":"\ntime to get started"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()

			// Mock the CreateStatus API
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/statuses/%s", tt.args.event.Organization, tt.args.event.Repository, tt.args.event.SHA), func(rw http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(rw, "Failed to read request body", http.StatusInternalServerError)
					return
				}

				if res := cmp.Diff(string(tt.wantStatusJSON), string(body)); res != "" {
					t.Errorf("Received: %s Diff %s:", string(body), res)
				}

				_, _ = rw.Write([]byte(`{"state":"success"}`))
			})

			// Mock the CreateIssueComment API
			mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/issues/%d/comments", tt.args.event.Organization, tt.args.event.Repository, tt.args.event.PullRequestNumber), func(rw http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(rw, "Failed to read request body", http.StatusInternalServerError)
					return
				}

				if res := cmp.Diff(string(tt.wantCommentJSON), string(body)); res != "" {
					t.Errorf("Received: %s Diff %s:", string(body), res)
				}
				_, _ = rw.Write([]byte(`{"body":"Pipeline run for myapp has been triggered"}`))
			})

			v := &Provider{
				giteaClient: fakeclient,
			}

			if err := v.createStatusCommit(context.Background(), tt.args.event, tt.args.pacopts, tt.args.status); (err != nil) != tt.wantErr {
				t.Errorf("Provider.createStatusCommit() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProviderCreateStatusCommitRetryOnTransientError(t *testing.T) {
	tests := []struct {
		name           string
		failCount      int
		errorMessage   string
		wantErr        bool
		wantRetryCount int
	}{
		{
			name:           "retry on user does not exist error",
			failCount:      2,
			errorMessage:   "user does not exist [uid: 0, name: ]",
			wantErr:        false,
			wantRetryCount: 2,
		},
		{
			name:           "fail after max retries",
			failCount:      5, // More than maxRetries (3)
			errorMessage:   "user does not exist [uid: 0, name: ]",
			wantErr:        true,
			wantRetryCount: 3,
		},
		{
			name:           "no retry on other errors",
			failCount:      1,
			errorMessage:   "some other error",
			wantErr:        true,
			wantRetryCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeclient, mux, teardown := tgitea.Setup(t)
			defer teardown()

			observer, logs := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			callCount := 0
			mux.HandleFunc("/repos/myorg/myrepo/statuses/abc123", func(rw http.ResponseWriter, _ *http.Request) {
				callCount++
				if callCount <= tt.failCount {
					rw.WriteHeader(http.StatusInternalServerError)
					_, _ = fmt.Fprintf(rw, `{"message": "%s"}`, tt.errorMessage)
					return
				}
				_, _ = rw.Write([]byte(`{"state":"success"}`))
			})

			fc := clockwork.NewFakeClock()
			v := &Provider{
				giteaClient: fakeclient,
				Logger:      logger,
				clock:       fc,
			}

			event := &info.Event{
				Organization: "myorg",
				Repository:   "myrepo",
				SHA:          "abc123",
			}
			pacopts := &info.PacOpts{Settings: settings.Settings{
				ApplicationName: "myapp",
			}}
			status := status.StatusOpts{
				Conclusion: "success",
			}

			ctx := context.Background()
			if strings.Contains(tt.errorMessage, "user does not exist") && tt.failCount > 0 {
				// Drive the fake clock only for retryable cases that actually sleep.
				go func() {
					for i := range 3 {
						fc.BlockUntilContext(ctx, 1) //nolint:errcheck
						fc.Advance(time.Duration(i+1) * 500 * time.Millisecond)
					}
				}()
			}

			err := v.createStatusCommit(ctx, event, pacopts, status)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected an error but got none")
			} else {
				assert.NilError(t, err)
			}

			// Verify the number of API calls made
			isTransientError := strings.Contains(tt.errorMessage, "user does not exist")
			switch {
			case tt.wantErr && isTransientError:
				// For transient errors, we should have tried maxRetries (3) times
				assert.Equal(t, 3, callCount, "expected 3 retries for transient error")
			case tt.wantErr:
				// For non-retryable errors, we should have tried only once
				assert.Equal(t, 1, callCount, "expected only 1 attempt for non-retryable error")
			default:
				// For success after retries, we should have failCount+1 calls
				assert.Equal(t, tt.failCount+1, callCount, "expected failCount+1 calls for eventual success")
			}

			// Verify warning logs were emitted for retries on "user does not exist" errors
			if strings.Contains(tt.errorMessage, "user does not exist") && tt.failCount > 0 {
				retryLogs := 0
				for _, log := range logs.All() {
					if strings.Contains(log.Message, "CreateStatus failed with transient error") {
						retryLogs++
					}
				}
				expectedLogs := min(tt.failCount, 3)
				assert.Equal(t, expectedLogs, retryLogs, "unexpected number of retry warning logs")
			}
		})
	}
}

func TestCreateStatusUpdateCommentNormalizesBreaks(t *testing.T) {
	fakeclient, mux, teardown := tgitea.Setup(t)
	defer teardown()

	var createCommentBody string
	mux.HandleFunc("/repos/org/repo/statuses/", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{"state":"pending"}`)
	})
	mux.HandleFunc("/repos/org/repo/issues/123/comments", func(rw http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(rw, `[]`)
		case http.MethodPost:
			b, err := io.ReadAll(r.Body)
			assert.NilError(t, err)
			createCommentBody = string(b)
			fmt.Fprint(rw, `{}`)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	})

	p := &Provider{
		giteaClient: fakeclient,
		pacInfo: &info.PacOpts{
			Settings: settings.Settings{
				ApplicationName: settings.PACApplicationNameDefaultValue,
			},
		},
		repo: &v1alpha1.Repository{
			Spec: v1alpha1.RepositorySpec{
				Settings: &v1alpha1.Settings{
					Forgejo: &v1alpha1.ForgejoSettings{CommentStrategy: provider.UpdateCommentStrategy},
				},
			},
		},
	}

	err := p.CreateStatus(context.Background(), &info.Event{
		Organization:      "org",
		Repository:        "repo",
		SHA:               "abc123",
		PullRequestNumber: 123,
		EventType:         triggertype.PullRequest.String(),
		TriggerTarget:     triggertype.PullRequest,
	}, status.StatusOpts{
		Status:                  "in_progress",
		Text:                    "line1<br>line2",
		OriginalPipelineRunName: "demo",
		DetailsURL:              "https://example.test/log",
	})
	assert.NilError(t, err)
	assert.Assert(t, !strings.Contains(createCommentBody, "<br>"), "comment body should not contain raw <br>: %s", createCommentBody)
	assert.Assert(t, strings.Contains(createCommentBody, "line1\\nline2"), "comment body should contain normalized newline: %s", createCommentBody)

	golden.Assert(t, createCommentBody, strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
}

func TestFormatPipelineCommentEmoji(t *testing.T) {
	p := &Provider{
		pacInfo: &info.PacOpts{
			Settings: settings.Settings{
				ApplicationName: settings.PACApplicationNameDefaultValue,
			},
		},
	}

	tests := []struct {
		name   string
		status status.StatusOpts
		emoji  string
	}{
		{
			name: "failure conclusion uses failure emoji",
			status: status.StatusOpts{
				Conclusion:              status.ConclusionFailure,
				Title:                   "Failed",
				Text:                    "details",
				OriginalPipelineRunName: "demo",
				DetailsURL:              "https://example.test/log",
			},
			emoji: "❌",
		},
		{
			name: "in progress status uses rocket emoji",
			status: status.StatusOpts{
				Status:                  "in_progress",
				Title:                   "CI has Started",
				Text:                    "details",
				OriginalPipelineRunName: "demo",
				DetailsURL:              "https://example.test/log",
			},
			emoji: "🚀",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.formatPipelineComment("abc123", tt.status)
			assert.Assert(t, strings.HasPrefix(got, tt.emoji+" "), "expected prefix %q in comment %q", tt.emoji+" ", got)
		})
	}
}

func TestGetCommitStatuses(t *testing.T) {
	tests := []struct {
		name        string
		event       *info.Event
		nilClient   bool
		mockHandler func(http.ResponseWriter, *http.Request)
		want        []provider.CommitStatusInfo
		wantErr     string
	}{
		{
			name: "happy path with multiple statuses",
			event: &info.Event{
				Organization: "org",
				Repository:   "repo",
				SHA:          "abc123",
			},
			mockHandler: func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, `[
					{"context":"Pipelines as Code CI / pr-one","status":"success"},
					{"context":"Pipelines as Code CI / pr-two","status":"failure"}
				]`)
			},
			want: []provider.CommitStatusInfo{
				{Name: "Pipelines as Code CI / pr-one", Status: "success"},
				{Name: "Pipelines as Code CI / pr-two", Status: "failure"},
			},
		},
		{
			name: "deduplicates identical statuses",
			event: &info.Event{
				Organization: "org",
				Repository:   "repo",
				SHA:          "abc123",
			},
			mockHandler: func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, `[
					{"context":"CI / build","status":"success"},
					{"context":"CI / build","status":"success"},
					{"context":"CI / build","status":"failure"}
				]`)
			},
			want: []provider.CommitStatusInfo{
				{Name: "CI / build", Status: "success"},
				{Name: "CI / build", Status: "failure"},
			},
		},
		{
			name: "empty response",
			event: &info.Event{
				Organization: "org",
				Repository:   "repo",
				SHA:          "abc123",
			},
			mockHandler: func(rw http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(rw, `[]`)
			},
		},
		{
			name:      "nil client returns error",
			nilClient: true,
			event: &info.Event{
				Organization: "org",
				Repository:   "repo",
				SHA:          "abc123",
			},
			wantErr: "no gitea client has been initialized",
		},
		{
			name: "API error",
			event: &info.Event{
				Organization: "org",
				Repository:   "repo",
				SHA:          "abc123",
			},
			mockHandler: func(rw http.ResponseWriter, _ *http.Request) {
				rw.WriteHeader(http.StatusInternalServerError)
			},
			wantErr: "500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p *Provider
			if tt.nilClient {
				p = &Provider{}
			} else {
				fakeclient, mux, teardown := tgitea.Setup(t)
				defer teardown()

				mux.HandleFunc(
					fmt.Sprintf("/repos/%s/%s/commits/%s/statuses",
						tt.event.Organization, tt.event.Repository, tt.event.SHA),
					tt.mockHandler,
				)
				p = &Provider{giteaClient: fakeclient}
			}

			got, err := p.GetCommitStatuses(context.Background(), tt.event)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}
