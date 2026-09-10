package github

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-github/v91/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	"gotest.tools/v3/assert"
)

func TestProviderDetect(t *testing.T) {
	idd := int64(123)
	tests := []struct {
		name          string
		wantErrString string
		isGH          bool
		processReq    bool
		event         any
		eventType     string
		wantReason    string
	}{
		{
			name:       "not a github Event",
			eventType:  "",
			isGH:       false,
			processReq: false,
		},
		{
			name:          "invalid github Event",
			eventType:     "validator",
			wantErrString: "unknown X-Github-Event in message: validator",
			isGH:          false,
			processReq:    false,
		},
		{
			name: "valid check suite Event",
			event: github.CheckSuiteEvent{
				Action: new("rerequested"),
				CheckSuite: &github.CheckSuite{
					ID: &idd,
				},
			},
			eventType:  "check_suite",
			isGH:       true,
			processReq: true,
		},
		{
			name: "valid check run Event",
			event: github.CheckRunEvent{
				Action: new("rerequested"),
				CheckRun: &github.CheckRun{
					ID: &idd,
				},
			},
			eventType:  "check_run",
			isGH:       true,
			processReq: true,
		},
		{
			name: "unsupported Event",
			event: github.CommitCommentEvent{
				Action: new("something"),
			},
			eventType:  "release",
			wantReason: "event \"release\" is not supported",
			isGH:       true,
			processReq: false,
		},
		{
			name: "non standard commit_comment Event",
			event: github.CommitCommentEvent{
				Action: new("something"),
			},
			wantReason: "commit_comment: unsupported action \"something\"",
			eventType:  "commit_comment",
			isGH:       true,
			processReq: false,
		},
		{
			name: "invalid check run Event",
			event: github.CheckRunEvent{
				Action: new("not rerequested"),
			},
			eventType:  "check_run",
			isGH:       true,
			processReq: false,
		},
		{
			name: "invalid issue comment Event",
			event: github.IssueCommentEvent{
				Action: new("deleted"),
			},
			wantReason: "issue_comment: unsupported action \"deleted\"",
			eventType:  "issue_comment",
			isGH:       true,
			processReq: false,
		},
		{
			name: "issue comment Event with no valid comment",
			event: github.IssueCommentEvent{
				Action: new("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						URL: new("url"),
					},
					State: new("open"),
				},
				Installation: &github.Installation{
					ID: &idd,
				},
				Comment: &github.IssueComment{Body: new("abc")},
			},
			eventType:  "issue_comment",
			isGH:       true,
			processReq: true,
		},
		{
			name: "issue comment Event with ok-to-test comment",
			event: github.IssueCommentEvent{
				Action: new("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						URL: new("url"),
					},
					State: new("open"),
				},
				Installation: &github.Installation{
					ID: &idd,
				},
				Comment: &github.IssueComment{Body: new("/ok-to-test")},
			},
			eventType:  "issue_comment",
			isGH:       true,
			processReq: true,
		},
		{
			name: "issue comment Event with ok-to-test and some string",
			event: github.IssueCommentEvent{
				Action: new("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						URL: new("url"),
					},
					State: new("open"),
				},
				Installation: &github.Installation{
					ID: &idd,
				},
				Comment: &github.IssueComment{Body: new("/ok-to-test \n let me in :)")},
			},
			eventType:  "issue_comment",
			isGH:       true,
			processReq: true,
		},
		{
			name: "issue comment Event with retest",
			event: github.IssueCommentEvent{
				Action: new("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						URL: new("url"),
					},
					State: new("open"),
				},
				Installation: &github.Installation{
					ID: &idd,
				},
				Comment: &github.IssueComment{Body: new("/retest")},
			},
			eventType:  "issue_comment",
			isGH:       true,
			processReq: true,
		},
		{
			name: "issue comment Event with retest with some string",
			event: github.IssueCommentEvent{
				Action: new("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						URL: new("url"),
					},
					State: new("open"),
				},
				Installation: &github.Installation{
					ID: &idd,
				},
				Comment: &github.IssueComment{Body: new("/retest \n will you retest?")},
			},
			eventType:  "issue_comment",
			isGH:       true,
			processReq: true,
		},
		{
			name: "push event",
			event: github.PushEvent{
				Pusher: &github.CommitAuthor{Name: new("user")},
			},
			eventType:  "push",
			isGH:       true,
			processReq: true,
		},
		{
			name: "pull request event",
			event: github.PullRequestEvent{
				Action: new("opened"),
			},
			eventType:  "pull_request",
			isGH:       true,
			processReq: true,
		},
		{
			name: "pull request event converted from draft to active",
			event: github.PullRequestEvent{
				Action: new("ready_for_review"),
			},
			eventType:  "pull_request",
			isGH:       true,
			processReq: true,
		},
		{
			name: "pull request event not supported action",
			event: github.PullRequestEvent{
				Action: new("deleted"),
			},
			eventType:  "pull_request",
			isGH:       true,
			processReq: false,
		},
		{
			name: "issue comment event with cancel comment",
			event: github.IssueCommentEvent{
				Action: new("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						URL: new("url"),
					},
					State: new("open"),
				},
				Installation: &github.Installation{
					ID: &idd,
				},
				Comment: &github.IssueComment{Body: new("/cancel")},
			},
			eventType:  "issue_comment",
			isGH:       true,
			processReq: true,
		},
		{
			name: "issue comment Event with cancel comment ",
			event: github.IssueCommentEvent{
				Action: new("created"),
				Issue: &github.Issue{
					PullRequestLinks: &github.PullRequestLinks{
						URL: new("url"),
					},
					State: new("open"),
				},
				Installation: &github.Installation{
					ID: &idd,
				},
				Comment: &github.IssueComment{Body: new("/cancel dummy")},
			},
			eventType:  "issue_comment",
			isGH:       true,
			processReq: true,
		},
		{
			name: "commit comment event with cancel comment",
			event: github.CommitCommentEvent{
				Action: new("created"),
				Installation: &github.Installation{
					ID: &idd,
				},
				Comment: &github.RepositoryComment{Body: new("/cancel")},
			},
			eventType:  "commit_comment",
			isGH:       true,
			processReq: true,
		},
		{
			name: "commit comment Event with retest",
			event: github.CommitCommentEvent{
				Action: new("created"),
				Installation: &github.Installation{
					ID: &idd,
				},
				Comment: &github.RepositoryComment{Body: new("/retest")},
			},
			eventType:  "commit_comment",
			isGH:       true,
			processReq: true,
		},
		{
			name: "commit comment Event with test",
			event: github.CommitCommentEvent{
				Action: new("created"),
				Installation: &github.Installation{
					ID: &idd,
				},
				Comment: &github.RepositoryComment{Body: new("/test")},
			},
			eventType:  "commit_comment",
			isGH:       true,
			processReq: true,
		},
		{
			name: "commit comment Event with /ok-to-test being ignore as GitOps command on pushed commits",
			event: github.CommitCommentEvent{
				Action: new("created"),
				Installation: &github.Installation{
					ID: &idd,
				},
				Comment: &github.RepositoryComment{Body: new("/ok-to-test")},
			},
			eventType:  "commit_comment",
			isGH:       true,
			processReq: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gprovider := Provider{}
			logger, logCatcher := logger.GetLogger()
			jeez, err := json.Marshal(tt.event)
			if err != nil {
				assert.NilError(t, err)
			}

			header := http.Header{}
			header.Set("X-GitHub-Event", tt.eventType)
			header.Set("X-GitHub-Delivery", "1234567890")

			req := &http.Request{Header: header}
			isGh, processReq, logger, reason, err := gprovider.Detect(req, string(jeez), logger)
			if tt.wantErrString != "" {
				assert.ErrorContains(t, err, tt.wantErrString)
				return
			}
			if tt.wantReason != "" {
				assert.Assert(t, strings.Contains(reason, tt.wantReason), reason, tt.wantReason)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.isGH, isGh)
			assert.Equal(t, tt.processReq, processReq)

			if !tt.isGH {
				return
			}

			logger.Info("generate a log message to check if event-id is added to the logger")

			found := false
			logs := logCatcher.All()
			for _, entry := range logs {
				for _, field := range entry.Context {
					if field.Key == "event-id" {
						assert.Equal(t, field.String, "1234567890")
						found = true
					}
				}
			}
			assert.Assert(t, found, "event-id not found in the logs")
		})
	}
}
