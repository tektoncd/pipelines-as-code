//go:build e2e

package test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitlab "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitlab"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"github.com/tektoncd/pipeline/pkg/names"
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

func TestGitlabRetestAfterPipelineRunPruningFromFork(t *testing.T) {
	if !tgitlab.HasSecondIdentity() {
		t.Skip("Skipping fork retest pruning test: TEST_GITLAB_SECOND_TOKEN is not configured")
	}

	topts := &tgitlab.TestOpts{
		NoMRCreation: true,
		TargetEvent:  triggertype.PullRequest.String(),
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	assert.NilError(t, tgitlab.SetupSecondIdentity(ctx, topts))

	secondUser, _, err := topts.SecondGLProvider.Client().Users.CurrentUser()
	assert.NilError(t, err)
	assert.NilError(t, tgitlab.AddGitLabProjectMember(
		topts.GLProvider.Client(),
		topts.ProjectID,
		secondUser.ID,
		clientGitlab.DeveloperPermissions,
		topts.ParamsRun.Clients.Log,
	))

	topts.ParamsRun.Clients.Log.Infof("Fork destination group: %q (TEST_GITLAB_SECOND_GROUP)",
		os.Getenv("TEST_GITLAB_SECOND_GROUP"))
	forkProject, err := tgitlab.ForkGitLabProject(
		topts.SecondGLProvider.Client(),
		topts.ProjectID,
		os.Getenv("TEST_GITLAB_SECOND_GROUP"),
		true,
		topts.ParamsRun.Clients.Log,
	)
	assert.NilError(t, err)
	defer func() {
		topts.ParamsRun.Clients.Log.Infof("Deleting fork project %d", forkProject.ID)
		_, err := topts.SecondGLProvider.Client().Projects.DeleteProject(forkProject.ID, nil)
		if err != nil {
			t.Logf("Error deleting fork project %d: %v", forkProject.ID, err)
		}
	}()

	// Grant the first user (webhook token) access to the fork so the controller can read .tekton
	firstUser, _, err := topts.GLProvider.Client().Users.CurrentUser()
	assert.NilError(t, err)
	assert.NilError(t, tgitlab.AddGitLabProjectMember(
		topts.SecondGLProvider.Client(),
		int(forkProject.ID),
		firstUser.ID,
		clientGitlab.DeveloperPermissions,
		topts.ParamsRun.Clients.Log,
	))

	time.Sleep(5 * time.Second)

	entries, err := payload.GetEntries(map[string]string{
		".tekton/always-good-pipelinerun.yaml": "testdata/always-good-pipelinerun.yaml",
		".tekton/pipelinerun-exit-1.yaml":      "testdata/failures/pipelinerun-exit-1.yaml",
	}, topts.TargetNS, topts.DefaultBranch, triggertype.PullRequest.String(), map[string]string{})
	assert.NilError(t, err)

	targetRefName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-fork-retest")
	forkCloneURL, err := scm.MakeGitCloneURL(forkProject.WebURL, topts.SecondOpts.UserName, topts.SecondOpts.Password)
	assert.NilError(t, err)

	_ = scm.PushFilesToRefGit(t, &scm.Opts{
		GitURL:        forkCloneURL,
		CommitTitle:   "Add forked retest pruning fixtures - " + targetRefName,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        forkProject.WebURL,
		TargetRefName: targetRefName,
		BaseRefName:   topts.DefaultBranch,
	}, entries)

	topts.ParamsRun.Clients.Log.Infof("Pushed test files to fork at ref %q", targetRefName)
	mrTitle := "TestRetestAfterPipelineRunPruningFromFork - " + targetRefName
	mr, _, err := topts.SecondGLProvider.Client().MergeRequests.CreateMergeRequest(forkProject.ID, &clientGitlab.CreateMergeRequestOptions{
		Title:           &mrTitle,
		SourceBranch:    &targetRefName,
		TargetBranch:    &topts.DefaultBranch,
		TargetProjectID: &topts.ProjectInfo.ID,
	})
	assert.NilError(t, err)
	defer func() {
		_, _, err := topts.GLProvider.Client().MergeRequests.UpdateMergeRequest(topts.ProjectID, mr.IID,
			&clientGitlab.UpdateMergeRequestOptions{StateEvent: clientGitlab.Ptr("close")})
		if err != nil {
			t.Logf("Error closing MR %d: %v", mr.IID, err)
		}
	}()

	mr, _, err = topts.GLProvider.Client().MergeRequests.GetMergeRequest(topts.ProjectID, mr.IID, nil)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Created MR %q from fork with SHA %s", mr.WebURL, mr.SHA)

	labelSelector := fmt.Sprintf("%s=%s", keys.SHA, formatting.CleanValueKubernetes(mr.SHA))

	topts.ParamsRun.Clients.Log.Infof("Waiting for 2 PipelineRuns to appear for fork MR")
	err = twait.UntilMinPRAppeared(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:    topts.TargetNS,
		Namespace:   topts.TargetNS,
		PollTimeout: twait.DefaultTimeout,
		TargetSHA:   formatting.CleanValueKubernetes(mr.SHA),
	}, 2)
	assert.NilError(t, err)

	topts.ParamsRun.Clients.Log.Infof("Waiting for Repository status to have 2 entries for fork MR")
	_, err = twait.UntilRepositoryUpdated(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:        topts.TargetNS,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       mr.SHA,
	})
	assert.NilError(t, err)

	topts.ParamsRun.Clients.Log.Infof("Verifying commit statuses on fork (source) project for fork MR")
	sourceStatusCount, err := tgitlab.WaitForGitLabCommitStatusCount(ctx, topts.SecondGLProvider.Client(), topts.ParamsRun.Clients.Log, int(forkProject.ID), mr.SHA, "", 2)
	assert.NilError(t, err)
	assert.Assert(t, sourceStatusCount >= 2, "expected at least 2 commit statuses on fork (source) project, got %d", sourceStatusCount)

	topts.ParamsRun.Clients.Log.Infof("Verifying no commit statuses on target project for fork MR")
	targetStatuses, _, err := topts.GLProvider.Client().Commits.GetCommitStatuses(topts.ProjectID, mr.SHA, &clientGitlab.GetCommitStatusesOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(targetStatuses), 0,
		"expected no commit statuses on target project; with Developer access on fork, statuses are written directly to the source project")

	topts.ParamsRun.Clients.Log.Infof("Verifying initial PipelineRuns for fork MR")
	pruns, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), 2, "expected 2 initial PipelineRuns")

	initialPRNames := map[string]bool{}
	for _, pr := range pruns.Items {
		initialPRNames[pr.Name] = true
	}

	topts.ParamsRun.Clients.Log.Infof("Deleting all PipelineRuns to simulate pruning for fork MR")
	err = topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).DeleteCollection(ctx,
		metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector})
	assert.NilError(t, err)

	topts.ParamsRun.Clients.Log.Infof("Waiting for PipelineRuns to be deleted for fork MR")
	pollErr := kubeinteraction.PollImmediateWithContext(ctx, twait.DefaultTimeout, func() (bool, error) {
		pruns, err = topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return false, err
		}
		return len(pruns.Items) == 0, nil
	})
	if pollErr != nil {
		topts.ParamsRun.Clients.Log.Infof("Warning: PipelineRuns not fully deleted after polling: %v (proceeding anyway)", pollErr)
	}

	topts.ParamsRun.Clients.Log.Infof("Posting /retest comment on fork MR %d", topts.MRNumber)
	_, _, err = topts.GLProvider.Client().Notes.CreateMergeRequestNote(topts.ProjectID, mr.IID,
		&clientGitlab.CreateMergeRequestNoteOptions{Body: clientGitlab.Ptr("/retest")})
	assert.NilError(t, err)

	topts.ParamsRun.Clients.Log.Infof("Waiting for retest PipelineRun(s) to appear for fork MR")
	err = twait.UntilMinPRAppeared(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:    topts.TargetNS,
		Namespace:   topts.TargetNS,
		PollTimeout: twait.DefaultTimeout,
		TargetSHA:   formatting.CleanValueKubernetes(mr.SHA),
	}, 1)
	assert.NilError(t, err)

	topts.ParamsRun.Clients.Log.Infof("Waiting for Repository status to be updated with retest result for fork MR")
	_, err = twait.UntilRepositoryUpdated(ctx, topts.ParamsRun.Clients, twait.Opts{
		RepoName:            topts.TargetNS,
		Namespace:           topts.TargetNS,
		MinNumberStatus:     3,
		PollTimeout:         twait.DefaultTimeout,
		TargetSHA:           mr.SHA,
		FailOnRepoCondition: "no-match",
	})
	assert.NilError(t, err)

	topts.ParamsRun.Clients.Log.Infof("Asserting only the failed pipeline was re-run for fork MR")
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
		"expected only 1 new PipelineRun after /retest from a fork MR, but got %d", newCount)
}
