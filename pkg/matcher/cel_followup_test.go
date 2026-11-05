package matcher

import (
	"context"
	"net/http"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	"gotest.tools/v3/assert"
)

func TestValidateFollowUp(t *testing.T) {
	tests := []struct {
		name         string
		expr         string
		customParams []string
		wantErr      string
	}{
		{
			name: "valid expression over the runs map",
			expr: "runs.build.status == 'Succeeded' && runs.build.results.digest != ''",
		},
		{
			name: "valid expression over the event variables",
			expr: "target_branch == 'main' && source_url != '' && target_url != ''",
		},
		{
			name:    "pathChanged is rejected",
			expr:    "runs.build.status == 'Succeeded' && 'docs/*'.pathChanged()",
			wantErr: "calls pathChanged(), which a follow-up cannot use",
		},
		{
			name:         "a custom parameter of the repository is declared",
			expr:         "environment == 'prod'",
			customParams: []string{"environment"},
		},
		{
			name:    "an unknown variable is not declared",
			expr:    "environment == 'prod'",
			wantErr: "undeclared reference to 'environment'",
		},
		{
			name:    "body is not declared",
			expr:    "body.pull_request.user.login == 'me'",
			wantErr: "undeclared reference to 'body'",
		},
		{
			name:    "headers is not declared",
			expr:    "headers['x-event'] == 'push'",
			wantErr: "undeclared reference to 'headers'",
		},
		{
			name:    "files is not declared",
			expr:    "files.all.size() > 0",
			wantErr: "undeclared reference to 'files'",
		},
		{
			name:    "syntax error",
			expr:    "runs.build.status ==",
			wantErr: "failed to parse expression",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFollowUp(tt.expr, tt.customParams)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
		})
	}
}

func TestEvaluateFollowUp(t *testing.T) {
	runs := map[string]any{
		"build": map[string]any{
			"name":    "build-abc",
			"status":  "Succeeded",
			"reason":  "Succeeded",
			"results": map[string]any{"digest": "sha256:abc"},
		},
	}
	pullRequest := func() *info.Event {
		event := info.NewEvent()
		event.TriggerTarget = triggertype.PullRequest
		event.EventType = "pull_request"
		event.PullRequestTitle = "add a follow-up"
		event.BaseBranch = "main"
		event.HeadBranch = "follow-up"
		event.BaseURL = "https://forge/org/repo"
		event.HeadURL = "https://forge/fork/repo"
		return event
	}
	push := func() *info.Event {
		event := info.NewEvent()
		event.TriggerTarget = triggertype.Push
		event.EventType = "push"
		event.SHATitle = "merge the follow-up"
		event.BaseBranch = "refs/heads/main"
		event.HeadBranch = "refs/heads/main"
		return event
	}

	tests := []struct {
		name         string
		event        func() *info.Event
		expr         string
		customParams map[string]string
		expected     bool
	}{
		{name: "status check", event: pullRequest, expr: "runs.build.status == 'Succeeded'", expected: true},
		{name: "result check", event: pullRequest, expr: "runs.build.results.digest.startsWith('sha256:')", expected: true},
		{name: "false condition", event: pullRequest, expr: "runs.build.status == 'Failed'"},
		{
			name: "pull request event variables", event: pullRequest,
			expr:     "event == 'pull_request' && target_branch == 'main' && event_title == 'add a follow-up' && source_url == 'https://forge/fork/repo'",
			expected: true,
		},
		{
			name: "a push target branch is matched without refs/heads/ like the matcher", event: push,
			expr: "event == 'push' && target_branch == 'main' && event_title == 'merge the follow-up'", expected: true,
		},
		{
			name: "a push target branch keeps refs/heads/ when the expression spells it", event: push,
			expr: "target_branch == 'refs/heads/main'", expected: true,
		},
		{
			name: "a custom parameter is a variable", event: pullRequest,
			expr: "environment == 'prod'", customParams: map[string]string{"environment": "prod"}, expected: true,
		},
		{
			name: "a custom parameter cannot override the runs map", event: pullRequest,
			expr: "runs.build.status == 'Succeeded'", customParams: map[string]string{"runs": "hijacked"}, expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := tt.event()
			before := *event
			vcx := &testprovider.TestProviderImp{}
			got, err := EvaluateFollowUp(context.Background(), tt.expr, event, vcx, tt.customParams, nil, nil, runs)
			assert.NilError(t, err)
			assert.Equal(t, tt.expected, got)
			assert.DeepEqual(t, before.BaseBranch, event.BaseBranch)
			assert.DeepEqual(t, before.HeadBranch, event.HeadBranch)
		})
	}
}

// TestEvaluateFollowUpMatchesMatcher evaluates the same expressions with the
// matcher and as a follow-up: every variable they share must evaluate alike.
func TestEvaluateFollowUpMatchesMatcher(t *testing.T) {
	newEvent := func(trigger triggertype.Trigger, eventType, base, head string) *info.Event {
		event := info.NewEvent()
		event.TriggerTarget = trigger
		event.EventType = eventType
		event.PullRequestTitle = "the pull request"
		event.SHATitle = "the commit"
		event.BaseBranch = base
		event.HeadBranch = head
		event.BaseURL = "https://forge/org/repo"
		event.HeadURL = "https://forge/fork/repo"
		event.Request = &info.Request{Header: http.Header{}}
		return event
	}
	events := map[string]func() *info.Event{
		"pull request": func() *info.Event {
			return newEvent(triggertype.PullRequest, "pull_request", "main", "feature")
		},
		"push": func() *info.Event { return newEvent(triggertype.Push, "push", "refs/heads/main", "refs/heads/main") },
		"push of a tag": func() *info.Event {
			return newEvent(triggertype.Push, "push", "refs/tags/v1.0", "refs/tags/v1.0")
		},
	}
	exprs := []string{
		"event == 'pull_request'",
		"event == 'push'",
		"event_type == 'push'",
		"event_title == 'the pull request'",
		"event_title == 'the commit'",
		"target_branch == 'main'",
		"source_branch == 'main'",
		"target_branch == 'refs/heads/main'",
		"target_branch.startsWith('refs/tags/')",
		"target_url == 'https://forge/org/repo' && source_url == 'https://forge/fork/repo'",
		"environment == 'prod'",
	}
	customParams := map[string]string{"environment": "prod"}
	vcx := &testprovider.TestProviderImp{}
	for eventName, event := range events {
		for _, expr := range exprs {
			t.Run(eventName+": "+expr, func(t *testing.T) {
				matched, err := celEvaluate(context.Background(), expr, event(), vcx, customParams, nil, nil)
				assert.NilError(t, err)
				got, err := EvaluateFollowUp(context.Background(), expr, event(), vcx, customParams, nil, nil, map[string]any{})
				assert.NilError(t, err)
				assert.Equal(t, matched.Value() == true, got)
			})
		}
	}
}
