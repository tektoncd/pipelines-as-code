package bitbucketdatacenter

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/types"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	"gotest.tools/v3/assert"
)

func TestProviderDetect(t *testing.T) {
	tests := []struct {
		name          string
		wantErrString string
		isBS          bool
		processReq    bool
		event         any
		eventType     string
		wantReason    string
	}{
		{
			name:       "not a bitbucket data center Event",
			eventType:  "",
			isBS:       false,
			processReq: false,
		},
		{
			name:       "invalid bitbucket data center Event",
			eventType:  "validator",
			isBS:       false,
			processReq: false,
		},
		{
			name: "push event",
			event: types.PushRequestEvent{
				Actor: types.UserWithLinks{
					ID: 111,
				},
				Repository: types.Repository{},
				Changes: []types.PushRequestEventChange{
					{
						ToHash: "test",
						RefID:  "refID",
					},
				},
			},
			eventType:  "repo:refs_changed",
			isBS:       true,
			processReq: true,
		},
		{
			name:       "pull_request event",
			event:      types.PullRequestEvent{},
			eventType:  "pr:opened",
			isBS:       true,
			processReq: true,
		},
		{
			name:       "updated pull_request event",
			event:      types.PullRequestEvent{},
			eventType:  "pr:from_ref_updated",
			isBS:       true,
			processReq: true,
		},
		{
			name:       "pull_request merged event",
			event:      types.PullRequestEvent{},
			eventType:  "pr:merged",
			isBS:       true,
			processReq: true,
		},
		{
			name:       "pull_request modified event",
			event:      types.PullRequestEvent{},
			eventType:  "pr:modified",
			isBS:       true,
			processReq: true,
		},
		{
			name: "retest comment",
			event: types.PullRequestEvent{
				Comment: types.ActivityComment{Text: "/retest"},
			},
			eventType:  "pr:comment:added",
			isBS:       true,
			processReq: true,
		},
		{
			name: "random comment",
			event: types.PullRequestEvent{
				Comment: types.ActivityComment{Text: "random string, ignore me :)"},
			},
			eventType:  "pr:comment:added",
			isBS:       true,
			processReq: false,
		},
		{
			name: "ok-to-test comment",
			event: types.PullRequestEvent{
				Comment: types.ActivityComment{Text: "/ok-to-test"},
			},
			eventType:  "pr:comment:added",
			isBS:       true,
			processReq: true,
		},
		{
			name: "cancel comment",
			event: types.PullRequestEvent{
				Comment: types.ActivityComment{Text: "/cancel"},
			},
			eventType:  "pr:comment:added",
			isBS:       true,
			processReq: true,
		},
		{
			name: "cancel a pipelinerun comment",
			event: types.PullRequestEvent{
				Comment: types.ActivityComment{Text: "/cancel dummy"},
			},
			eventType:  "pr:comment:added",
			isBS:       true,
			processReq: true,
		},
		{
			name:       "pull_request user approval event",
			event:      types.PullRequestEvent{},
			eventType:  "pr:reviewer:approved",
			isBS:       true,
			processReq: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bprovider := Provider{}
			logger, _ := logger.GetLogger()

			jeez, err := json.Marshal(tt.event)
			if err != nil {
				assert.NilError(t, err)
			}

			header := http.Header{}
			header.Set("X-Event-Key", tt.eventType)
			req := &http.Request{Header: header}
			isBS, processReq, _, reason, err := bprovider.Detect(req, string(jeez), logger)
			if tt.wantErrString != "" {
				assert.ErrorContains(t, err, tt.wantErrString)
				return
			}
			if tt.wantReason != "" {
				assert.Assert(t, strings.Contains(reason, tt.wantReason))
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.isBS, isBS)
			assert.Equal(t, tt.processReq, processReq)
		})
	}
}
