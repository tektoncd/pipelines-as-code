//go:build e2e

package test

import (
	"context"
	"regexp"
	"testing"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
)

func TestGiteaConfigCancelInProgress(t *testing.T) {
	prmap := map[string]string{".tekton/pr.yaml": "testdata/pipelinerun-cancel-in-progress.yaml"}
	topts := &tgitea.TestOpts{
		TargetEvent:    triggertype.PullRequest.String(),
		YAMLFiles:      prmap,
		CheckForStatus: "",
		ExpectEvents:   false,
		Regexp:         nil,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	time.Sleep(3 * time.Second) // "Evil does not sleep. It waits." - Galadriel

	// wait a bit that the pipelinerun had created, then trigger a new run to test cancel-in-progress
	tgitea.PostCommentOnPullRequest(t, topts, "/test")

	time.Sleep(2 * time.Second) // "Evil does not sleep. It waits." - Galadriel

	targetRef := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("cancel-in-progress")
	entries, err := payload.GetEntries(prmap, topts.TargetNS, topts.DefaultBranch, topts.TargetEvent, map[string]string{})
	assert.NilError(t, err)
	topts.TargetRefName = topts.DefaultBranch
	scmOpts := &scm.Opts{
		GitURL:             topts.GitCloneURL,
		Log:                topts.ParamsRun.Clients.Log,
		WebURL:             topts.GitHTMLURL,
		TargetRefName:      targetRef,
		BaseRefName:        topts.DefaultBranch,
		NoCheckOutFromBase: true,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	pr, _, err := topts.GiteaCNX.Client().CreatePullRequest(topts.Opts.Organization, topts.Opts.Repo, forgejo.CreatePullRequestOption{
		Title: "Test Pull Request - " + targetRef,
		Head:  targetRef,
		Base:  topts.DefaultBranch,
	})
	assert.NilError(t, err)
	topts.PullRequest = pr
	topts.ParamsRun.Clients.Log.Infof("PullRequest %s has been created", pr.HTMLURL)
	topts.CheckForStatus = "success"
	tgitea.WaitForStatus(t, topts, "heads/"+targetRef, "", false)

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)

	sort.PipelineRunSortByStartTime(prs.Items)
	assert.Equal(t, len(prs.Items), 3, "should have 2 pipelineruns, but we have: %d", len(prs.Items))
	cancelledPr := 0
	for _, pr := range prs.Items {
		if pr.GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason() == "Cancelled" {
			cancelledPr++
		}
	}
	assert.Equal(t, cancelledPr, 1, "only one pr should have been cancelled")

	// Test that cancelling works with /retest - use specific PipelineRun name to bypass success check
	tgitea.PostCommentOnPullRequest(t, topts, "/retest pr-cancel-in-progress")
	topts.ParamsRun.Clients.Log.Info("Waiting 10 seconds before a new retest")
	time.Sleep(10 * time.Second) // "Evil does not sleep. It waits." - Galadriel
	tgitea.PostCommentOnPullRequest(t, topts, "/retest pr-cancel-in-progress")
	tgitea.WaitForStatus(t, topts, "heads/"+targetRef, "", false)

	for _, pr := range prs.Items {
		if pr.GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason() == "Cancelled" {
			cancelledPr++
		}
	}
	assert.Equal(t, cancelledPr, 2, "two pr should have been cancelled")
}

func TestGiteaConfigCancelInProgressAfterPRClosed(t *testing.T) {
	prmap := map[string]string{".tekton/pr.yaml": "testdata/pipelinerun-cancel-in-progress.yaml"}
	topts := &tgitea.TestOpts{
		TargetEvent:    triggertype.PullRequest.String(),
		YAMLFiles:      prmap,
		CheckForStatus: "",
		ExpectEvents:   false,
		Regexp:         nil,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	time.Sleep(3 * time.Second) // "Evil does not sleep. It waits." - Galadriel
	waitOpts := twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	}
	_, err := twait.UntilPipelineRunCreated(context.Background(), topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)

	closed := forgejo.StateClosed
	_, _, err = topts.GiteaCNX.Client().EditPullRequest(topts.Opts.Organization, topts.Opts.Repo, topts.PullRequest.Index, forgejo.EditPullRequestOption{
		State: &closed,
		Body:  &topts.PullRequest.Body,
	})
	assert.NilError(t, err)

	topts.ParamsRun.Clients.Log.Info("Waiting 10 seconds to check things has been cancelled")
	time.Sleep(10 * time.Second) // "Evil does not sleep. It waits." - Galadriel

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 1, "should have only one pipelinerun, but we have: %d", len(prs.Items))

	assert.Equal(t, prs.Items[0].GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason(), "Cancelled", "should have been cancelled")
}

