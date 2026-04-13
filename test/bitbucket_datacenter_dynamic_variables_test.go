//go:build e2e

package test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tbbdc "github.com/openshift-pipelines/pipelines-as-code/test/pkg/bitbucketdatacenter"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"

	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
)

func TestBitbucketDataCenterDynamicVariables(t *testing.T) {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	ctx := context.Background()
	bitbucketWSOwner := os.Getenv("TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY")

	ctx, runcnx, opts, client, err := tbbdc.Setup(ctx)
	assert.NilError(t, err)

	repo := tbbdc.CreateCRD(ctx, t, client, runcnx, bitbucketWSOwner, targetNS)
	runcnx.Clients.Log.Infof("Repository %s has been created", repo.Name)
	defer tbbdc.TearDownNs(ctx, t, runcnx, targetNS)

	branch, _, err := client.Git.CreateRef(ctx, bitbucketWSOwner, targetNS, "refs/heads/main")
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Branch %s has been created", branch.Name)

	files := map[string]string{
		".tekton/pipelinerun.yaml": "testdata/pipelinerun-dynamic-vars.yaml",
	}

	files, err = payload.GetEntries(files, targetNS, targetNS, triggertype.Push.String(), map[string]string{})
	assert.NilError(t, err)
	gitCloneURL, err := scm.MakeGitCloneURL(repo.Clone, opts.UserName, opts.Password)
	assert.NilError(t, err)

	commitMsg := fmt.Sprintf("commit %s", targetNS)
	scmOpts := &scm.Opts{
		GitURL:        gitCloneURL,
		Log:           runcnx.Clients.Log,
		WebURL:        repo.Clone,
		TargetRefName: targetNS,
		BaseRefName:   repo.Branch,
		CommitTitle:   commitMsg,
	}
	scm.PushFilesToRefGit(t, scmOpts, files)
	runcnx.Clients.Log.Infof("Files has been pushed to branch %s", targetNS)

	successOpts := wait.SuccessOpt{
		TargetNS:        targetNS,
		OnEvent:         triggertype.Push.String(),
		NumberofPRMatch: 1,
		MinNumberStatus: 1,
	}
	wait.Succeeded(ctx, t, runcnx, opts, successOpts)

	reg := *regexp.MustCompile(fmt.Sprintf("event: repo:refs_changed, refId: refs/heads/%s, message: %s", targetNS, commitMsg))
	err = wait.RegexpMatchingInPodLog(ctx, runcnx, targetNS, "pipelinesascode.tekton.dev/original-prname=pipelinerun-dynamic-vars", "step-task", reg, "", 2, nil)
	assert.NilError(t, err)
}
