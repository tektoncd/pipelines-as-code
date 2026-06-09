//go:build e2e

package test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tbbs "github.com/openshift-pipelines/pipelines-as-code/test/pkg/bitbucketdatacenter"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"

	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
)

func TestBitbucketDataCenterCELPathChangeOnPush(t *testing.T) {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	ctx := context.Background()
	bitbucketWSOwner := os.Getenv("TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY")

	ctx, runcnx, opts, client, err := tbbs.Setup(ctx)
	assert.NilError(t, err)

	repo := tbbs.CreateCRD(ctx, t, client, runcnx, bitbucketWSOwner, targetNS)
	runcnx.Clients.Log.Infof("Repository %s has been created", repo.Name)
	defer tbbs.TearDownNs(ctx, t, runcnx, targetNS)

	files := map[string]string{
		".tekton/pipelinerun.yaml": "testdata/pipelinerun-cel-path-changed.yaml",
	}

	mainBranchRef := "refs/heads/main"
	branch, resp, err := client.Git.CreateRef(ctx, bitbucketWSOwner, targetNS, mainBranchRef)
	assert.NilError(t, err, "error creating branch: http status code: %d : %v", resp.Status, err)
	runcnx.Clients.Log.Infof("Branch %s has been created", branch.Name)
	defer tbbs.TearDown(ctx, t, runcnx, client, -1, bitbucketWSOwner, branch.Name)

	files, err = payload.GetEntries(files, targetNS, branch.Name, triggertype.Push.String(), map[string]string{})
	assert.NilError(t, err)
	gitCloneURL, err := scm.MakeGitCloneURL(repo.Clone, opts.UserName, opts.Password)
	assert.NilError(t, err)
	scmOpts := &scm.Opts{
		GitURL:        gitCloneURL,
		Log:           runcnx.Clients.Log,
		WebURL:        repo.Clone,
		TargetRefName: targetNS,
		BaseRefName:   repo.Branch,
		CommitTitle:   fmt.Sprintf("commit %s", targetNS),
	}
	scm.PushFilesToRefGit(t, scmOpts, files)
	runcnx.Clients.Log.Infof("Branch %s has been created and pushed with files", targetNS)

	successOpts := wait.SuccessOpt{
		TargetNS:        targetNS,
		OnEvent:         triggertype.Push.String(),
		NumberofPRMatch: 1,
		MinNumberStatus: 1,
	}
	wait.Succeeded(ctx, t, runcnx, opts, successOpts)
}
