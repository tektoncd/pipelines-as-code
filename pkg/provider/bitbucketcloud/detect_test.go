package bitbucketcloud

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	"gotest.tools/v3/assert"
)

func TestProviderDetect(t *testing.T) {
	tests := []struct {
		name           string
		wantErrString  string
		isBC           bool
		processReq     bool
		event          any
		eventType      string
		wantReason     string
		wantLogSnippet string
	}{
		{
			name:       "not a bitbucket cloud Event",
			eventType:  "",
			isBC:       false,
			processReq: false,
		},
		{
			name:           "invalid bitbucket cloud Event",
			eventType:      "validator",
			isBC:           false,
			processReq:     false,
			wantLogSnippet: "skip processing event",
		},
		{
			event: types.PushRequestEvent{
				Push: types.Push{
					Changes: []types.Change{
						{
							New: types.ChangeType{Name: "new"},
							Old: types.ChangeType{Name: "old"},
						},
					},
				},
			},
			eventType:  "repo:push",
			isBC:       true,
			processReq: true,
			name:       "push event",
		},
		{
			name:       "pull_request event",
			event:      types.PullRequestEvent{},
			eventType:  "pullrequest:created",
			isBC:       true,
			processReq: true,
		},
		{
			name:       "updated pull_request event",
			event:      types.PullRequestEvent{},
			eventType:  "pullrequest:updated",
			isBC:       true,
			processReq: true,
		},
		{
			name: "retest comment",
			event: types.PullRequestEvent{
				Comment: types.Comment{
					Content: types.Content{
						Raw: "/retest",
					},
				},
			},
			eventType:  "pullrequest:comment_created",
			isBC:       true,
			processReq: true,
		},
		{
			name: "random comment",
			event: types.PullRequestEvent{
				Comment: types.Comment{
					Content: types.Content{
						Raw: "abc",
					},
				},
			},
			eventType:  "pullrequest:comment_created",
			isBC:       true,
			processReq: false,
		},
		{
			name: "ok-to-test comment",
			event: types.PullRequestEvent{
				Comment: types.Comment{
					Content: types.Content{
						Raw: "/ok-to-test",
					},
				},
			},
			eventType:  "pullrequest:comment_created",
			isBC:       true,
			processReq: true,
		},
		{
			name: "cancel comment",
			event: types.PullRequestEvent{
				Comment: types.Comment{
					Content: types.Content{
						Raw: "/cancel",
					},
				},
			},
			eventType:  "pullrequest:comment_created",
			isBC:       true,
			processReq: true,
		},
		{
			name: "cancel a pr",
			event: types.PullRequestEvent{
				Comment: types.Comment{
					Content: types.Content{
						Raw: "/cancel dummy",
					},
				},
			},
			eventType:  "pullrequest:comment_created",
			isBC:       true,
			processReq: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, logCatcher := logger.GetLogger()
			bprovider := Provider{Logger: logger}

			jeez, err := json.Marshal(tt.event)
			if err != nil {
				assert.NilError(t, err)
			}

			header := http.Header{}
			header.Set("X-Event-Key", tt.eventType)
			header.Set("X-Request-UUID", "1234567890")
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
			assert.Equal(t, tt.isBC, isBS)
			assert.Equal(t, tt.processReq, processReq)

			if tt.wantLogSnippet != "" {
				assert.Assert(t, logCatcher.FilterMessageSnippet(tt.wantLogSnippet).Len() > 0, logCatcher.All())
			}

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
