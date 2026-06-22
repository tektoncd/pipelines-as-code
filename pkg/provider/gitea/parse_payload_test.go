package gitea

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea/forgejostructs"
	"gotest.tools/v3/assert"
)

func TestParsePayloadIssueCommentPullRequestData(t *testing.T) {
	const payloadWithPR = `{
		"action": "created",
		"issue": {
			"url": "https://gitea.example/api/v1/repos/test-org/test-repo/issues/42",
			"number": 42,
			"pull_request": {"html_url": "https://gitea.example/test-org/test-repo/pulls/42"}
		},
		"pull_request": {
			"number": 42,
			"title": "Test PR",
			"head": {
				"ref": "feature",
				"sha": "abc123",
				"repo": {"html_url": "https://gitea.example/testuser/test-repo-fork"}
			},
			"base": {
				"ref": "main",
				"repo": {"html_url": "https://gitea.example/test-org/test-repo"}
			},
			"html_url": "https://gitea.example/test-org/test-repo/pulls/42"
		},
		"comment": {"body": "/retest"},
		"repository": {
			"name": "test-repo",
			"owner": {"login": "test-org"},
			"html_url": "https://gitea.example/test-org/test-repo",
			"default_branch": "main"
		},
		"sender": {"login": "testuser"}
	}`

	tests := []struct {
		name          string
		eventType     string
		payload       string
		wantErr       string
		wantHeadURL   string
		wantBaseURL   string
		wantPRNumber  int
		wantSHA       string
		wantEventType string
	}{
		{
			name:          "issue_comment populates source and target urls",
			eventType:     "issue_comment",
			payload:       payloadWithPR,
			wantHeadURL:   "https://gitea.example/testuser/test-repo-fork",
			wantBaseURL:   "https://gitea.example/test-org/test-repo",
			wantPRNumber:  42,
			wantSHA:       "abc123",
			wantEventType: opscomments.RetestAllCommentEventType.String(),
		},
		{
			name:          "pull_request_comment populates source and target urls",
			eventType:     "pull_request_comment",
			payload:       payloadWithPR,
			wantHeadURL:   "https://gitea.example/testuser/test-repo-fork",
			wantBaseURL:   "https://gitea.example/test-org/test-repo",
			wantPRNumber:  42,
			wantSHA:       "abc123",
			wantEventType: opscomments.RetestAllCommentEventType.String(),
		},
		{
			name:      "non pull request issue comment still fails",
			eventType: "issue_comment",
			payload: `{
				"action": "created",
				"issue": {"url": "https://gitea.example/api/v1/repos/test-org/test-repo/issues/7"},
				"comment": {"body": "/retest"},
				"repository": {"name": "test-repo", "owner": {"login": "test-org"}},
				"sender": {"login": "testuser"}
			}`,
			wantErr: "issue comment is not coming from a pull_request",
		},
		{
			name:      "missing head repo does not panic and leaves head url empty",
			eventType: "issue_comment",
			payload: `{
				"action": "created",
				"issue": {
					"url": "https://gitea.example/api/v1/repos/test-org/test-repo/issues/44",
					"pull_request": {"html_url": "https://gitea.example/test-org/test-repo/pulls/44"}
				},
				"pull_request": {
					"number": 44,
					"head": {"ref": "feature", "sha": "abc999"},
					"base": {"ref": "main", "repo": {"html_url": "https://gitea.example/test-org/test-repo"}},
					"html_url": "https://gitea.example/test-org/test-repo/pulls/44"
				},
				"comment": {"body": "/retest"},
				"repository": {"name": "test-repo", "owner": {"login": "test-org"}}
			}`,
			wantHeadURL:   "",
			wantBaseURL:   "https://gitea.example/test-org/test-repo",
			wantPRNumber:  44,
			wantSHA:       "abc999",
			wantEventType: opscomments.RetestAllCommentEventType.String(),
		},
		{
			// Empty issue URL: the PR number falls back to PullRequest.Index
			// (the else if gitEvent.PullRequest != nil branch).
			name:      "empty issue URL falls back to PullRequest Index",
			eventType: "issue_comment",
			payload: `{
				"action": "created",
				"issue": {
					"pull_request": {"html_url": "https://gitea.example/test-org/test-repo/pulls/2787"}
				},
				"pull_request": {
					"number": 2787,
					"head": {"ref": "feature", "sha": "def456", "repo": {"html_url": "https://gitea.example/testuser/test-repo-fork"}},
					"base": {"ref": "main", "repo": {"html_url": "https://gitea.example/test-org/test-repo"}},
					"html_url": "https://gitea.example/test-org/test-repo/pulls/2787"
				},
				"comment": {"body": "/retest"},
				"repository": {"name": "test-repo", "owner": {"login": "test-org"}, "html_url": "https://gitea.example/test-org/test-repo", "default_branch": "main"},
				"sender": {"login": "testuser"}
			}`,
			wantHeadURL:   "https://gitea.example/testuser/test-repo-fork",
			wantBaseURL:   "https://gitea.example/test-org/test-repo",
			wantPRNumber:  2787,
			wantSHA:       "def456",
			wantEventType: opscomments.RetestAllCommentEventType.String(),
		},
		{
			// No comment in the payload: SetEventTypeAndTargetPR is skipped, so
			// the event keeps the TriggerTarget set for the pull request and the
			// nil comment is not dereferenced.
			name:      "nil comment skips event type override",
			eventType: "issue_comment",
			payload: `{
				"action": "created",
				"issue": {
					"url": "https://gitea.example/api/v1/repos/test-org/test-repo/issues/42",
					"number": 42,
					"pull_request": {"html_url": "https://gitea.example/test-org/test-repo/pulls/42"}
				},
				"pull_request": {
					"number": 42,
					"head": {"ref": "feature", "sha": "abc123", "repo": {"html_url": "https://gitea.example/testuser/test-repo-fork"}},
					"base": {"ref": "main", "repo": {"html_url": "https://gitea.example/test-org/test-repo"}},
					"html_url": "https://gitea.example/test-org/test-repo/pulls/42"
				},
				"repository": {"name": "test-repo", "owner": {"login": "test-org"}, "html_url": "https://gitea.example/test-org/test-repo", "default_branch": "main"},
				"sender": {"login": "testuser"}
			}`,
			wantHeadURL:  "https://gitea.example/testuser/test-repo-fork",
			wantBaseURL:  "https://gitea.example/test-org/test-repo",
			wantPRNumber: 42,
			wantSHA:      "abc123",
			// EventType stays empty: the override is skipped for a nil comment.
			wantEventType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGiteaPayload(tt.eventType, tt.payload)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Assert(t, got != nil)
			assert.Equal(t, got.TriggerTarget, triggertype.PullRequest)
			assert.Equal(t, got.EventType, tt.wantEventType)
			assert.Equal(t, got.PullRequestNumber, tt.wantPRNumber)
			assert.Equal(t, got.HeadURL, tt.wantHeadURL)
			assert.Equal(t, got.BaseURL, tt.wantBaseURL)
			assert.Equal(t, got.SHA, tt.wantSHA)
		})
	}
}

