//go:build e2e

package test

import (
	"regexp"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"gotest.tools/v3/assert"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	tstatus "github.com/openshift-pipelines/pipelines-as-code/test/pkg/status"
)

// TestGiteaStatusStepBreakdown checks that the status comment breaks down every
// step of a task, with a link to the logs of each of them.
func TestGiteaStatusStepBreakdown(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun-multiple-steps-success.yaml",
		},
		CheckForStatus: "success",
		ExpectEvents:   false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	topts.Regexp = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(tstatus.BreakdownStart) + `.*` + regexp.QuoteMeta(tstatus.StepLink("echo")) + `.*</details>`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	tstatus.AssertStepBreakdown(t, lastGiteaComment(t, topts),
		[]string{"alpha", "Bravo has a display name", "charlie", "delta", "echo"},
		nil)
}

// TestGiteaStatusStepBreakdownOnFailure checks that the step which has failed is
// shown in the breakdown while the steps skipped after it are not.
func TestGiteaStatusStepBreakdownOnFailure(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun-multiple-steps-failure.yaml",
		},
		CheckForStatus: "failure",
		ExpectEvents:   false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	topts.Regexp = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(tstatus.BreakdownStart) + `.*` + regexp.QuoteMeta(tstatus.StepLink("charlie")) + `.*</details>`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	tstatus.AssertStepBreakdown(t, lastGiteaComment(t, topts),
		[]string{"alpha", "Bravo has a display name", "charlie"},
		[]string{"delta", "echo"})
}

func lastGiteaComment(t *testing.T, topts *tgitea.TestOpts) string {
	t.Helper()
	comments, _, err := topts.GiteaCNX.Client().ListIssueComments(topts.PullRequest.Base.Repository.Owner.UserName,
		topts.PullRequest.Base.Repository.Name, topts.PullRequest.Index, forgejo.ListIssueCommentOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(comments) > 0, "no comment has been posted on the pull request")
	return comments[len(comments)-1].Body
}
