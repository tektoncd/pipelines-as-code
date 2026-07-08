//go:build e2e

package test

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitlab "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitlab"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	clientGitlab "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
)

// TestGitlabOpsCommentInThreadReply verifies that a /test command placed
// in a reply within a discussion thread on a Merge Request is honored.
func TestGitlabOpsCommentInThreadReply(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipelinerun.yaml": "testdata/pipelinerun.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	// Create a discussion thread with an initial note
	disc, _, err := topts.GLProvider.Client().Discussions.CreateMergeRequestDiscussion(topts.ProjectID, int64(topts.MRNumber), &clientGitlab.CreateMergeRequestDiscussionOptions{
		Body: clientGitlab.Ptr("random initial note"),
	})
	assert.NilError(t, err)

	// Wait for PipelineRun to succeed after the MR
	waitOpts := twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
	}
	_, err = twait.UntilPipelineRunHasReason(ctx, topts.ParamsRun.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)

	topts.ParamsRun.Clients.Log.Info("Updating discussion with /test comment in a reply thread")
	// Add a reply to the discussion containing /test
	_, _, err = topts.GLProvider.Client().Discussions.AddMergeRequestDiscussionNote(topts.ProjectID, int64(topts.MRNumber), disc.ID, &clientGitlab.AddMergeRequestDiscussionNoteOptions{
		Body: clientGitlab.Ptr("/test"),
	})
	assert.NilError(t, err)
	waitOpts.MinNumberStatus = 2
	_, err = twait.UntilPipelineRunHasReason(ctx, topts.ParamsRun.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)

	topts.ParamsRun.Clients.Log.Info("PipelineRun completed after /test comment")
}
