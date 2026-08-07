//go:build e2e

package test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tbbdc "github.com/openshift-pipelines/pipelines-as-code/test/pkg/bitbucketdatacenter"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"

	"github.com/jenkins-x/go-scm/scm"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
)

func TestBitbucketDataCenterNonGitopsCommentTriggersPipelineRun(t *testing.T) {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	ctx := context.Background()
	bitbucketWSOwner := os.Getenv("TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY")

	ctx, runcnx, opts, client, err := tbbdc.Setup(ctx)
	assert.NilError(t, err)

	repo := tbbdc.CreateCRD(ctx, t, client, runcnx, bitbucketWSOwner, targetNS)
	runcnx.Clients.Log.Infof("Repository %s has been created", repo.Name)
	defer tbbdc.TearDownNs(ctx, t, runcnx, targetNS)

	files := map[string]string{
		".tekton/on-comment.yaml": "testdata/pipelinerun-on-comment-annotation.yaml",
	}
	files, err = payload.GetEntries(files, targetNS, options.MainBranch, triggertype.PullRequest.String(), map[string]string{})
	assert.NilError(t, err)

	pr := tbbdc.CreatePR(ctx, t, client, runcnx, opts, repo, files, bitbucketWSOwner, targetNS)
	runcnx.Clients.Log.Infof("Pull Request with title '%s' is created", pr.Title)
	defer tbbdc.TearDown(ctx, t, runcnx, client, pr, bitbucketWSOwner, targetNS)

	triggerComment := "/hello-world"
	runcnx.Clients.Log.Infof("Posting comment %q on PR", triggerComment)
	_, _, err = client.PullRequests.CreateComment(ctx, bitbucketWSOwner, pr.Number, &scm.CommentInput{Body: triggerComment})
	assert.NilError(t, err)

	waitOpts := wait.Opts{
		Namespace:       targetNS,
		MinNumberStatus: 1,
		PollTimeout:     wait.DefaultTimeout,
	}
	prs, err := wait.UntilPipelineRunsFinished(ctx, runcnx.Clients, waitOpts)
	assert.NilError(t, err)
	assert.Equal(t, prs[0].Annotations[keys.EventType], "on-comment",
		"PipelineRun should have on-comment event type when triggered by a non-gitops comment")

	err = wait.RegexpMatchingInPodLog(context.Background(), runcnx, targetNS,
		fmt.Sprintf("tekton.dev/pipelineRun=%s", prs[0].Name),
		"step-task", *regexp.MustCompile(triggerComment), "", 2, nil)
	assert.NilError(t, err, "pod logs should contain the trigger comment text %q", triggerComment)
}
