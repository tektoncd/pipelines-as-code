//go:build e2e

package test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"

	"github.com/google/go-github/v81/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	tgitlab "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitlab"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/names"
	clientGitlab "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"knative.dev/pkg/apis"
)

func TestGitlabMergeRequest(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipelinerun.yaml":       "testdata/pipelinerun.yaml",
			".tekton/pipelinerun-clone.yaml": "testdata/pipelinerun-clone.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	commitTitle := "Committing files from test on " + topts.TargetRefName

	// Send another Push to make an update and make sure we react to it
	entries, err := payload.GetEntries(map[string]string{
		"hello-world.yaml": "testdata/pipelinerun.yaml",
	}, topts.TargetNS, topts.DefaultBranch,
		triggertype.PullRequest.String(), map[string]string{})
	assert.NilError(t, err)
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.TargetRefName,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	sopt := twait.SuccessOpt{
		Title:           commitTitle,
		OnEvent:         "Merge Request",
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 4,
		SHA:             "",
	}
	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)
	prsNew, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prsNew.Items) == 4)

	for i := 0; i < len(prsNew.Items); i++ {
		assert.Equal(t, "Merge Request", prsNew.Items[i].Annotations[keys.EventType])
	}

	// Get the MR to fetch the SHA
	mr, _, err := topts.GLProvider.Client().MergeRequests.GetMergeRequest(topts.ProjectID, int64(topts.MRNumber), nil)
	assert.NilError(t, err)

	// Check GitLab pipelines via API - should have 1 pipeline from normal MR processing
	pipelines, _, err := topts.GLProvider.Client().Pipelines.ListProjectPipelines(topts.ProjectID, &clientGitlab.ListProjectPipelinesOptions{
		SHA: &mr.SHA,
	})
	assert.NilError(t, err)
	assert.Assert(t, len(pipelines) == 1, "Expected 1 GitLab pipeline from normal MR processing, got %d", len(pipelines))

	topts.ParamsRun.Clients.Log.Infof("Sending /test comment on MergeRequest %s/-/merge_requests/%d", topts.GitHTMLURL, topts.MRNumber)
	_, _, err = topts.GLProvider.Client().Notes.CreateMergeRequestNote(topts.ProjectID, int64(topts.MRNumber), &clientGitlab.CreateMergeRequestNoteOptions{
		Body: clientGitlab.Ptr("/test"),
	})
	assert.NilError(t, err)

	sopt = twait.SuccessOpt{
		Title:           commitTitle,
		OnEvent:         opscomments.TestAllCommentEventType.String(),
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 5, // this is the max we get in repos status
		SHA:             mr.SHA,
	}
	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)
}

func TestGitlabOnLabel(t *testing.T) {
	prName := "on-label"
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			fmt.Sprintf(".tekton/%s.yaml", prName): "testdata/pipelinerun-on-label.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	topts.ParamsRun.Clients.Log.Infof("waiting 5 seconds until we make sure nothing happened")
	time.Sleep(1 * time.Second)
	prsNew, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prsNew.Items) == 0)

	// now add a Label
	mr, _, err := topts.GLProvider.Client().MergeRequests.UpdateMergeRequest(topts.ProjectID, int64(topts.MRNumber), &clientGitlab.UpdateMergeRequestOptions{
		Labels: &clientGitlab.LabelOptions{"bug"},
	})
	assert.NilError(t, err)

	waitOpts := twait.Opts{
		RepoName:        topts.TargetNS,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       mr.SHA,
	}
	repo, err := twait.UntilRepositoryUpdated(ctx, topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)
	assert.Assert(t, len(repo.Status) > 0)
	assert.Equal(t, *repo.Status[0].EventType, triggertype.PullRequestLabeled.String())
}

