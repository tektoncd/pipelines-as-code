//go:build e2e

package test

import (
	"context"
	"fmt"
	"testing"

	"github.com/ktrysmt/go-bitbucket"
	"github.com/mitchellh/mapstructure"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	tbb "github.com/openshift-pipelines/pipelines-as-code/test/pkg/bitbucketcloud"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestBitbucketCloudRetestAfterPipelineRunPruning verifies that /retest only
// re-runs failed pipelines when PipelineRun objects have been pruned from the
// cluster. This relies on GetCommitStatuses returning Bitbucket Cloud commit
// statuses so that the annotation matcher can detect previously successful runs.
func TestBitbucketCloudRetestAfterPipelineRunPruning(t *testing.T) {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	ctx := context.Background()

	runcnx, opts, bprovider, err := tbb.Setup(ctx)
	if err != nil {
		t.Skip(err.Error())
		return
	}
	bcrepo := tbb.CreateCRD(ctx, t, bprovider, runcnx, opts, targetNS)
	targetRefName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
	title := "TestRetestPruned - " + targetRefName

	entries, err := payload.GetEntries(
		map[string]string{
			".tekton/always-good-pipelinerun-long-name.yaml": "testdata/always-good-pipelinerun-long-name.yaml",
			".tekton/pipelinerun-exit-1.yaml":                "testdata/failures/pipelinerun-exit-1.yaml",
		},
		targetNS, options.MainBranch, triggertype.PullRequest.String(), map[string]string{},
	)
	assert.NilError(t, err)

	pr, repobranch := tbb.MakePR(t, bprovider, runcnx, bcrepo, opts, title, targetRefName, entries)
	defer tbb.TearDown(ctx, t, runcnx, bprovider, opts, pr.ID, targetRefName, targetNS, false)

	hash, ok := repobranch.Target["hash"].(string)
	assert.Assert(t, ok)

	labelSelector := fmt.Sprintf("%s=%s", keys.SHA, formatting.CleanValueKubernetes(hash))

	runcnx.Clients.Log.Infof("Waiting for 2 PipelineRuns to finish")
	_, err = twait.UntilPipelineRunsFinished(ctx, runcnx.Clients, twait.Opts{
		Namespace:       targetNS,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{hash},
	})
	assert.NilError(t, err)

	pruns, err := runcnx.Clients.Tekton.TektonV1().PipelineRuns(targetNS).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), 2, "expected 2 initial PipelineRuns")

	initialPRNames := map[string]bool{}
	for _, prun := range pruns.Items {
		initialPRNames[prun.Name] = true
	}

	// Verify Bitbucket Cloud commit statuses before pruning
	successCount, failureCount := countBBCloudTerminalStatuses(t, bprovider, opts, hash)
	assert.Equal(t, successCount, 1, "expected exactly 1 successful pipeline status")
	assert.Equal(t, failureCount, 1, "expected exactly 1 failed pipeline status")

	runcnx.Clients.Log.Infof("Deleting all PipelineRuns to simulate pruning")
	err = runcnx.Clients.Tekton.TektonV1().PipelineRuns(targetNS).DeleteCollection(ctx,
		metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector})
	assert.NilError(t, err)

	runcnx.Clients.Log.Infof("Waiting for PipelineRuns to be deleted")
	pollErr := kubeinteraction.PollImmediateWithContext(ctx, twait.DefaultTimeout, func() (bool, error) {
		pruns, err = runcnx.Clients.Tekton.TektonV1().PipelineRuns(targetNS).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return false, err
		}
		return len(pruns.Items) == 0, nil
	})
	if pollErr != nil {
		runcnx.Clients.Log.Infof("Warning: PipelineRuns not fully deleted after polling: %v (proceeding anyway)", pollErr)
	}

	runcnx.Clients.Log.Infof("Posting /retest comment on PR %d", pr.ID)
	_, err = bprovider.Client().Repositories.PullRequests.AddComment(
		&bitbucket.PullRequestCommentOptions{
			Owner:         opts.Organization,
			RepoSlug:      opts.Repo,
			PullRequestID: fmt.Sprintf("%d", pr.ID),
			Content:       "/retest",
		},
	)
	assert.NilError(t, err)

	runcnx.Clients.Log.Infof("Waiting for retest PipelineRun to finish")
	_, err = twait.UntilPipelineRunsFinished(ctx, runcnx.Clients, twait.Opts{
		Namespace:       targetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{hash},
	})
	assert.NilError(t, err)

	prunsAfterRetest, err := runcnx.Clients.Tekton.TektonV1().PipelineRuns(targetNS).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	assert.NilError(t, err)

	newCount := 0
	for _, prun := range prunsAfterRetest.Items {
		if !initialPRNames[prun.Name] {
			newCount++
		}
	}
	assert.Equal(t, newCount, 1,
		"expected only 1 new PipelineRun after /retest (only the failed pipeline should re-run), but got %d", newCount)
}

func countBBCloudTerminalStatuses(t *testing.T, bprovider bitbucketcloud.Provider, opts options.E2E, sha string) (successCount, failureCount int) {
	t.Helper()
	resp, err := bprovider.Client().Repositories.Commits.GetCommitStatuses(&bitbucket.CommitsOptions{
		Owner:    opts.Organization,
		RepoSlug: opts.Repo,
		Revision: sha,
	})
	assert.NilError(t, err)

	statusList := &types.Statuses{}
	err = mapstructure.Decode(resp, statusList)
	assert.NilError(t, err)

	seen := map[string]struct{}{}
	for _, s := range statusList.Values {
		if _, ok := seen[s.Key]; ok {
			continue
		}
		seen[s.Key] = struct{}{}
		switch s.State {
		case string(types.StateSuccessful):
			successCount++
		case string(types.StateFailed), string(types.StateStopped):
			failureCount++
		}
	}
	return successCount, failureCount
}