// parseGiteaPayload runs ParsePayload for the given Gitea event type and body.
func parseGiteaPayload(eventType, payload string) (*info.Event, error) {
	req := &http.Request{Header: http.Header{"X-Gitea-Event-Type": []string{eventType}}}
	return (&Provider{}).ParsePayload(context.Background(), nil, req, payload)
}

// prPayload builds a pull_request webhook payload with the given action and an
// optional labels JSON fragment (e.g. `{"name": "bug"}`).
func prPayload(action, labelsJSON string) string {
	return fmt.Sprintf(`{
		"action": %q,
		"number": 5,
		"pull_request": {
			"title": "Add feature",
			"head": {
				"ref": "feature-branch",
				"sha": "abc123",
				"repo": {"html_url": "https://gitea.example/testuser/test-repo-fork"}
			},
			"base": {
				"ref": "main",
				"repo": {"html_url": "https://gitea.example/test-org/test-repo"}
			},
			"html_url": "https://gitea.example/test-org/test-repo/pulls/5",
			"labels": [%s]
		},
		"repository": {
			"name": "test-repo",
			"owner": {"login": "test-org"},
			"html_url": "https://gitea.example/test-org/test-repo",
			"default_branch": "main"
		},
		"sender": {"login": "testuser"}
	}`, action, labelsJSON)
}

