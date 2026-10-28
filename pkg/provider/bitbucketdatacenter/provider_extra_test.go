package bitbucketdatacenter

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	"github.com/tektoncd/pipelines-as-code/pkg/params/settings"
	"github.com/tektoncd/pipelines-as-code/pkg/params/triggertype"
	"github.com/tektoncd/pipelines-as-code/pkg/provider"
	bbtest "github.com/tektoncd/pipelines-as-code/pkg/provider/bitbucketdatacenter/test"
	"github.com/tektoncd/pipelines-as-code/pkg/provider/status"
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
				got := (&Provider{}).GetTemplate(provider.PipelineRunStatusType)
				assert.Equal(t, provider.GetMarkdownTemplate(provider.PipelineRunStatusType), got)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestGetCommitStatusesCurrentNoop(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, mux *http.ServeMux, event *info.Event)
	}{
		{
			name: "ignores successful build status endpoint",
			setup: func(t *testing.T, mux *http.ServeMux, event *info.Event) {
				t.Helper()
				mux.HandleFunc(fmt.Sprintf("/commits/%s", event.SHA), func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"values":[{"key":"ci","state":"SUCCESSFUL"}]}`)
				})
			},
		},
		{
			name: "ignores build status API error",
			setup: func(t *testing.T, mux *http.ServeMux, event *info.Event) {
				t.Helper()
				mux.HandleFunc(fmt.Sprintf("/commits/%s", event.SHA), func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusInternalServerError)
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, mux, teardown, _ := bbtest.SetupBBDataCenterClient(t)
			defer teardown()
			event := bbtest.MakeEvent(nil)
			tt.setup(t, mux, event)

			got, err := (&Provider{client: client}).GetCommitStatuses(context.Background(), event)
			assert.NilError(t, err)
			assert.Equal(t, 0, len(got))
		})
	}
}

func TestCreateStatusAdditionalBranches(t *testing.T) {
	tests := []struct {
		name    string
		status  status.StatusOpts
		event   *info.Event
		setup   func(t *testing.T, mux *http.ServeMux, event *info.Event)
		wantErr string
	}{
		{
			name: "status API error",
			status: status.StatusOpts{
				Conclusion: status.ConclusionFailure,
				Text:       "failed",
			},
			event: bbtest.MakeEvent(nil),
			setup: func(t *testing.T, mux *http.ServeMux, event *info.Event) {
				t.Helper()
				mux.HandleFunc(fmt.Sprintf("/commits/%s", event.SHA), func(rw http.ResponseWriter, _ *http.Request) {
					rw.WriteHeader(http.StatusInternalServerError)
				})
			},
			wantErr: "No message available",
		},
		{
			name: "completed pull request creates comment",
			status: status.StatusOpts{
				Conclusion:              status.ConclusionSuccess,
				Status:                  "completed",
				Text:                    "completed text",
				OriginalPipelineRunName: "pipeline",
			},
			event: bbtest.MakeEvent(&info.Event{
				EventType:         triggertype.PullRequest.String(),
				TriggerTarget:     triggertype.PullRequest,
				PullRequestNumber: 7,
			}),
			setup: func(t *testing.T, mux *http.ServeMux, event *info.Event) {
				t.Helper()
				bbtest.MuxCreateAndTestCommitStatus(t, mux, event, "completed text", status.StatusOpts{
					Conclusion:              status.ConclusionSuccess,
					Status:                  "completed",
					Text:                    "completed text",
					OriginalPipelineRunName: "pipeline",
				})
				bbtest.MuxCreateComment(t, mux, event, "completed text", event.PullRequestNumber)
			},
		},
		{
			name: "cancelled conclusion keeps unknown state",
			status: status.StatusOpts{
				Conclusion: status.ConclusionCancelled,
				Text:       "cancelled",
			},
			event: bbtest.MakeEvent(nil),
			setup: func(t *testing.T, mux *http.ServeMux, event *info.Event) {
				t.Helper()
				bbtest.MuxCreateAndTestCommitStatus(t, mux, event, "cancelled", status.StatusOpts{
					Conclusion: status.ConclusionCancelled,
					Text:       "cancelled",
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, mux, teardown, tURL := bbtest.SetupBBDataCenterClient(t)
			defer teardown()
			tt.setup(t, mux, tt.event)

			p := &Provider{
				client:  client,
				baseURL: tURL,
				pacInfo: &info.PacOpts{Settings: settings.Settings{
					ApplicationName: "PAC",
				}},
			}
			err := p.CreateStatus(context.Background(), tt.event, tt.status)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
		})
	}
}

func TestSetClientCreatesClientWhenMissing(t *testing.T) {
	tests := []struct {
		name      string
		event     *info.Event
		setup     func(t *testing.T, mux *http.ServeMux)
		wantAPI   string
		wantLog   string
		wantError string
	}{
		{
			name: "creates client and appends rest suffix",
			event: &info.Event{
				Provider: &info.Provider{
					User:  "foo",
					Token: "bar",
				},
				EventType: "push",
			},
			setup: func(t *testing.T, mux *http.ServeMux) {
				t.Helper()
				mux.HandleFunc("/whoami", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, "foo")
				})
				mux.HandleFunc("/users/foo", func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprint(rw, `{"name": "foo"}`)
				})
			},
			wantLog: "bitbucket-datacenter: initialized client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mux, teardown, tURL := bbtest.SetupBBDataCenterClient(t)
			defer teardown()
			tt.event.Provider.URL = tURL
			tt.wantAPI = tURL + "/rest"
			tt.setup(t, mux)

			core, observer := zapobserver.New(zap.InfoLevel)
			logger := zap.New(core).Sugar()
			p := &Provider{Logger: logger}
			run := params.New()
			err := p.SetClient(context.Background(), run, tt.event, nil, nil)
			if tt.wantError != "" {
				assert.ErrorContains(t, err, tt.wantError)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, p.client != nil)
			assert.Equal(t, tt.wantAPI, p.apiURL)
			assert.Assert(t, p.run == run)
			assert.Equal(t, tt.event.EventType, p.triggerEvent)
			logs := observer.TakeAll()
			assert.Assert(t, len(logs) > 0)
			assert.Assert(t, strings.Contains(logs[0].Message, tt.wantLog))
		})
	}
}
