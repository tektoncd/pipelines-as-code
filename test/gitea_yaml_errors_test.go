//go:build e2e

package test

import (
	"regexp"
	"testing"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"gotest.tools/v3/assert"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
)

// TestGiteaBadYamlReportingOnPR makes sure that we can catch a bad yaml file
// and report on PR, we only do updates and not creating a new comment all the
// time.
func TestGiteaBadYamlReportingOnPR(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pr-bad-validation.yaml": "testdata/failures/pipeline-validation.yaml"},
		ExpectEvents: true,
	}

	_, f := tgitea.TestPR(t, topts)
	defer f()
	topts.Regexp = regexp.MustCompile(`.*bad-valid | .json: cannot unmarshal array into Go struct field PipelineRunSpec.spec.pipelineSpec of type v1.PipelineSpec.*`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	comments, _, err := topts.GiteaCNX.Client().ListRepoIssueComments(topts.PullRequest.Base.Repository.Owner.UserName, topts.PullRequest.Base.Repository.Name, forgejo.ListIssueCommentOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(comments), 1, "should have only one comment")

	// sending a second time the comment should have been updated
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.DefaultBranch,
		PushForce:     true,
	}
	processed, err := payload.ApplyTemplate("testdata/failures/pipeline-validation.yaml", map[string]string{
		"TargetNamespace": topts.TargetNS,
		"TargetBranch":    topts.DefaultBranch,
		"TargetEvent":     topts.TargetEvent,
		"PipelineName":    "pr-a-second-time",
		"Command":         "sleep 10",
	})
	assert.NilError(t, err)
	entries := map[string]string{".tekton/pr-bad-validation.yaml": processed}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	comments, _, err = topts.GiteaCNX.Client().ListRepoIssueComments(topts.PullRequest.Base.Repository.Owner.UserName, topts.PullRequest.Base.Repository.Name, forgejo.ListIssueCommentOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(comments), 1, "should have only one comment")
}

func TestGiteaYamlReportingNotReportingNotTektonResources(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/randomcrd.yaml": "testdata/randomcrd.yaml"},
		ExpectEvents: true,
	}

	_, f := tgitea.TestPR(t, topts)
	defer f()
	comments, _, err := topts.GiteaCNX.Client().ListRepoIssueComments(topts.PullRequest.Base.Repository.Owner.UserName, topts.PullRequest.Base.Repository.Name, forgejo.ListIssueCommentOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(comments), 0, "should have zero comments")
}

// TestGiteaBadYaml we can't check pr status but this shows up in the
// controller, so let's dig ourself in there....  TargetNS is a random string, so
// it can only success if it matches it.
func TestGiteaBadYamlValidation(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pr-bad-format.yaml": "testdata/failures/bad-yaml.yaml"},
		ExpectEvents: true,
	}

	ctx, f := tgitea.TestPR(t, topts)
	defer f()
	maxLines := int64(1000)
	assert.NilError(t, twait.RegexpMatchingInControllerLog(ctx, topts.ParamsRun, *regexp.MustCompile(
		"cannot read the PipelineRun: pr-bad-format.yaml, error: yaml validation error: line 3: could not find expected ':'",
	),
		10, "controller", &maxLines, nil))
}

// TestGiteaInvalidSpecValues tests invalid field values of a PipelinRun and ensures that these
// validation errors are reported on UI.
func TestGiteaInvalidSpecValues(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:    triggertype.PullRequest.String(),
		YAMLFiles:      map[string]string{".tekton/pr-bad-format.yaml": "testdata/failures/invalid-timeouts-values-pipelinerun.yaml"},
		CheckForStatus: "failure",
		ExpectEvents:   true,
		Regexp:         regexp.MustCompile(options.InvalidYamlErrorPattern),
	}

	_, f := tgitea.TestPR(t, topts)
	defer f()
}

// TestGiteaBadLinkOfTask checks that we fail properly with the error from the
// tekton pipelines controller. We check on the UI interface that we display
// and inside the pac controller.
func TestGiteaBadLinkOfTask(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/failures/bad-runafter-task.yaml",
		},
		CheckForStatus: "failure",
		ExpectEvents:   true,
		Regexp:         regexp.MustCompile(".*There was an error creating the PipelineRun*"),
	}
	ctx, f := tgitea.TestPR(t, topts)
	defer f()
	errre := regexp.MustCompile("There was an error starting the PipelineRun")
	maxLines := int64(1000)
	assert.NilError(t, twait.RegexpMatchingInControllerLog(ctx, topts.ParamsRun, *errre, 10, "controller", &maxLines, nil))
}

// TestGiteaPipelineRunWithSameName checks that we fail properly with the error from the
// tekton pipelines controller. We check on the UI interface that we display
// and inside the pac controller.
func TestGiteaPipelineRunWithSameName(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr1.yaml": "testdata/failures/pipelinerun_same_name_on_pull.yaml",
			".tekton/pr2.yaml": "testdata/failures/pipelinerun_same_name_on_push.yaml",
		},
		CheckForStatus: "failure",
		ExpectEvents:   true,
		Regexp:         regexp.MustCompile(".*found multiple pipelinerun in .tekton with the same name*"),
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	// Wait for any webhook feedback loop to settle, then verify only 1 failure
	// comment was posted (not duplicates from re-triggered no-op comment events).
	time.Sleep(10 * time.Second)

	comments, _, err := topts.GiteaCNX.Client().ListRepoIssueComments(
		topts.PullRequest.Base.Repository.Owner.UserName,
		topts.PullRequest.Base.Repository.Name,
		forgejo.ListIssueCommentOptions{},
	)
	assert.NilError(t, err)

	failureRe := regexp.MustCompile("found multiple pipelinerun in .tekton with the same name")
	var failureCount int
	for _, comment := range comments {
		if failureRe.MatchString(comment.Body) {
			failureCount++
		}
	}
	assert.Equal(t, failureCount, 1,
		"expected 1 failure comment but found %d", failureCount)
}