func TestParsePayloadPullRequest(t *testing.T) {
	tests := []struct {
		name              string
		eventType         string
		payload           string
		wantEventType     string
		wantTriggerTarget triggertype.Trigger
		wantLabels        []string
	}{
		{
			name:              "opened pull request sets pull_request event and target",
			eventType:         "pull_request",
			payload:           prPayload("opened", ""),
			wantEventType:     triggertype.PullRequest.String(),
			wantTriggerTarget: triggertype.PullRequest,
		},
		{
			name:              "synchronized pull request sets pull_request event and target",
			eventType:         "pull_request",
			payload:           prPayload("synchronized", ""),
			wantEventType:     triggertype.PullRequest.String(),
			wantTriggerTarget: triggertype.PullRequest,
		},
		{
			name:              "label updated sets pull_request_labeled event type",
			eventType:         "pull_request_label",
			payload:           prPayload("label_updated", `{"name": "bug"}, {"name": "enhancement"}`),
			wantEventType:     string(triggertype.PullRequestLabeled),
			wantTriggerTarget: triggertype.PullRequest,
			wantLabels:        []string{"bug", "enhancement"},
		},
		{
			name:              "closed pull request sets pull_request_closed target",
			eventType:         "pull_request",
			payload:           prPayload("closed", ""),
			wantEventType:     triggertype.PullRequest.String(),
			wantTriggerTarget: triggertype.PullRequestClosed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGiteaPayload(tt.eventType, tt.payload)
			assert.NilError(t, err)
			assert.Assert(t, got != nil)

			// Fields common to every pull_request payload above.
			assert.Equal(t, got.Sender, "testuser")
			assert.Equal(t, got.Organization, "test-org")
			assert.Equal(t, got.Repository, "test-repo")
			assert.Equal(t, got.DefaultBranch, "main")
			assert.Equal(t, got.URL, "https://gitea.example/test-org/test-repo")
			assert.Equal(t, got.PullRequestTitle, "Add feature")
			assert.Equal(t, got.SHA, "abc123")
			assert.Equal(t, got.HeadBranch, "feature-branch")
			assert.Equal(t, got.HeadURL, "https://gitea.example/testuser/test-repo-fork")
			assert.Equal(t, got.BaseBranch, "main")
			assert.Equal(t, got.BaseURL, "https://gitea.example/test-org/test-repo")
			assert.Equal(t, got.SHAURL, "https://gitea.example/test-org/test-repo/pulls/5/commit/abc123")
			assert.Equal(t, got.PullRequestNumber, 5)

			// Fields driven by the action.
			assert.Equal(t, got.EventType, tt.wantEventType)
			assert.Equal(t, got.TriggerTarget, tt.wantTriggerTarget)
			if len(tt.wantLabels) == 0 {
				assert.Equal(t, len(got.PullRequestLabel), 0)
			} else {
				assert.DeepEqual(t, got.PullRequestLabel, tt.wantLabels)
			}
		})
	}
}

func TestParsePayloadPush(t *testing.T) {
	tests := []struct {
		name         string
		payload      string
		wantSHA      string
		wantSHAURL   string
		wantSHATitle string
	}{
		{
			name: "head commit provides sha url and title",
			payload: `{
				"ref": "refs/heads/main",
				"before": "before000",
				"head_commit": {
					"id": "push123",
					"url": "https://gitea.example/test-org/test-repo/commit/push123",
					"message": "Update code"
				},
				"repository": {
					"name": "test-repo",
					"owner": {"login": "test-org"},
					"html_url": "https://gitea.example/test-org/test-repo",
					"default_branch": "main"
				},
				"sender": {"login": "pusher-user"}
			}`,
			wantSHA:      "push123",
			wantSHAURL:   "https://gitea.example/test-org/test-repo/commit/push123",
			wantSHATitle: "Update code",
		},
		{
			name: "missing head commit falls back to before sha",
			payload: `{
				"ref": "refs/heads/main",
				"before": "before999",
				"repository": {
					"name": "test-repo",
					"owner": {"login": "test-org"},
					"html_url": "https://gitea.example/test-org/test-repo",
					"default_branch": "main"
				},
				"sender": {"login": "pusher-user"}
			}`,
			wantSHA: "before999",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGiteaPayload("push", tt.payload)
			assert.NilError(t, err)
			assert.Assert(t, got != nil)

			assert.Equal(t, got.SHA, tt.wantSHA)
			assert.Equal(t, got.SHAURL, tt.wantSHAURL)
			assert.Equal(t, got.SHATitle, tt.wantSHATitle)
			assert.Equal(t, got.Organization, "test-org")
			assert.Equal(t, got.Repository, "test-repo")
			assert.Equal(t, got.DefaultBranch, "main")
			assert.Equal(t, got.URL, "https://gitea.example/test-org/test-repo")
			assert.Equal(t, got.BaseURL, "https://gitea.example/test-org/test-repo")
			// In push events HeadURL mirrors BaseURL.
			assert.Equal(t, got.HeadURL, got.BaseURL)
			assert.Equal(t, got.Sender, "pusher-user")
			assert.Equal(t, got.BaseBranch, "refs/heads/main")
			// In push events HeadBranch mirrors BaseBranch.
			assert.Equal(t, got.HeadBranch, got.BaseBranch)
			assert.Equal(t, got.EventType, "push")
			assert.Equal(t, got.TriggerTarget, triggertype.Push)
		})
	}
}

