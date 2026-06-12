package bitbucketcloud

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	bbcloudtest "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/test"
	httptesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/http"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestParsePayload(t *testing.T) {
	tests := []struct {
		name                      string
		payloadEvent              any
		wantErr                   bool
		expectedSender            string
		expectedEventType         string
		expectedAccountID         string
		expectedSHA               string
		expectedRef               string
		eventType                 string
		sourceIP                  string
		allowedConfig             map[string]map[string]string
		additionalAllowedsourceIP string
		targetPipelinerun         string
		cancelPipelinerun         string
	}{
		{
			name:              "parse push request",
			payloadEvent:      bbcloudtest.MakePushEvent("PushAccountID", "Barbie", "slighlyhashed", "branch"),
			expectedSender:    "Barbie",
			expectedAccountID: "PushAccountID",
			expectedSHA:       "slighlyhashed",
			expectedRef:       "mychange",
			eventType:         "repo:push",
			expectedEventType: triggertype.Push.String(),
		},
		{
			name:              "parse push tag",
			payloadEvent:      bbcloudtest.MakePushEvent("PushAccountID", "Barbie", "slighlyhashed", "tag"),
			expectedSender:    "Barbie",
			expectedAccountID: "PushAccountID",
			expectedSHA:       "slighlyhashed",
			expectedRef:       "refs/tags/mychange",
			eventType:         "repo:push",
			expectedEventType: triggertype.Push.String(),
		},
		{
			name:              "parse pull request",
			payloadEvent:      bbcloudtest.MakePREvent("TheAccountID", "Sender", "SHABidou", ""),
			expectedAccountID: "TheAccountID",
			expectedSender:    "Sender",
			expectedSHA:       "SHABidou",
			eventType:         "pullrequest:created",
			expectedEventType: triggertype.PullRequest.String(),
		},
		{
			name:              "check source ip allowed",
			payloadEvent:      bbcloudtest.MakePREvent("account", "sender", "sha", ""),
			expectedAccountID: "account",
			expectedSender:    "sender",
			expectedSHA:       "sha",
			eventType:         "pullrequest:created",
			sourceIP:          "1.2.3.1",
			allowedConfig: map[string]map[string]string{
				bitbucketCloudIPrangesList: {
					"body": `{"items": [{"cidr": "1.2.3.10/16"}]}`,
					"code": "200",
				},
			},
			expectedEventType: triggertype.PullRequest.String(),
		},
		{
			name:              "check source ip allowed multiple xff",
			expectedEventType: triggertype.PullRequest.String(),
			payloadEvent:      bbcloudtest.MakePREvent("account", "sender", "sha", ""),
			expectedAccountID: "account",
			expectedSender:    "sender",
			expectedSHA:       "sha",
			eventType:         "pullrequest:updated",
			sourceIP:          "127.0.0.1,30.30.30.30,1.2.3.1",
			allowedConfig: map[string]map[string]string{
				bitbucketCloudIPrangesList: {
					"body": `{"items": [{"cidr": "1.2.3.10/16"}]}`,
					"code": "200",
				},
			},
		},
		{
			name:              "check source ip not allowed",
			expectedEventType: triggertype.PullRequest.String(),
			wantErr:           true,
			payloadEvent:      bbcloudtest.MakePREvent("account", "sender", "sha", ""),
			expectedAccountID: "account",
			expectedSender:    "sender",
			expectedSHA:       "sha",
			eventType:         "pullrequest:created",
			sourceIP:          "1.2.3.1",
			allowedConfig: map[string]map[string]string{
				bitbucketCloudIPrangesList: {
					"body": `{"items": [{"cidr": "10.12.33.22/16"}]}`,
					"code": "200",
				},
			},
		},
		{
			name:                      "additional source ip allowed",
			expectedEventType:         triggertype.PullRequest.String(),
			payloadEvent:              bbcloudtest.MakePREvent("account", "sender", "sha", ""),
			expectedAccountID:         "account",
			expectedSender:            "sender",
			expectedSHA:               "sha",
			eventType:                 "pullrequest:created",
			sourceIP:                  "1.2.3.1",
			additionalAllowedsourceIP: "1.2.3.1",
			allowedConfig: map[string]map[string]string{
				bitbucketCloudIPrangesList: {
					"body": `{"items": [{"cidr": "10.12.33.22/16"}]}`,
					"code": "200",
				},
			},
		},
		{
			name:                      "additional network allowed with spaces",
			expectedEventType:         triggertype.PullRequest.String(),
			payloadEvent:              bbcloudtest.MakePREvent("account", "sender", "sha", ""),
			expectedAccountID:         "account",
			expectedSender:            "sender",
			expectedSHA:               "sha",
			eventType:                 "pullrequest:created",
			sourceIP:                  "1.2.3.3",
			additionalAllowedsourceIP: "1.2.3.4, 1.2.3.0/16",
			allowedConfig: map[string]map[string]string{
				bitbucketCloudIPrangesList: {
					"body": `{"items": [{"cidr": "10.12.33.22/16"}]}`,
					"code": "200",
				},
			},
		},
		{
			name:                      "not allowed with additional ips",
			wantErr:                   true,
			payloadEvent:              bbcloudtest.MakePREvent("account", "sender", "sha", ""),
			eventType:                 "pullrequest:created",
			sourceIP:                  "1.2.3.3",
			additionalAllowedsourceIP: "1.1.3.0/16",
			allowedConfig: map[string]map[string]string{
				bitbucketCloudIPrangesList: {
					"body": `{"items": [{"cidr": "10.12.33.22/16"}]}`,
					"code": "200",
				},
			},
		},
		{
			name:         "check xff hijack",
			wantErr:      true,
			payloadEvent: bbcloudtest.MakePREvent("account", "sender", "sha", ""),
			eventType:    "pullrequest:created",
			sourceIP:     "1.2.3.1,127.0.0.1",
			allowedConfig: map[string]map[string]string{
				bitbucketCloudIPrangesList: {
					"body": `{"items": [{"cidr": "1.2.3.10/16"}]}`,
					"code": "200",
				},
			},
		},
		{
			name:              "retest comment with a pipelinerun",
			expectedEventType: opscomments.RetestSingleCommentEventType.String(),
			payloadEvent:      bbcloudtest.MakePREvent("account", "sender", "sha", "/retest dummy"),
			eventType:         "pullrequest:comment_created",
			expectedAccountID: "account",
			expectedSender:    "sender",
			expectedSHA:       "sha",
			targetPipelinerun: "dummy",
		},
		{
			name:              "ok-to-test comment",
			expectedEventType: opscomments.OkToTestCommentEventType.String(),
			payloadEvent:      bbcloudtest.MakePREvent("account", "sender", "sha", "/ok-to-test"),
			eventType:         "pullrequest:comment_created",
			expectedAccountID: "account",
			expectedSender:    "sender",
			expectedSHA:       "sha",
		},
		{
			name:              "test comment",
			expectedEventType: opscomments.TestAllCommentEventType.String(),
			payloadEvent:      bbcloudtest.MakePREvent("account", "sender", "sha", "/test"),
			eventType:         "pullrequest:comment_created",
			expectedAccountID: "account",
			expectedSender:    "sender",
			expectedSHA:       "sha",
		},
		{
			name:              "cancel comment with a pipelinerun",
			expectedEventType: opscomments.CancelCommentSingleEventType.String(),
			payloadEvent:      bbcloudtest.MakePREvent("account", "sender", "sha", "/cancel dummy"),
			eventType:         "pullrequest:comment_created",
			expectedAccountID: "account",
			expectedSender:    "sender",
			expectedSHA:       "sha",
			cancelPipelinerun: "dummy",
		},
		{
			name:              "cancel all comment",
			expectedEventType: opscomments.CancelCommentAllEventType.String(),
			payloadEvent:      bbcloudtest.MakePREvent("account", "sender", "sha", "/cancel"),
			eventType:         "pullrequest:comment_created",
			expectedAccountID: "account",
			expectedSender:    "sender",
			expectedSHA:       "sha",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			v := &Provider{
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						BitbucketCloudCheckSourceIP:      false,
						BitbucketCloudAdditionalSourceIP: "",
					},
				},
			}

			req := &http.Request{Header: map[string][]string{}}
			req.Header.Set("X-Event-Key", tt.eventType)
			req.Header.Set("X-Forwarded-For", tt.sourceIP)

			run := &params.Run{}

			if tt.sourceIP != "" {
				v = &Provider{
					pacInfo: &info.PacOpts{
						Settings: settings.Settings{
							BitbucketCloudCheckSourceIP:      true,
							BitbucketCloudAdditionalSourceIP: tt.additionalAllowedsourceIP,
						},
					},
				}

				httpTestClient := httptesthelper.MakeHTTPTestClient(tt.allowedConfig)
				run.Clients.HTTP = *httpTestClient
			}

			payload, err := json.Marshal(tt.payloadEvent)
			assert.NilError(t, err)
			got, err := v.ParsePayload(ctx, run, req, string(payload))
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, got != nil)
			assert.Equal(t, tt.expectedAccountID, got.AccountID)
			assert.Equal(t, tt.expectedSender, got.Sender)
			assert.Equal(t, tt.expectedSHA, got.SHA, "%s != %s", tt.expectedSHA, got.SHA)
			assert.Equal(t, tt.expectedEventType, got.EventType, "%s != %s", tt.expectedEventType, got.EventType)
			assert.Assert(t, got.Event != nil)

			if tt.expectedRef != "" {
				assert.Equal(t, tt.expectedRef, got.BaseBranch, tt.expectedRef, got.BaseBranch)
			}
			if tt.targetPipelinerun != "" {
				assert.Equal(t, tt.targetPipelinerun, got.TargetTestPipelineRun, tt.targetPipelinerun, got.TargetTestPipelineRun)
			}
			if tt.cancelPipelinerun != "" {
				assert.Equal(t, tt.cancelPipelinerun, got.TargetCancelPipelineRun, tt.cancelPipelinerun, got.TargetCancelPipelineRun)
			}
		})
	}
}