func TestGitlabOnComment(t *testing.T) {
	triggerComment := "/hello-world"
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipelinerun.yaml": "testdata/pipelinerun-on-comment-annotation.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	note, _, err := topts.GLProvider.Client().Notes.CreateMergeRequestNote(topts.ProjectID, int64(topts.MRNumber), &clientGitlab.CreateMergeRequestNoteOptions{
		Body: github.Ptr(triggerComment),
	})
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Note %s/-/merge_requests/%d/notes/%d has been created", topts.GitHTMLURL, int64(topts.MRNumber), note.ID)

	sopt := twait.SuccessOpt{
		OnEvent:         opscomments.OnCommentEventType.String(),
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 1,
		Title:           "Committing files from test on " + topts.TargetRefName,
	}
	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)

	// get pull request info
	mr, _, err := topts.GLProvider.Client().MergeRequests.GetMergeRequest(topts.ProjectID, int64(topts.MRNumber), nil)
	assert.NilError(t, err)

	waitOpts := twait.Opts{
		RepoName:        topts.TargetNS,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       mr.SHA,
	}
	repo, err := twait.UntilRepositoryUpdated(ctx, topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Check if we have the repository set as succeeded")
	assert.Assert(t, repo.Status[len(repo.Status)-1].Conditions[0].Status == corev1.ConditionTrue)
	lastPrName := repo.Status[len(repo.Status)-1].PipelineRunName

	err = twait.RegexpMatchingInPodLog(context.Background(), topts.ParamsRun, topts.TargetNS, fmt.Sprintf("tekton.dev/pipelineRun=%s", lastPrName), "step-task", *regexp.MustCompile(triggerComment), "", 2, nil)
	assert.NilError(t, err)
}

func TestGitlabCancelInProgressOnChange(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/in-progress.yaml": "testdata/pipelinerun-cancel-in-progress.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	topts.ParamsRun.Clients.Log.Infof("Waiting for the pipelinerun to be created")
	originalPipelineWaitOpts := twait.Opts{
		RepoName:        topts.TargetNS,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       topts.SHA,
	}
	err := twait.UntilPipelineRunCreated(ctx, topts.ParamsRun.Clients, originalPipelineWaitOpts)
	assert.NilError(t, err)

	newEntries := map[string]string{
		"new-file.txt": "plz work",
	}

	changeTitle := "TestCancelInProgress second commit - " + topts.TargetRefName
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.TargetRefName,
		CommitTitle:   changeTitle,
	}
	newSha := scm.PushFilesToRefGit(t, scmOpts, newEntries)

	topts.ParamsRun.Clients.Log.Infof("Waiting for new pipeline to be created")
	newPipelineWaitOpts := twait.Opts{
		RepoName:        topts.TargetNS,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       newSha,
	}
	err = twait.UntilPipelineRunCreated(ctx, topts.ParamsRun.Clients, newPipelineWaitOpts)
	assert.NilError(t, err)

	topts.ParamsRun.Clients.Log.Infof("Waiting for old pipelinerun to be cancelled")
	cancelledErr := twait.UntilPipelineRunHasReason(ctx, topts.ParamsRun.Clients, v1.PipelineRunReasonCancelled, originalPipelineWaitOpts)
	assert.NilError(t, cancelledErr)
}

func TestGitlabCancelInProgressOnPRClose(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/in-progress.yaml": "testdata/pipelinerun-cancel-in-progress.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	topts.ParamsRun.Clients.Log.Infof("Waiting for the pipelinerun to be created")
	waitOpts := twait.Opts{
		RepoName:        topts.TargetNS,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       topts.SHA,
	}
	err := twait.UntilPipelineRunCreated(ctx, topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)
	_, _, err = topts.GLProvider.Client().MergeRequests.UpdateMergeRequest(topts.ProjectID, int64(topts.MRNumber), &clientGitlab.UpdateMergeRequestOptions{
		StateEvent: clientGitlab.Ptr("close"),
	})
	assert.NilError(t, err)

	err = twait.UntilPipelineRunHasReason(ctx, topts.ParamsRun.Clients, v1.PipelineRunReasonCancelled, waitOpts)
	assert.NilError(t, err)

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 1, "should have only one pipelinerun, but we have: %d", len(prs.Items))
	assert.Equal(t, prs.Items[0].GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason(), "Cancelled", "should have been cancelled")

	// failing on `true` condition because for cancelled PipelineRun we want `false` condition.
	waitOpts.FailOnRepoCondition = corev1.ConditionTrue
	repo, err := twait.UntilRepositoryUpdated(ctx, topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)

	laststatus := repo.Status[len(repo.Status)-1]
	assert.Equal(t, "Cancelled", laststatus.Conditions[0].Reason)
}