func TestParsePayloadErrors(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		payload   string
		wantErr   string
	}{
		{
			name:      "missing event type header",
			eventType: "",
			payload:   `{}`,
			wantErr:   "failed to find event type in request header",
		},
		{
			name:      "unknown event type rejected by webhook parser",
			eventType: "totally_bogus",
			payload:   `{}`,
			wantErr:   "unexpected event type: totally_bogus",
		},
		{
			name:      "supported webhook event but unsupported by ParsePayload",
			eventType: "release",
			payload:   `{"action": "published"}`,
			wantErr:   "event release is not supported",
		},
		{
			name:      "invalid json payload",
			eventType: "push",
			payload:   `{invalid`,
			wantErr:   "invalid character",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGiteaPayload(tt.eventType, tt.payload)
			assert.ErrorContains(t, err, tt.wantErr)
			assert.Assert(t, got == nil)
		})
	}
}

func TestPopulateEventFromGiteaPullRequest(t *testing.T) {
	tests := []struct {
		name           string
		pr             *forgejostructs.PullRequest
		wantTitle      string
		wantSHA        string
		wantHeadBranch string
		wantHeadURL    string
		wantBaseBranch string
		wantBaseURL    string
		wantSHAURL     string
	}{
		{
			name: "nil pull request leaves event untouched",
		},
		{
			name: "full pull request populates every field",
			pr: &forgejostructs.PullRequest{
				Title:   "My PR",
				HTMLURL: "https://gitea.example/upstream/pulls/1",
				Head: &forgejostructs.PRBranchInfo{
					Ref:        "feature",
					Sha:        "sha123",
					Repository: &forgejostructs.Repository{HTMLURL: "https://gitea.example/fork"},
				},
				Base: &forgejostructs.PRBranchInfo{
					Ref:        "main",
					Repository: &forgejostructs.Repository{HTMLURL: "https://gitea.example/upstream"},
				},
			},
			wantTitle:      "My PR",
			wantSHA:        "sha123",
			wantHeadBranch: "feature",
			wantHeadURL:    "https://gitea.example/fork",
			wantBaseBranch: "main",
			wantBaseURL:    "https://gitea.example/upstream",
			wantSHAURL:     "https://gitea.example/upstream/pulls/1/commit/sha123",
		},
		{
			name: "head and base without repository leave urls empty",
			pr: &forgejostructs.PullRequest{
				HTMLURL: "https://gitea.example/upstream/pulls/2",
				Head:    &forgejostructs.PRBranchInfo{Ref: "feature", Sha: "sha123"},
				Base:    &forgejostructs.PRBranchInfo{Ref: "main"},
			},
			wantSHA:        "sha123",
			wantHeadBranch: "feature",
			wantBaseBranch: "main",
			wantSHAURL:     "https://gitea.example/upstream/pulls/2/commit/sha123",
		},
		{
			name: "no sha skips the sha url",
			pr: &forgejostructs.PullRequest{
				Title:   "No head",
				HTMLURL: "https://gitea.example/upstream/pulls/3",
				Base:    &forgejostructs.PRBranchInfo{Ref: "main"},
			},
			wantTitle:      "No head",
			wantBaseBranch: "main",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := info.NewEvent()
			populateEventFromGiteaPullRequest(event, tt.pr)
			assert.Equal(t, event.PullRequestTitle, tt.wantTitle)
			assert.Equal(t, event.SHA, tt.wantSHA)
			assert.Equal(t, event.HeadBranch, tt.wantHeadBranch)
			assert.Equal(t, event.HeadURL, tt.wantHeadURL)
			assert.Equal(t, event.BaseBranch, tt.wantBaseBranch)
			assert.Equal(t, event.BaseURL, tt.wantBaseURL)
			assert.Equal(t, event.SHAURL, tt.wantSHAURL)
		})
	}
}