func TestGiteaPullRequestPrivateRepository(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipeline.yaml": "testdata/pipelinerun_git_clone_private-gitea.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "success",
	}
	ctx, f := tgitea.TestPR(t, topts)
	defer f()
	reg := regexp.MustCompile(".*successfully fetched git-clone task from default configured catalog Hub")
	maxLines := int64(1000)
	err := twait.RegexpMatchingInControllerLog(ctx, topts.ParamsRun, *reg, 20, "controller", &maxLines, nil)
	assert.NilError(t, err)
	tgitea.WaitForSecretDeletion(t, topts, topts.TargetRefName)
}

func TestGiteaPRSkippedStatusReported(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:           triggertype.PullRequest.String(),
		NoPullRequestCreation: true,
		SkipEventsCheck:       true,
		Settings: &v1alpha1.Settings{
			StatusChecks: &v1alpha1.StatusChecks{
				Enabled: true,
				Mode:    v1alpha1.StatusCheckModePerPipelineRun,
			},
		},
	}
	ctx, f := tgitea.TestPR(t, topts)
	defer f()

	entries, err := payload.GetEntries(
		map[string]string{".tekton/pipelinerun-matching.yaml": "testdata/pipelinerun.yaml"},
		topts.TargetNS, topts.DefaultBranch, triggertype.PullRequest.String(), map[string]string{},
	)
	assert.NilError(t, err)

	// this is not going to match as it's targeting main branch on push event while we're gonna raise a pull request
	skipEntry, err := payload.GetEntries(
		map[string]string{".tekton/pipelinerun-skipped.yaml": "testdata/pipelinerun.yaml"},
		topts.TargetNS, topts.DefaultBranch, triggertype.Push.String(), map[string]string{},
	)
	assert.NilError(t, err)
	entries[".tekton/pipelinerun-skipped.yaml"] = skipEntry[".tekton/pipelinerun-skipped.yaml"]

	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.DefaultBranch,
	}
	topts.SHA = scm.PushFilesToRefGit(t, scmOpts, entries)

	pr, _, err := topts.GiteaCNX.Client().CreatePullRequest(topts.Opts.Organization, topts.Opts.Repo, forgejo.CreatePullRequestOption{
		Title: "Test Pull Request - " + topts.TargetRefName,
		Head:  topts.TargetRefName,
		Base:  topts.DefaultBranch,
	})
	assert.NilError(t, err)
	topts.PullRequest = pr
	topts.ParamsRun.Clients.Log.Infof("PullRequest %s has been created", pr.HTMLURL)

	sopt := twait.SuccessOpt{
		TargetNS:        topts.TargetNS,
		OnEvent:         triggertype.PullRequest.String(),
		NumberofPRMatch: 1,
		MinNumberStatus: 1,
	}
	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)

	statuses, _, err := topts.GiteaCNX.Client().ListStatuses(topts.Opts.Organization, topts.Opts.Repo, topts.SHA, forgejo.ListStatusesOption{})
	assert.NilError(t, err)

	foundStatus := false
	for _, cstatus := range statuses {
		if cstatus.State == forgejo.StatusSuccess && cstatus.Description == "Skipped" {
			foundStatus = true
			break
		}
	}
	assert.Equal(t, foundStatus, true, "should have found the skipped status for the non-matching pipeline run")
}
