package gitea

import (
	"net/http"
	"strings"
	"testing"

	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
)

func TestProviderDetect(t *testing.T) {
	type args struct {
		req     *http.Request
		payload string
	}
	tests := []struct {
		name          string
		args          args
		wantReason    string
		wantErrSubstr string
		isGitea       bool
		processEvent  bool
	}{
		{
			name:       "bad/test not a gitea request",
			wantReason: "not a gitea event",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Foobar-Event": []string{"push"},
					},
				},
			},
			isGitea: false,
		},
		{
			name: "bad/invalid json payload",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"push"},
					},
				},
				payload: "foobar",
			},
			wantErrSubstr: "invalid character",
		},
		{
			name: "bad/invalid push payload type",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"push"},
					},
				},
				payload: `{"foo": "bar"}`,
			},
			wantReason: "invalid payload: no pusher in event",
			isGitea:    true,
		},
		{
			name: "bad/invalid pull request payload type",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"pull_request"},
					},
				},
				payload: `{"action": "foo"}`,
			},
			wantReason: `pull_request: unsupported action "foo"`,
			isGitea:    true,
		},
		{
			name: "bad/invalid issue comment payload type",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"issue_comment"},
					},
				},
				payload: `{"action": "foo"}`,
			},
			wantReason: `skip: not a PAC gitops comment`,
			isGitea:    true,
		},
		{
			name: "good/pull request",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"pull_request"},
					},
				},
				payload: `{"action": "opened"}`,
			},
			isGitea:      true,
			processEvent: true,
		},
		{
			name: "good/push",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"push"},
					},
				},
				payload: `{"pusher": {"id": 1}}`,
			},
			isGitea:      true,
			processEvent: true,
		},
		{
			name: "good/retest comment",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"issue_comment"},
					},
				},
				payload: `{"action": "created", "comment":{"body": "/retest"}, "issue":{"pull_request": {"merged": false}, "state": "open"}}`,
			},
			isGitea:      true,
			processEvent: true,
		},
		{
			name: "good/ok-to-test comment",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"issue_comment"},
					},
				},
				payload: `{"action": "created", "comment":{"body": "/ok-to-test"}, "issue":{"pull_request": {"merged": false}, "state": "open"}}`,
			},
			isGitea:      true,
			processEvent: true,
		},
		{
			name: "good/random issue comment",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"issue_comment"},
					},
				},
				payload: `{"action": "created", "comment":{"body": "YOYO/ok-to-test"}, "issue":{"pull_request": {"merged": false}, "state": "open"}}`,
			},
			isGitea:      true,
			processEvent: true,
		},
		{
			name: "bad/event not supported",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"hellomoto"},
					},
				},
			},
			wantErrSubstr: "unexpected event type",
		},
		{
			name: "good/cancel comment",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"issue_comment"},
					},
				},
				payload: `{"action": "created", "comment":{"body": "/cancel"}, "issue":{"pull_request": {"merged": false}, "state": "open"}}`,
			},
			isGitea:      true,
			processEvent: true,
		},
		{
			name: "good/cancel comment single pr",
			args: args{
				req: &http.Request{
					Header: http.Header{
						"X-Gitea-Event-Type": []string{"issue_comment"},
					},
				},
				payload: `{"action": "created", "comment":{"body": "/cancel pr"}, "issue":{"pull_request": {"merged": false}, "state": "open"}}`,
			},
			isGitea:      true,
			processEvent: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, logCatcher := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			v := &Provider{}
			if tt.args.req != nil {
				if tt.args.req.Header == nil {
					tt.args.req.Header = http.Header{}
				}
				tt.args.req.Header.Set("X-Gitea-Delivery", "1234567890")
			}
			isGitea, processEvent, logger, gotReason, err := v.Detect(tt.args.req, tt.args.payload, logger)
			assert.Assert(t, gotReason == tt.wantReason, gotReason, tt.wantReason)
			if tt.wantErrSubstr != "" {
				assert.Assert(t, err != nil)
				assert.Assert(t, strings.Contains(err.Error(), tt.wantErrSubstr), err.Error(), "doesn't have", tt.wantErrSubstr)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, isGitea == tt.isGitea)
			assert.Assert(t, processEvent == tt.processEvent)

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
