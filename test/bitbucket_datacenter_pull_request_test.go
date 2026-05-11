//go:build e2e

package test

import (
	"context"
	"fmt"
	"os"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBitbucketDataCenterPullRequest(t *testing.T) {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	ctx := context.Background()
	bitbucketWSOwner := os.Getenv("TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY")

	ctx, runcnx, opts, client, err := tbbdc.Setup(ctx)
	assert.NilError(t, err)

	repo := tbbdc.CreateCRD(ctx, t, client, runcnx, bitbucketWSOwner, targetNS)
	runcnx.Clients.Log.Infof("Repository %s has been created", repo.Name)
	defer tbbdc.TearDownNs(ctx, t, runcnx, targetNS)

	numberOfFiles := 5
	files := map[string]string{}
	for i := range numberOfFiles {
		files[fmt.Sprintf(".tekton/pipelinerun-%d.yaml", i)] = "testdata/pipelinerun.yaml"
	}

	files, err = payload.GetEntries(files, targetNS, options.MainBranch, triggertype.PullRequest.String(), map[string]string{})
	assert.NilError(t, err)

	pr := tbbdc.CreatePR(ctx, t, client, runcnx, opts, repo, files, bitbucketWSOwner, targetNS)
	runcnx.Clients.Log.Infof("Pull Request with title '%s' is created", pr.Title)
	defer tbbdc.TearDown(ctx, t, runcnx, client, pr, bitbucketWSOwner, targetNS)

	successOpts := wait.SuccessOpt{
		TargetNS:        targetNS,
		OnEvent:         triggertype.PullRequest.String(),
		NumberofPRMatch: numberOfFiles,
		MinNumberStatus: numberOfFiles,
	}
	wait.Succeeded(ctx, t, runcnx, opts, successOpts)
}

func TestBitbucketDataCenterCELPathChangeInPullRequest(t *testing.T) {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	ctx := context.Background()
	bitbucketWSOwner := os.Getenv("TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY")

	ctx, runcnx, opts, client, err := tbbdc.Setup(ctx)
	assert.NilError(t, err)

	repo := tbbdc.CreateCRD(ctx, t, client, runcnx, bitbucketWSOwner, targetNS)
	runcnx.Clients.Log.Infof("Repository %s has been created", repo.Name)
	defer tbbdc.TearDownNs(ctx, t, runcnx, targetNS)

	files := map[string]string{
		".tekton/pipelinerun.yaml": "testdata/pipelinerun-cel-path-changed.yaml",
	}

	files, err = payload.GetEntries(files, targetNS, options.MainBranch, triggertype.PullRequest.String(), map[string]string{})
	assert.NilError(t, err)

	pr := tbbdc.CreatePR(ctx, t, client, runcnx, opts, repo, files, bitbucketWSOwner, targetNS)
	runcnx.Clients.Log.Infof("Pull Request with title '%s' is created", pr.Title)
	defer tbbdc.TearDown(ctx, t, runcnx, client, pr, bitbucketWSOwner, targetNS)

	successOpts := wait.SuccessOpt{
		TargetNS:        targetNS,
		OnEvent:         triggertype.PullRequest.String(),
		NumberofPRMatch: 1,
		MinNumberStatus: 1,
	}
	wait.Succeeded(ctx, t, runcnx, opts, successOpts)
}

func TestBitbucketDataCenterOnPathChangeAnnotationOnPRMerge(t *testing.T) {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	// this would be a temporary base branch for the pull request we're going to raise
	// we need this because we're going to merge the pull request so that after test
	// we can delete the temporary base branch and our main branch should not be affected
	// by this merge because we run the E2E frequently.
	tempBaseBranch := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	ctx := context.Background()
	bitbucketWSOwner := os.Getenv("TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY")

	ctx, runcnx, opts, client, err := tbbdc.Setup(ctx)
	assert.NilError(t, err)

	repo := tbbdc.CreateCRD(ctx, t, client, runcnx, bitbucketWSOwner, targetNS)
	runcnx.Clients.Log.Infof("Repository %s has been created", repo.Name)
	defer tbbdc.TearDownNs(ctx, t, runcnx, targetNS)

	branch, resp, err := client.Git.CreateRef(ctx, bitbucketWSOwner, tempBaseBranch, repo.Branch)
	assert.NilError(t, err, "error creating branch: http status code: %d : %v", resp.Status, err)
	runcnx.Clients.Log.Infof("Base branch %s has been created", branch.Name)

	opts.BaseBranch = branch.Name

	if os.Getenv("TEST_NOCLEANUP") != "true" {
		defer func() {
			_, err := client.Git.DeleteRef(ctx, bitbucketWSOwner, tempBaseBranch)
			assert.NilError(t, err, "error deleting branch: http status code: %d : %v", resp.Status, err)
		}()
	}

	files := map[string]string{
		".tekton/pr.yaml":       "testdata/pipelinerun-on-path-change.yaml",
		"doc/foo/bar/README.md": "README.md",
	}

	files, err = payload.GetEntries(files, targetNS, tempBaseBranch, triggertype.Push.String(), map[string]string{})
	assert.NilError(t, err)

	pr := tbbdc.CreatePR(ctx, t, client, runcnx, opts, repo, files, bitbucketWSOwner, targetNS)
	defer tbbdc.TearDown(ctx, t, runcnx, client, nil, bitbucketWSOwner, targetNS)

	// merge the pull request so that we can get push event.
	_, err = client.PullRequests.Merge(ctx, bitbucketWSOwner, pr.Number, &scm.PullRequestMergeOptions{})
	assert.NilError(t, err)

	successOpts := wait.SuccessOpt{
		TargetNS:        targetNS,
		OnEvent:         triggertype.Push.String(),
		NumberofPRMatch: 1,
		MinNumberStatus: 1,
	}
	wait.Succeeded(ctx, t, runcnx, opts, successOpts)

	pipelineRuns, err := runcnx.Clients.Tekton.TektonV1().PipelineRuns(targetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(pipelineRuns.Items), 1)
	// check that pipeline run contains on-path-change annotation.
	assert.Equal(t, pipelineRuns.Items[0].GetAnnotations()[keys.OnPathChange], "[doc/***.md]")
}
