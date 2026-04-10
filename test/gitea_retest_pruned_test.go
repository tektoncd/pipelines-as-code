//go:build e2e

package test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestGiteaRetestAfterPipelineRunPruning verifies that /retest only re-runs
// failed pipelines when PipelineRun objects have been pruned from the cluster.
//
// This relies on GetCommitStatuses returning Forgejo commit statuses so that
// the annotation matcher can detect previously successful runs.
//
// Flow:
// 1. Create PR with 2 pipelines: one that succeeds, one that fails
// 2. Wait for both to complete
// 3. Delete all PipelineRun objects (simulating pruning)
// 4. Issue /retest
// 5. Assert that only the failed pipeline is re-run.
func TestGiteaRetestAfterPipelineRunPruning(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:     triggertype.PullRequest.String(),
		SkipEventsCheck: true,
		YAMLFiles: map[string]string{
			".tekton/always-good-pipelinerun.yaml": "testdata/always-good-pipelinerun.yaml",
			".tekton/pipelinerun-exit-1.yaml":      "testdata/failures/pipelinerun-exit-1.yaml",
		},
	}
	ctx, cleanup := tgitea.TestPR(t, topts)
	defer cleanup()

	sha := topts.SHA
	labelSelector := fmt.Sprintf("%s=%s", keys.SHA, formatting.CleanValueKubernetes(sha))

	// Wait for both PipelineRuns to appear
	topts.ParamsRun.Clients.Log.Infof("Waiting for 2 PipelineRuns to appear")
	err := twait.UntilMinPRAppeared(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:    topts.TargetNS,
		Namespace:   topts.TargetNS,
		PollTimeout: twait.DefaultTimeout,
		TargetSHA:   formatting.CleanValueKubernetes(sha),
	}, 2)
	assert.NilError(t, err)

	// Wait for repository to have at least 2 status entries
	topts.ParamsRun.Clients.Log.Infof("Waiting for Repository status to have 2 entries")
	_, err = twait.UntilRepositoryUpdated(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:            topts.TargetNS,
		Namespace:           topts.TargetNS,
		MinNumberStatus:     2,
		PollTimeout:         twait.DefaultTimeout,
		TargetSHA:           sha,
		FailOnRepoCondition: "no-match",
	})
	assert.NilError(t, err)

	// Verify we have exactly 2 PipelineRuns
	pruns, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), 2, "expected 2 initial PipelineRuns")

	// Record initial PipelineRun names
	initialPRNames := map[string]bool{}
	for _, pr := range pruns.Items {
		initialPRNames[pr.Name] = true
	}

	// Verify Forgejo commit statuses: exactly 1 successful template + 1 failed template
	statuses, _, err := topts.GiteaCNX.Client().ListStatuses(
		topts.Opts.Organization, topts.Opts.Repo, sha,
		forgejo.ListStatusesOption{},
	)
	assert.NilError(t, err)
	initialSummary := summarizeTerminalStatuses(statuses)
	successContexts, failureContexts := splitTerminalStatusContexts(initialSummary)
	assert.Equal(t, len(successContexts), 1, "expected exactly 1 successful pipeline context")
	assert.Equal(t, len(failureContexts), 1, "expected exactly 1 failed pipeline context")

	successContext := successContexts[0]
	failureContext := failureContexts[0]

	// Simulate pruning: delete all PipelineRun objects
	topts.ParamsRun.Clients.Log.Infof("Deleting all PipelineRuns to simulate pruning")
	err = topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).DeleteCollection(ctx,
		metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector})
	assert.NilError(t, err)

	// Wait for pruning to complete
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

	// Issue /retest comment on the PR
	topts.ParamsRun.Clients.Log.Infof("Posting /retest comment on PR %d", topts.PullRequest.Index)
	tgitea.PostCommentOnPullRequest(t, topts, "/retest")

	// Wait until the terminal provider statuses stop changing. This avoids
	// false-passing if a second, incorrect rerun is created slightly later.
	topts.ParamsRun.Clients.Log.Infof("Waiting for stable retest status set")
	finalSummary, err := waitForStableGiteaTerminalStatuses(ctx, topts, sha, 3)
	assert.NilError(t, err)

	assert.Equal(t, finalSummary[successContext].Success, initialSummary[successContext].Success,
		"expected successful pipeline context %q to not rerun", successContext)
	assert.Equal(t, finalSummary[successContext].Failure, initialSummary[successContext].Failure,
		"expected successful pipeline context %q to not gain failing statuses", successContext)
	assert.Equal(t, finalSummary[failureContext].Success, initialSummary[failureContext].Success,
		"expected failed pipeline context %q to remain unsuccessful", failureContext)
	assert.Equal(t, finalSummary[failureContext].Failure, initialSummary[failureContext].Failure+1,
		"expected failed pipeline context %q to rerun exactly once", failureContext)

	// Assert: only the failed pipeline should have been re-run.
	prunsAfterRetest, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	assert.NilError(t, err)

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

type terminalStatusSummary struct {
	Success int
	Failure int
}

func summarizeTerminalStatuses(statuses []*forgejo.Status) map[string]terminalStatusSummary {
	summary := map[string]terminalStatusSummary{}
	for _, status := range statuses {
		if status == nil {
			continue
		}
		contextSummary := summary[status.Context]
		switch status.State {
		case forgejo.StatusSuccess:
			contextSummary.Success++
		case forgejo.StatusFailure, forgejo.StatusError:
			contextSummary.Failure++
		default:
			continue
		}
		summary[status.Context] = contextSummary
	}
	return summary
}

func splitTerminalStatusContexts(summary map[string]terminalStatusSummary) ([]string, []string) {
	successContexts := []string{}
	failureContexts := []string{}
	for contextName, counts := range summary {
		switch {
		case counts.Success > 0 && counts.Failure == 0:
			successContexts = append(successContexts, contextName)
		case counts.Failure > 0 && counts.Success == 0:
			failureContexts = append(failureContexts, contextName)
		}
	}
	return successContexts, failureContexts
}

func waitForStableGiteaTerminalStatuses(ctx context.Context, topts *tgitea.TestOpts, sha string, minTerminalStatuses int) (map[string]terminalStatusSummary, error) {
	const stableWindow = 5 * time.Second

	var (
		lastSummary   map[string]terminalStatusSummary
		stableSummary map[string]terminalStatusSummary
		stableSince   time.Time
	)

	err := kubeinteraction.PollImmediateWithContext(ctx, twait.DefaultTimeout, func() (bool, error) {
		statuses, _, err := topts.GiteaCNX.Client().ListStatuses(
			topts.Opts.Organization, topts.Opts.Repo, sha,
			forgejo.ListStatusesOption{},
		)
		if err != nil {
			return false, err
		}

		summary := summarizeTerminalStatuses(statuses)
		terminalCount := 0
		for _, counts := range summary {
			terminalCount += counts.Success + counts.Failure
		}
		if terminalCount < minTerminalStatuses {
			return false, nil
		}

		if !reflect.DeepEqual(summary, lastSummary) {
			lastSummary = summary
			stableSummary = summary
			stableSince = time.Now()
			return false, nil
		}

		return time.Since(stableSince) >= stableWindow, nil
	})
	if err != nil {
		return nil, err
	}
	return stableSummary, nil
}