func TestGitlabIssueGitopsComment(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/no-match.yaml": "testdata/pipelinerun-nomatch.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	_, _, err := topts.GLProvider.Client().Notes.CreateMergeRequestNote(topts.ProjectID, int64(topts.MRNumber), &clientGitlab.CreateMergeRequestNoteOptions{
		Body: clientGitlab.Ptr("/test no-match"),
	})
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Created gitops comment /test no-match to get the no-match tested")

	commitTitle := "Committing files from test on " + topts.TargetRefName
	sopt := twait.SuccessOpt{
		Title:           commitTitle,
		OnEvent:         opscomments.TestSingleCommentEventType.String(),
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 1,
	}
	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)
}

func TestGitlabDisableCommentsOnMRNotCreated(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		Settings: &v1alpha1.Settings{
			Gitlab: &v1alpha1.GitlabSettings{
				CommentStrategy: "disable_all",
			},
		},
		YAMLFiles: map[string]string{
			".tekton/pipelinerun.yaml": "testdata/pipelinerun.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	commitTitle := "Committing files from test on " + topts.TargetRefName
	sopt := twait.SuccessOpt{
		Title:           commitTitle,
		OnEvent:         "Merge Request",
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 1,
		SHA:             topts.SHA,
	}
	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)
	prsNew, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prsNew.Items) == 1)

	topts.ParamsRun.Clients.Log.Infof("Checking status of GitLab pipeline for commit: %s", topts.SHA)
	const pipelineCheckTimeout = 5 * time.Minute
	const pipelineCheckInterval = 10 * time.Second

	var pipeline *clientGitlab.Pipeline
	err = wait.PollUntilContextTimeout(ctx, pipelineCheckInterval, pipelineCheckTimeout, true, func(_ context.Context) (bool, error) {
		pipelines, _, listErr := topts.GLProvider.Client().Pipelines.ListProjectPipelines(topts.ProjectID, &clientGitlab.ListProjectPipelinesOptions{
			SHA: &topts.SHA,
		})
		if listErr != nil {
			return false, listErr
		}
		if len(pipelines) == 0 {
			topts.ParamsRun.Clients.Log.Info("Waiting for pipeline to be created...")
			return false, nil
		}
		if len(pipelines) > 1 {
			return false, fmt.Errorf("expected 1 pipeline for SHA %s, but found %d", topts.SHA, len(pipelines))
		}

		p, _, getErr := topts.GLProvider.Client().Pipelines.GetPipeline(topts.ProjectID, pipelines[0].ID)
		if getErr != nil {
			return false, getErr
		}

		topts.ParamsRun.Clients.Log.Infof("Current pipeline status: %s", p.Status)
		switch p.Status {
		case "success":
			pipeline = p
			return true, nil
		case "failed", "canceled", "skipped": //nolint:misspell
			return false, fmt.Errorf("pipeline finished with non-success status: %s", p.Status)
		default:
			return false, nil
		}
	})
	assert.NilError(t, err, "failed while waiting for GitLab pipeline to succeed")
	assert.Equal(t, "success", pipeline.Status, "The final pipeline status was not 'success'")
	topts.ParamsRun.Clients.Log.Infof("GitLab pipeline ID %d has succeeded!", pipeline.ID)

	// No comments will be added related to pipelineruns info
	notes, _, err := topts.GLProvider.Client().Notes.ListMergeRequestNotes(topts.ProjectID, int64(topts.MRNumber), nil)

	commentRegexp := regexp.MustCompile(`.*Pipelines as Code CI/*`)
	assert.NilError(t, err)
	successCommentsPost := 0
	for _, n := range notes {
		if commentRegexp.MatchString(n.Body) {
			successCommentsPost++
		}
	}
	// Since Gitlab comment strategy is disabled,
	// no comments will be posted related to PipelineRuns
	assert.Equal(t, 0, successCommentsPost)
}

