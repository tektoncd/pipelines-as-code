//go:build e2e

package test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitlab "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitlab"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	"github.com/tektoncd/pipeline/pkg/names"
	clientGitlab "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
)

// TestGitlabSuccessStatusAfterOkToTest tests that when an unauthorized user
// creates a fork MR, the CI status starts as pending/skipped, and after an
// authorized user posts /ok-to-test the status transitions to success.
func TestGitlabSuccessStatusAfterOkToTest(t *testing.T) {
	ctx := context.Background()
	if !tgitlab.HasSecondIdentity() {
		t.Skip("Skipping: TEST_GITLAB_SECOND_TOKEN is not configured")
	}

	topts := &tgitlab.TestOpts{
		NoMRCreation: true,
		TargetEvent:  triggertype.PullRequest.String(),
	}

	runcnx, opts, glprovider, err := tgitlab.Setup(ctx)
	assert.NilError(t, err, fmt.Errorf("cannot do gitlab setup: %w", err))
	topts.GLProvider = glprovider
	topts.ParamsRun = runcnx
	topts.Opts = opts
	topts.TargetRefName = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
	topts.TargetNS = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	// Create a fresh GitLab project
	groupPath := os.Getenv("TEST_GITLAB_GROUP")
	hookURL := os.Getenv("TEST_GITLAB_SMEEURL")
	webhookSecret := os.Getenv("TEST_EL_WEBHOOK_SECRET")
	project, err := tgitlab.CreateGitLabProject(topts.GLProvider.Client(), groupPath, topts.TargetRefName, hookURL, webhookSecret, false, topts.ParamsRun.Clients.Log)
	assert.NilError(t, err)
	topts.ProjectID = int(project.ID)
	topts.ProjectInfo = project
	topts.GitHTMLURL = project.WebURL
	topts.DefaultBranch = project.DefaultBranch

	defer func() {
		if os.Getenv("TEST_NOCLEANUP") != "true" {
			tgitlab.TearDown(ctx, t, topts)
		}
	}()

	assert.NilError(t, tgitlab.SetupSecondIdentity(ctx, topts))

	err = tgitlab.CreateCRD(ctx, topts)
	assert.NilError(t, err)

	// Fork project as second user
	forkProject, err := tgitlab.ForkGitLabProject(
		topts.SecondGLProvider.Client(),
		topts.ProjectID,
		os.Getenv("TEST_GITLAB_SECOND_GROUP"),
		false,
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

	// Grant first user access to fork so controller can read .tekton
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
		".tekton/pr.yaml": "testdata/pipelinerun.yaml",
	}, topts.TargetNS, topts.DefaultBranch, triggertype.PullRequest.String(), map[string]string{})
	assert.NilError(t, err)

	targetRefName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-fork-oktotest")
	forkCloneURL, err := scm.MakeGitCloneURL(forkProject.WebURL, topts.SecondOpts.UserName, topts.SecondOpts.Password)
	assert.NilError(t, err)

	_ = scm.PushFilesToRefGit(t, &scm.Opts{
		GitURL:        forkCloneURL,
		CommitTitle:   "Add fork ok-to-test fixtures - " + targetRefName,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        forkProject.WebURL,
		TargetRefName: targetRefName,
		BaseRefName:   topts.DefaultBranch,
	}, entries)

	// Create MR from fork to original project
	mrTitle := "TestGitlabSuccessStatusAfterOkToTest - " + targetRefName
	mr, _, err := topts.SecondGLProvider.Client().MergeRequests.CreateMergeRequest(forkProject.ID, &clientGitlab.CreateMergeRequestOptions{
		Title:           &mrTitle,
		SourceBranch:    &targetRefName,
		TargetBranch:    &topts.ProjectInfo.DefaultBranch,
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
	topts.ParamsRun.Clients.Log.Infof("Created fork MR %q with SHA %s", mr.WebURL, mr.SHA)

	// Post /ok-to-test as authorized user (first user / admin)
	topts.ParamsRun.Clients.Log.Infof("Posting /ok-to-test comment as authorized user on MR %d", mr.IID)
	_, _, err = topts.GLProvider.Client().Notes.CreateMergeRequestNote(topts.ProjectID, mr.IID,
		&clientGitlab.CreateMergeRequestNoteOptions{Body: clientGitlab.Ptr("/ok-to-test")})
	assert.NilError(t, err)

	// Wait for the pending/skipped status from the unauthorized fork MR
	topts.ParamsRun.Clients.Log.Infof("Waiting for pending status on fork MR from unauthorized user")
	sourceStatusCount, err := tgitlab.WaitForGitLabCommitStatusCount(ctx, topts.SecondGLProvider.Client(), topts.ParamsRun.Clients.Log, int(forkProject.ID), mr.SHA, "success", 2)
	assert.NilError(t, err)
	assert.Assert(t, sourceStatusCount >= 2, "expected 2 success commit status on fork, got %d", sourceStatusCount)
}
