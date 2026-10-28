package bitbucketcloud

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/events"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	"github.com/tektoncd/pipelines-as-code/pkg/params/settings"
	"github.com/tektoncd/pipelines-as-code/pkg/params/triggertype"
	"github.com/tektoncd/pipelines-as-code/pkg/provider"
	bbcloudtest "github.com/tektoncd/pipelines-as-code/pkg/provider/bitbucketcloud/test"
	"github.com/tektoncd/pipelines-as-code/pkg/provider/status"
	httptesthelper "github.com/tektoncd/pipelines-as-code/pkg/test/http"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
)

func TestProviderNoopMethods(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "create comment",
			run: func(t *testing.T) {
				t.Helper()
				err := (&Provider{}).CreateComment(context.Background(), info.NewEvent(), "body", "marker")
				assert.NilError(t, err)
			},
		},
		{
			name: "check policy allowing",
			run: func(t *testing.T) {
				t.Helper()
				allowed, reason := (&Provider{}).CheckPolicyAllowing(context.Background(), info.NewEvent(), []string{"team"})
				assert.Equal(t, false, allowed)
				assert.Equal(t, "", reason)
			},
		},
		{
			name: "get task URI",
			run: func(t *testing.T) {
				t.Helper()
				found, content, err := (&Provider{}).GetTaskURI(context.Background(), info.NewEvent(), "https://example.test/task.yaml")
				assert.NilError(t, err)
				assert.Equal(t, false, found)
				assert.Equal(t, "", content)
			},
		},
		{
			name: "get files",
			run: func(t *testing.T) {
				t.Helper()
				got, err := (&Provider{}).GetFiles(context.Background(), info.NewEvent())
				assert.NilError(t, err)
				assert.Equal(t, 0, len(got.All))
				assert.Equal(t, 0, len(got.Added))
				assert.Equal(t, 0, len(got.Modified))
				assert.Equal(t, 0, len(got.Deleted))
				assert.Equal(t, 0, len(got.Renamed))
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
				got := (&Provider{}).GetTemplate(provider.QueueingPipelineType)
				assert.Equal(t, provider.GetMarkdownTemplate(provider.QueueingPipelineType), got)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestCheckFromPublicCloudIPSErrors(t *testing.T) {
	tests := []struct {
		name          string
		sourceIP      string
		allowedConfig map[string]map[string]string
		wantErr       string
	}{
		{
			name:    "missing source IP",
			wantErr: "no source_ip has been passed",
		},
		{
			name:     "IP range URL error",
			sourceIP: "1.2.3.4",
			allowedConfig: map[string]map[string]string{
				bitbucketCloudIPrangesList: {
					"body": "server error",
					"code": "500",
				},
			},
			wantErr: "Non-OK HTTP status: 500",
		},
		{
			name:     "invalid IP range JSON",
			sourceIP: "1.2.3.4",
			allowedConfig: map[string]map[string]string{
				bitbucketCloudIPrangesList: {
					"body": "not-json",
					"code": "200",
				},
			},
			wantErr: "invalid character",
		},
		{
			name:     "invalid CIDR",
			sourceIP: "1.2.3.4",
			allowedConfig: map[string]map[string]string{
				bitbucketCloudIPrangesList: {
					"body": `{"items": [{"cidr": "not-a-cidr"}]}`,
					"code": "200",
				},
			},
			wantErr: "invalid CIDR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &Provider{pacInfo: &info.PacOpts{Settings: settings.Settings{BitbucketCloudCheckSourceIP: true}}}
			run := &params.Run{}
			if tt.allowedConfig != nil {
				httpTestClient := httptesthelper.MakeHTTPTestClient(tt.allowedConfig)
				run.Clients.HTTP = *httpTestClient
			}
			allowed, err := v.checkFromPublicCloudIPS(context.Background(), run, tt.sourceIP)
			assert.Equal(t, false, allowed)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestParsePayloadTypeBranches(t *testing.T) {
	tests := []struct {
		name       string
		event      string
		wantNil    bool
		wantErr    string
		wantTypeOf string
	}{
		{
			name:    "unsupported pull request event",
			event:   "pullrequest:unknown",
			wantErr: "event pullrequest:unknown is not supported",
		},
		{
			name:    "unknown event returns nil payload",
			event:   "repo:unknown",
			wantNil: true,
		},
		{
			name:       "push event",
			event:      "repo:push",
			wantTypeOf: "*types.PushRequestEvent",
		},
		{
			name:       "pull request event",
			event:      "pullrequest:created",
			wantTypeOf: "*types.PullRequestEvent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePayloadType(tt.event, `{}`)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			if tt.wantNil {
				assert.Assert(t, got == nil)
				return
			}
			assert.Equal(t, tt.wantTypeOf, fmt.Sprintf("%T", got))
		})
	}
}

func TestGetFileInsideRepoDefaultBranch(t *testing.T) {
	tests := []struct {
		name       string
		provenance string
		want       string
	}{
		{
			name:       "uses default branch provenance",
			provenance: "default_branch",
			want:       "owners",
		},
		{
			name: "uses source revision",
			want: "owners",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bbclient, mux, teardown := bbcloudtest.SetupBBCloudClient(t)
			defer teardown()
			event := bbcloudtest.MakeEvent(nil)
			bbcloudtest.MuxFiles(t, mux, event, map[string]string{"OWNERS": tt.want}, tt.provenance)

			p := &Provider{
				bbClient:   bbclient,
				provenance: tt.provenance,
			}
			got, err := p.GetFileInsideRepo(context.Background(), event, "OWNERS", "")
			assert.NilError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCreateStatusErrorBranches(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(t *testing.T, mux *http.ServeMux, event *info.Event)
		nilBB   bool
		wantErr string
	}{
		{
			name:    "nil client",
			nilBB:   true,
			wantErr: "no token has been set",
		},
		{
			name: "commit status error still reports comment error",
			setup: func(t *testing.T, mux *http.ServeMux, event *info.Event) {
				t.Helper()
				mux.HandleFunc(fmt.Sprintf("/repositories/%s/%s/commit/%s/statuses/build", event.Organization, event.Repository, event.SHA),
					func(rw http.ResponseWriter, _ *http.Request) {
						rw.WriteHeader(http.StatusInternalServerError)
					})
				mux.HandleFunc(fmt.Sprintf("/repositories/%s/%s/pullrequests/%d/comments", event.Organization, event.Repository, event.PullRequestNumber),
					func(rw http.ResponseWriter, _ *http.Request) {
						rw.WriteHeader(http.StatusInternalServerError)
					})
			},
			wantErr: "500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bbclient, mux, teardown := bbcloudtest.SetupBBCloudClient(t)
			defer teardown()
			if tt.nilBB {
				bbclient = nil
			}
			event := bbcloudtest.MakeEvent(&info.Event{
				EventType:     triggertype.PullRequest.String(),
				TriggerTarget: triggertype.PullRequest,
			})
			if tt.setup != nil {
				tt.setup(t, mux, event)
			}

			core, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(core).Sugar()
			p := &Provider{
				bbClient:     bbclient,
				eventEmitter: events.NewEventEmitter(nil, logger),
				pacInfo:      &info.PacOpts{Settings: settings.Settings{ApplicationName: settings.PACApplicationNameDefaultValue}},
			}
			err := p.CreateStatus(context.Background(), event, status.StatusOpts{
				Conclusion:              status.ConclusionSuccess,
				Status:                  "completed",
				Text:                    "comment text",
				OriginalPipelineRunName: "pipeline",
			})
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}