func TestGitlabMergeRequestOnUpdateAtAndLabelChange(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipelinerun.yaml":       "testdata/pipelinerun.yaml",
			".tekton/pipelinerun-clone.yaml": "testdata/pipelinerun-clone.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	commitTitle := "Committing files from test on " + topts.TargetRefName
	sopt := twait.SuccessOpt{
		Title:           commitTitle,
		OnEvent:         "Merge Request",
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 2,
		SHA:             "",
	}
	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)
	prsNew, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prsNew.Items) == 2)

	topts.ParamsRun.Clients.Log.Infof("Changing Title on MergeRequest %s/-/merge_requests/%d", topts.GitHTMLURL, topts.MRNumber)
	_, _, err = topts.GLProvider.Client().MergeRequests.UpdateMergeRequest(topts.ProjectID, int64(topts.MRNumber), &clientGitlab.UpdateMergeRequestOptions{
		Title: clientGitlab.Ptr("test"),
	})
	assert.NilError(t, err)

	// let's wait 10 secs and check every second that a PipelineRun is created or not.
	for i := 0; i < 10; i++ {
		prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
		assert.NilError(t, err)
		assert.Assert(t, len(prs.Items) == 2)
		time.Sleep(1 * time.Second)
	}
}

func TestGitlabMergeRequestBadYaml(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/bad-yaml.yaml": "testdata/failures/bad-yaml.yaml",
		},
	}
	_, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	maxLoop := 10
	for i := 0; i < maxLoop; i++ {
		notes, _, err := topts.GLProvider.Client().Notes.ListMergeRequestNotes(topts.ProjectID, int64(topts.MRNumber), nil)
		assert.NilError(t, err)

		for _, note := range notes {
			if note.System {
				continue
			}
			golden.Assert(t, note.Body, strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
			return
		}

		topts.ParamsRun.Clients.Log.Infof("Looping %d/%d waiting for a comment to appear", i, maxLoop)
		time.Sleep(6 * time.Second)
	}

	t.Fatal("No comments with the pipelinerun error found on the merge request")
}

func TestGitlabMergeRequestValidationErrorsFromFork(t *testing.T) {
	topts := &tgitlab.TestOpts{
		NoMRCreation: true,
	}
	_, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	topts.ParamsRun.Clients.Log.Info("Testing GitLab validation error commenting from fork scenario")

	// Fork the project
	forkProject, _, err := topts.GLProvider.Client().Projects.ForkProject(topts.ProjectID, &clientGitlab.ForkProjectOptions{})
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Forked project %s (ID: %d) from original project (ID: %d)",
		forkProject.PathWithNamespace, forkProject.ID, topts.ProjectID)

	// Schedule fork project deletion
	defer func() {
		topts.ParamsRun.Clients.Log.Infof("Deleting fork project %d", forkProject.ID)
		_, err := topts.GLProvider.Client().Projects.DeleteProject(forkProject.ID, nil)
		if err != nil {
			t.Logf("Error deleting fork project %d: %v", forkProject.ID, err)
		}
	}()

	// Wait for fork to be ready
	time.Sleep(1 * time.Second)

	// Commit invalid .tekton files to the fork
	entries, err := payload.GetEntries(map[string]string{
		".tekton/bad-yaml.yaml": "testdata/failures/bad-yaml.yaml",
	}, topts.TargetNS, topts.DefaultBranch,
		triggertype.PullRequest.String(), map[string]string{})
	assert.NilError(t, err)

	targetRefName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-fork-test")
	forkCloneURL, err := scm.MakeGitCloneURL(forkProject.WebURL, topts.Opts.UserName, topts.Opts.Password)
	assert.NilError(t, err)

	commitTitle := "Add invalid .tekton file from fork - " + targetRefName
	scmOpts := &scm.Opts{
		GitURL:        forkCloneURL,
		CommitTitle:   commitTitle,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        forkProject.WebURL,
		TargetRefName: targetRefName,
		BaseRefName:   topts.DefaultBranch,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)
	topts.ParamsRun.Clients.Log.Infof("Pushed invalid .tekton files to fork branch: %s", targetRefName)

	// Create merge request from fork to original project
	mrTitle := "TestValidationErrorsFromFork - " + targetRefName
	mrOptions := &clientGitlab.CreateMergeRequestOptions{
		Title:           &mrTitle,
		SourceBranch:    &targetRefName,
		TargetBranch:    &topts.DefaultBranch,
		TargetProjectID: &topts.ProjectInfo.ID,
	}

	mr, _, err := topts.GLProvider.Client().MergeRequests.CreateMergeRequest(forkProject.ID, mrOptions)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Created merge request from fork to original: %s/-/merge_requests/%d",
		topts.GitHTMLURL, mr.IID)

	defer func() {
		// Close MR on original project
		_, _, err := topts.GLProvider.Client().MergeRequests.UpdateMergeRequest(topts.ProjectID, mr.IID,
			&clientGitlab.UpdateMergeRequestOptions{StateEvent: clientGitlab.Ptr("close")})
		if err != nil {
			t.Logf("Error closing MR %d: %v", mr.IID, err)
		}
	}()

	topts.ParamsRun.Clients.Log.Info("Waiting for webhook validation to process MR and post validation comment...")

	maxLoop := 12
	foundValidationComment := false

	for i := 0; i < maxLoop; i++ {
		notes, _, err := topts.GLProvider.Client().Notes.ListMergeRequestNotes(topts.ProjectID, mr.IID, nil)
		assert.NilError(t, err)

		for _, note := range notes {
			if regexp.MustCompile(`.*There are some errors in your PipelineRun template.*`).MatchString(note.Body) &&
				regexp.MustCompile(`.*bad-yaml\.yaml.*yaml validation error.*`).MatchString(note.Body) {
				topts.ParamsRun.Clients.Log.Info("Found validation error comment on original project's MR!")
				foundValidationComment = true

				assert.Assert(t, regexp.MustCompile(`\[!CAUTION\]`).MatchString(note.Body), "Comment should contain caution header")
				break
			}
		}

		if foundValidationComment {
			break
		}

		topts.ParamsRun.Clients.Log.Infof("Loop %d/%d: Waiting for webhook validation to post comment (testing TargetProjectID fix)", i+1, maxLoop)
		time.Sleep(6 * time.Second)
	}

	assert.Assert(t, foundValidationComment, "Validation error comment should appear on original project's MR. ")
}

