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
			name: "retest comment",
			event: types.PullRequestEvent{
				PullRequest: types.PullRequest{Open: true},
				Comment:     types.ActivityComment{Text: "/retest"},
			},
			eventType:  "pr:comment:added",
			isBS:       true,
			processReq: true,
		},
		{
			name: "random comment",
			event: types.PullRequestEvent{
				PullRequest: types.PullRequest{Open: true},
				Comment:     types.ActivityComment{Text: "random string, ignore me :)"},
			},
			eventType:  "pr:comment:added",
			isBS:       true,
			processReq: true,
		},
		{
			name: "ok-to-test comment",
			event: types.PullRequestEvent{
				PullRequest: types.PullRequest{Open: true},
				Comment:     types.ActivityComment{Text: "/ok-to-test"},
			},
			eventType:  "pr:comment:added",
			isBS:       true,
			processReq: true,
		},
		{
			name: "cancel comment",
			event: types.PullRequestEvent{
				PullRequest: types.PullRequest{Open: true},
				Comment:     types.ActivityComment{Text: "/cancel"},
			},
			eventType:  "pr:comment:added",
			isBS:       true,
			processReq: true,
		},
		{
			name: "cancel a pipelinerun comment",
			event: types.PullRequestEvent{
				PullRequest: types.PullRequest{Open: true},
				Comment:     types.ActivityComment{Text: "/cancel dummy"},
			},
			eventType:  "pr:comment:added",
			isBS:       true,
			processReq: true,
		},
		{
			name: "comment on closed pull request",
			event: types.PullRequestEvent{
				PullRequest: types.PullRequest{Open: false},
				Comment:     types.ActivityComment{Text: "/retest"},
			},
			eventType:  "pr:comment:added",
			isBS:       true,
			processReq: false,
			wantReason: "comments on closed pull requests are not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bprovider := Provider{}
			logger, logCatcher := logger.GetLogger()

			jeez, err := json.Marshal(tt.event)
			if err != nil {
				assert.NilError(t, err)
			}

			header := http.Header{}
			header.Set("X-Event-Key", tt.eventType)
			header.Set("X-Request-ID", "1234567890")
			req := &http.Request{Header: header}
			isBS, processReq, logger, reason, err := bprovider.Detect(req, string(jeez), logger)
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

			logger.Info("generate a log message to check if event-id is added to the logger")

			logs := logCatcher.All()
			for _, entry := range logs {
				for _, field := range entry.Context {
					if field.Key == "event-id" {
						assert.Equal(t, field.String, "1234567890")
					}
				}
			}
		})
	}
}
