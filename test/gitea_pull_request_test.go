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