func TestGitlabConsistentCommitStatusOnMR(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/bad-pipelinerun.yaml":         "testdata/failures/bad-pipelinerun.yaml",
			".tekton/always-good-pipelinerun.yaml": "testdata/always-good-pipelinerun.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	commitTitle := "Committing files from test on " + topts.TargetRefName

	// Get MR for SHA
	mr, _, err := topts.GLProvider.Client().MergeRequests.GetMergeRequest(topts.ProjectID, int64(topts.MRNumber), nil)
	assert.NilError(t, err)

	sopt := twait.SuccessOpt{
		Title:           commitTitle,
		OnEvent:         "Merge Request",
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 1,
		SHA:             mr.SHA,
	}
	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)
	labelSelector := fmt.Sprintf("%s=%s", keys.SHA, formatting.CleanValueKubernetes(mr.SHA))
	prsNew, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	assert.NilError(t, err)
	assert.Assert(t, len(prsNew.Items) == 1)

	commitStatuses, _, err := topts.GLProvider.Client().Commits.GetCommitStatuses(topts.ProjectID, mr.SHA, &clientGitlab.GetCommitStatusesOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(commitStatuses) == 2)

	for _, cs := range commitStatuses {
		switch cs.Name {
		case "Pipelines as Code CI / bad-converts-good-pipelinerun":
			assert.Assert(t, cs.Status == "failed")
		case "Pipelines as Code CI / always-good-pipelinerun":
			assert.Assert(t, cs.Status == "success")
		default:
			t.Fatalf("unexpected commit status name: %s", cs.Name)
		}
	}

	entries, err := payload.GetEntries(map[string]string{
		".tekton/bad-pipelinerun.yaml":         "testdata/bad-converts-good-pipelinerun.yaml",
		".tekton/always-good-pipelinerun.yaml": "testdata/always-good-pipelinerun.yaml",
	}, topts.TargetNS, topts.DefaultBranch,
		triggertype.PullRequest.String(), map[string]string{})
	assert.NilError(t, err)

	commitTitle = "Add good .tekton file - " + topts.TargetRefName
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		CommitTitle:   commitTitle,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.DefaultBranch,
		PushForce:     true,
	}
	newSHA := scm.PushFilesToRefGit(t, scmOpts, entries)
	topts.ParamsRun.Clients.Log.Infof("Pushed good .tekton files to branch: %s", topts.TargetRefName)

	sopt = twait.SuccessOpt{
		Title:           commitTitle,
		OnEvent:         "Merge Request",
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 2,
		SHA:             newSHA,
	}

	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)
	labelSelector = fmt.Sprintf("%s=%s", keys.SHA, formatting.CleanValueKubernetes(newSHA))
	prsNew, err = topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	assert.NilError(t, err)
	assert.Assert(t, len(prsNew.Items) == 2)

	commitStatuses, _, err = topts.GLProvider.Client().Commits.GetCommitStatuses(topts.ProjectID, newSHA, &clientGitlab.GetCommitStatusesOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(commitStatuses) == 2)

	for _, cs := range commitStatuses {
		switch cs.Name {
		case "Pipelines as Code CI / bad-converts-good-pipelinerun", "Pipelines as Code CI / always-good-pipelinerun":
			assert.Assert(t, cs.Status == "success")
		default:
			t.Fatalf("unexpected commit status name: %s", cs.Name)
		}
	}
}

