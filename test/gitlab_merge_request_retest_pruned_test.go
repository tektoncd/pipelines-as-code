//go:build e2e

package test

import (
	"fmt"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitlab "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitlab"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	clientGitlab "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestGitlabRetestAfterPipelineRunPruning reproduces the bug where /retest
// re-runs all pipelines instead of only the failed ones when PipelineRun
// objects have been pruned from the cluster.
//
// See: https://github.com/openshift-pipelines/pipelines-as-code/issues/2580
//
// Flow:
// 1. Create MR with 2 pipelines: one that succeeds, one that fails
// 2. Wait for both to complete
// 3. Delete all PipelineRun objects (simulating pruning)
// 4. Issue /retest
// 5. Assert that only the failed pipeline is re-run (not both).
func TestGitlabRetestAfterPipelineRunPruning(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/always-good-pipelinerun.yaml": "testdata/always-good-pipelinerun.yaml",
			".tekton/pipelinerun-exit-1.yaml":      "testdata/failures/pipelinerun-exit-1.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	// Get MR to obtain the SHA
	mr, _, err := topts.GLProvider.Client().MergeRequests.GetMergeRequest(topts.ProjectID, int64(topts.MRNumber), nil)
	assert.NilError(t, err)

	labelSelector := fmt.Sprintf("%s=%s", keys.SHA, formatting.CleanValueKubernetes(mr.SHA))

	// Wait for both PipelineRuns to appear
	topts.ParamsRun.Clients.Log.Infof("Waiting for 2 PipelineRuns to appear")
	err = twait.UntilMinPRAppeared(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:    topts.TargetNS,
		Namespace:   topts.TargetNS,
		PollTimeout: twait.DefaultTimeout,
		TargetSHA:   formatting.CleanValueKubernetes(mr.SHA),
	}, 2)
	assert.NilError(t, err)

	// Wait for repository to have at least 2 status entries (both pipelines reported)
	topts.ParamsRun.Clients.Log.Infof("Waiting for Repository status to have 2 entries")
	_, err = twait.UntilRepositoryUpdated(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:        topts.TargetNS,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       mr.SHA,
	})
	assert.NilError(t, err)

	// Verify we have exactly 2 PipelineRuns
	pruns, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), 2, "expected 2 initial PipelineRuns")

	// Record initial PipelineRun names so we can distinguish old from new after /retest
	initialPRNames := map[string]bool{}
	for _, pr := range pruns.Items {
		initialPRNames[pr.Name] = true
	}

	// Verify GitLab commit statuses: 1 success + 1 failure
	commitStatuses, _, err := topts.GLProvider.Client().Commits.GetCommitStatuses(topts.ProjectID, mr.SHA, &clientGitlab.GetCommitStatusesOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(commitStatuses) >= 2, "expected at least 2 commit statuses, got %d", len(commitStatuses))

	successCount := 0
	failureCount := 0
	for _, cs := range commitStatuses {
		switch cs.Status {
		case "success":
			successCount++
		case "failed":
			failureCount++
		}
	}
	assert.Assert(t, successCount >= 1, "expected at least 1 successful commit status")
	assert.Assert(t, failureCount >= 1, "expected at least 1 failed commit status")

	// Simulate pruning: delete all PipelineRun objects
	topts.ParamsRun.Clients.Log.Infof("Deleting all PipelineRuns to simulate pruning")
	err = topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).DeleteCollection(ctx,
		metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector})
	assert.NilError(t, err)

	// Wait for pruning to complete (DeleteCollection is async)
	// This is best-effort — the real assertion is on new PipelineRuns after /retest
	topts.ParamsRun.Clients.Log.Infof("Waiting for PipelineRuns to be deleted")
	pollErr := kubeinteraction.PollImmediateWithContext(ctx, twait.DefaultTimeout, func() (bool, error) {
		pruns, err = topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return false, err
		}
		topts.ParamsRun.Clients.Log.Infof("Waiting for PipelineRuns to be deleted: %d remaining", len(pruns.Items))
		return len(pruns.Items) == 0, nil
	})
	if pollErr != nil {
		topts.ParamsRun.Clients.Log.Infof("Warning: PipelineRuns not fully deleted after polling: %v (proceeding anyway)", pollErr)
	}

	// Issue /retest comment on the MR
	topts.ParamsRun.Clients.Log.Infof("Posting /retest comment on MR %d", topts.MRNumber)
	_, _, err = topts.GLProvider.Client().Notes.CreateMergeRequestNote(topts.ProjectID, int64(topts.MRNumber),
		&clientGitlab.CreateMergeRequestNoteOptions{Body: clientGitlab.Ptr("/retest")})
	assert.NilError(t, err)

	// Wait for retest pipeline(s) to be created
	// After /retest, we expect only 1 new PipelineRun (the failed one re-runs)
	topts.ParamsRun.Clients.Log.Infof("Waiting for retest PipelineRun(s) to appear")
	err = twait.UntilMinPRAppeared(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:    topts.TargetNS,
		Namespace:   topts.TargetNS,
		PollTimeout: twait.DefaultTimeout,
		TargetSHA:   formatting.CleanValueKubernetes(mr.SHA),
	}, 1)
	assert.NilError(t, err)

	// Wait for repository status to be updated with the retest result
	// We expect the re-run pipeline to fail (it's pipelinerun-exit-1), so disable
	// the default FailOnRepoCondition=False check by setting it to a no-match value.
	_, err = twait.UntilRepositoryUpdated(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:            topts.TargetNS,
		Namespace:           topts.TargetNS,
		MinNumberStatus:     3,
		PollTimeout:         twait.DefaultTimeout,
		TargetSHA:           mr.SHA,
		FailOnRepoCondition: "no-match",
	})
	assert.NilError(t, err)

	// Assert: only the failed pipeline should have been re-run
	prunsAfterRetest, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	assert.NilError(t, err)

	// Count only NEW PipelineRuns (filter out any old ones that may still be lingering)
	newCount := 0
	for _, pr := range prunsAfterRetest.Items {
		if !initialPRNames[pr.Name] {
			newCount++
		}
	}
	assert.Equal(t, newCount, 1,
		"expected only 1 new PipelineRun after /retest (only the failed pipeline should re-run), but got %d",
		newCount)
}