// TestGitlabMergeRequestCelPrefix tests the cel: prefix for arbitrary CEL expressions.
func TestGitlabMergeRequestCelPrefix(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipelinerun.yaml": "testdata/pipelinerun-cel-prefix-gitlab.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	commitTitle := "Committing files from test on " + topts.TargetRefName
	sopt := twait.SuccessOpt{
		Title:           commitTitle,
		OnEvent:         "Merge Request",
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 1,
		SHA:             "",
	}
	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prs.Items) >= 1, "Expected at least one PipelineRun, got %d", len(prs.Items))

	err = twait.RegexpMatchingInPodLog(
		ctx,
		topts.ParamsRun,
		topts.TargetNS,
		fmt.Sprintf("tekton.dev/pipelineRun=%s,tekton.dev/pipelineTask=cel-prefix-test", prs.Items[0].Name),
		"step-test-cel-prefix-values",
		regexp.Regexp{},
		t.Name(),
		2,
		nil,
	)
	assert.NilError(t, err)
}

// TestGitlabMergeRequestVariableSubs tests variable substitution in PipelineRun annotations.
func TestGitlabMergeRequestVariableSubs(t *testing.T) {
	topts := &tgitlab.TestOpts{
		NoMRCreation: true,
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	targetRefName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-variable-subs.yaml": "testdata/pipelinerun-variable-subs.yaml",
	}, topts.TargetNS, targetRefName,
		triggertype.Push.String(), map[string]string{})
	assert.NilError(t, err)

	commitTitle := "Testing variable substitution on " + targetRefName
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		CommitTitle:   commitTitle,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: targetRefName,
		BaseRefName:   topts.DefaultBranch,
	}
	sha := scm.PushFilesToRefGit(t, scmOpts, entries)

	topts.ParamsRun.Clients.Log.Infof("Branch %s has been created and pushed with files, commit SHA: %s", targetRefName, sha)

	// Make a comment on the commit
	topts.ParamsRun.Clients.Log.Infof("Creating comment /test pipelinerun-variable-subs on commit %s", sha)
	commentOpts := &clientGitlab.PostCommitCommentOptions{
		Note: clientGitlab.Ptr(fmt.Sprintf("/test pipelinerun-variable-subs branch:%s", targetRefName)),
	}
	cc, _, err := topts.GLProvider.Client().Commits.PostCommitComment(topts.ProjectID, sha, commentOpts)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Commit comment %s has been created", cc.Note)

	// Wait for PipelineRun creation
	waitOpts := twait.Opts{
		RepoName:        topts.TargetNS,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       sha,
	}
	err = twait.UntilPipelineRunHasReason(ctx, topts.ParamsRun.Clients, v1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)

	// Get the PipelineRun
	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prs.Items) >= 1, "Expected at least one PipelineRun, got %d", len(prs.Items))

	// Check that PipelineRun logs contain the commit message
	err = twait.RegexpMatchingInPodLog(
		ctx,
		topts.ParamsRun,
		topts.TargetNS,
		fmt.Sprintf("pipelinesascode.tekton.dev/event-type=%s",
			opscomments.TestSingleCommentEventType.String()),
		"step-task",
		*regexp.MustCompile(regexp.QuoteMeta(commitTitle)),
		"",
		2,
		nil,
	)
	assert.NilError(t, err, "PipelineRun logs should contain the commit message: %s", commitTitle)
}

// TestGitlabMergeRequestCommentStrategyUpdateCELErrorReplacement tests:
// 1. A CEL error comment is posted for a PLR
// 2. After fixing the CEL error with a new commit, the same comment is updated with success status
// 3. Only one comment exists.
func TestGitlabMergeRequestCommentStrategyUpdateCELErrorReplacement(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipelinerun-invalid-cel.yaml": "testdata/failures/pipelinerun-invalid-cel.yaml",
		},
		SkipEventsCheck: true,
		Settings: &v1alpha1.Settings{
			Gitlab: &v1alpha1.GitlabSettings{
				CommentStrategy: provider.UpdateCommentStrategy,
			},
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	// Poll for the CEL error note
	topts.ParamsRun.Clients.Log.Infof("Waiting for CEL error comment to be created")
	var celErrorNoteID int64
	var pipelineRunName string
	markerPattern := regexp.MustCompile(`<!-- pac-status-([^\s]+) -->`)
	maxLoop := 20
	for i := 0; i < maxLoop; i++ {
		notes, _, err := topts.GLProvider.Client().Notes.ListMergeRequestNotes(topts.ProjectID, int64(topts.MRNumber), nil)
		assert.NilError(t, err)
		for _, note := range notes {
			if note.System {
				continue
			}
			matches := markerPattern.FindStringSubmatch(note.Body)
			if len(matches) > 1 {
				celErrorNoteID = note.ID
				pipelineRunName = matches[1]
				break
			}
		}
		if celErrorNoteID != 0 {
			break
		}
		topts.ParamsRun.Clients.Log.Infof("Loop %d/%d: Waiting for CEL error comment...", i+1, maxLoop)
		time.Sleep(2 * time.Second)
	}
	assert.Assert(t, celErrorNoteID != 0, "CEL error comment not found")
	topts.ParamsRun.Clients.Log.Infof("Found CEL error note ID: %d for PLR: %s", celErrorNoteID, pipelineRunName)

	// Fix the CEL expression and push
	fixedContentRaw, err := os.ReadFile("testdata/failures/pipelinerun-invalid-cel.yaml")
	assert.NilError(t, err)
	fixedContent := strings.ReplaceAll(string(fixedContentRaw), `event == "pull request" |`, `event_type == "Merge Request"`)
	fixedContent = strings.ReplaceAll(fixedContent, `"\\ .PipelineName //"`, fmt.Sprintf("%q", pipelineRunName))
	fixedContent = strings.ReplaceAll(fixedContent, `"\\ .TargetNamespace //"`, fmt.Sprintf("%q", topts.TargetNS))

	commitTitle := "fix: replace CEL error with valid pipelinerun"
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.TargetRefName,
		CommitTitle:   commitTitle,
	}
	sha := scm.PushFilesToRefGit(t, scmOpts, map[string]string{
		".tekton/pipelinerun-invalid-cel.yaml": fixedContent,
	})
	topts.ParamsRun.Clients.Log.Infof("Pushed fix commit: %s", sha)

	sopt := twait.SuccessOpt{
		Title:           commitTitle,
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 1,
		SHA:             sha,
		OnEvent:         "Merge Request",
	}
	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)

	// Verify commit status is success
	commitStatuses, _, err := topts.GLProvider.Client().Commits.GetCommitStatuses(topts.ProjectID, sha, &clientGitlab.GetCommitStatusesOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(commitStatuses) > 0, "Expected at least one commit status")
	for _, cs := range commitStatuses {
		assert.Equal(t, "success", cs.Status,
			"Commit status %s should be success, got %s", cs.Name, cs.Status)
	}

	// Verify the same comment was updated (not recreated)
	notes, _, err := topts.GLProvider.Client().Notes.ListMergeRequestNotes(topts.ProjectID, int64(topts.MRNumber), nil)
	assert.NilError(t, err)
	var updatedNoteID int64
	for _, note := range notes {
		if note.System {
			continue
		}
		if markerPattern.MatchString(note.Body) {
			updatedNoteID = note.ID
			break
		}
	}
	assert.Assert(t, updatedNoteID != 0, "Updated comment not found")
	assert.Equal(t, celErrorNoteID, updatedNoteID,
		"Comment should be updated (ID %d), not a new one created (got ID %d)",
		celErrorNoteID, updatedNoteID)
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -run ^TestGitlabMergeRequest$"
// End:
