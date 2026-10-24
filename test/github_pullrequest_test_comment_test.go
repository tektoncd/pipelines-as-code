package test

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/google/go-github/v91/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	cctx "github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/configmap"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

func TestGithubGHEPullRequestTest(t *testing.T) {
	ctx := context.TODO()
	g := &tgithub.PRTest{
		Label:     "Github test implicit comment",
		YamlFiles: []string{"testdata/pipelinerun.yaml", "testdata/pipelinerun-clone.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Creating /test in PullRequest")
	_, _, err := g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		github.IssueCommentRequest{Body: "/test pipeline"})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Waiting for PipelineRun to succeed")
	waitOpts := twait.Opts{
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{g.SHA},
	}
	prs, err := twait.UntilPipelineRunHasReason(ctx, g.Cnx.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)
	g.Cnx.Clients.Log.Infof("Check if we have the pipelinerun set as succeeded")
	cond := prs[len(prs)-1].Status.GetCondition(apis.ConditionSucceeded)
	assert.Assert(t, cond != nil)
	assert.Equal(t, corev1.ConditionTrue, cond.Status)
}

func TestGithubGHEPullRequestTestEditedComment(t *testing.T) {
	ctx := context.TODO()
	g := &tgithub.PRTest{
		Label:     "Github test edited comment",
		YamlFiles: []string{"testdata/pipelinerun.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	patchData := map[string]string{
		"support-edited-comments": "true",
	}

	ctx, err := cctx.GetControllerCtxInfo(ctx, g.Cnx)
	assert.NilError(t, err)

	configMapTearDown := configmap.ChangeGlobalConfig(ctx, t, g.Cnx, tgithub.ControllerConfigMapName(g.GHE), patchData)
	defer configMapTearDown()

	g.Cnx.Clients.Log.Infof("Creating wrong comment /tost in PullRequest")
	comment, _, err := g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		github.IssueCommentRequest{Body: "/tost"}) // deliberately wrong to test edited comment
	assert.NilError(t, err)

	for i := 0; i < 10; i++ {
		plrs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", keys.EventType, opscomments.NoOpsCommentEventType.String()),
		})
		assert.NilError(t, err)
		assert.Equal(t, len(plrs.Items), 0, "no pipelinerun should be created but got %d", len(plrs.Items))
		time.Sleep(1 * time.Second)
		g.Cnx.Clients.Log.Infof("Waiting if no PipelineRun is created")
	}

	g.Cnx.Clients.Log.Infof("Editing comment %d", comment.GetID())
	_, _, err = g.Provider.Client().Issues.UpdateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, comment.GetID(),
		github.IssueCommentRequest{Body: "/test"})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Waiting for PipelineRun to succeed")
	waitOpts := twait.Opts{
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{g.SHA},
	}
	prs, err := twait.UntilPipelineRunHasReason(ctx, g.Cnx.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)
	g.Cnx.Clients.Log.Infof("Check if we have the pipelinerun set as succeeded")
	cond := prs[len(prs)-1].Status.GetCondition(apis.ConditionSucceeded)
	assert.Assert(t, cond != nil)
	assert.Equal(t, corev1.ConditionTrue, cond.Status)
}

func TestGithubGHEPullRequestOnCommentEditedComment(t *testing.T) {
	ctx := context.TODO()
	g := &tgithub.PRTest{
		Label:         "Github test on comment edited comment",
		YamlFiles:     []string{"testdata/pipelinerun-on-comment-annotation.yaml"},
		GHE:           true,
		NoStatusCheck: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	patchData := map[string]string{
		"support-edited-comments": "true",
	}

	ctx, err := cctx.GetControllerCtxInfo(ctx, g.Cnx)
	assert.NilError(t, err)

	configMapTearDown := configmap.ChangeGlobalConfig(ctx, t, g.Cnx, "ghe-configmap", patchData)
	defer configMapTearDown()

	g.Cnx.Clients.Log.Infof("Creating wrong comment /bye-world in PullRequest")
	comment, _, err := g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		github.IssueCommentRequest{Body: "/bye-world"}) // deliberately wrong to test edited comment
	assert.NilError(t, err)

	for i := 0; i < 10; i++ {
		plrs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
		})
		assert.NilError(t, err)
		assert.Equal(t, len(plrs.Items), 0, "no pipelinerun should be created but got %d", len(plrs.Items))
		time.Sleep(1 * time.Second)
		g.Cnx.Clients.Log.Infof("Waiting if no PipelineRun is created")
	}

	g.Cnx.Clients.Log.Infof("Editing comment %d", comment.GetID())
	_, _, err = g.Provider.Client().Issues.UpdateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, comment.GetID(),
		github.IssueCommentRequest{Body: "/hello-world"})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Waiting for PipelineRun to succeed")
	waitOpts := twait.Opts{
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{g.SHA},
	}
	prs, err := twait.UntilPipelineRunHasReason(ctx, g.Cnx.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)
	g.Cnx.Clients.Log.Infof("Check if we have the pipelinerun set as succeeded")
	cond := prs[len(prs)-1].Status.GetCondition(apis.ConditionSucceeded)
	assert.Assert(t, cond != nil)
	assert.Equal(t, corev1.ConditionTrue, cond.Status)
}

func TestGithubGHEOnCommentAnnotation(t *testing.T) {
	g := &tgithub.PRTest{
		Label:         "Github test implicit comment",
		YamlFiles:     []string{"testdata/pipelinerun-on-comment-annotation.yaml"},
		GHE:           true,
		NoStatusCheck: true,
	}
	ctx := context.Background()
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	triggerComment := "/hello-world"

	g.Cnx.Clients.Log.Infof("Creating %s custom comment on PullRequest", triggerComment)
	_, _, err := g.Provider.Client().Issues.CreateComment(ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
		github.IssueCommentRequest{Body: triggerComment})
	assert.NilError(t, err)
	sopt := twait.SuccessOpt{
		Title:           fmt.Sprintf("Testing %s with Github APPS integration on %s", g.Label, g.TargetNamespace),
		OnEvent:         opscomments.OnCommentEventType.String(),
		TargetNS:        g.TargetNamespace,
		NumberofPRMatch: 1,
	}
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)

	waitOpts := twait.Opts{
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{g.SHA},
	}
	prs, err := twait.UntilPipelineRunHasReason(ctx, g.Cnx.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)
	g.Cnx.Clients.Log.Infof("Check if we have the pipelinerun set as succeeded")
	lastPR := prs[len(prs)-1]
	cond := lastPR.Status.GetCondition(apis.ConditionSucceeded)
	assert.Assert(t, cond != nil)
	assert.Equal(t, corev1.ConditionTrue, cond.Status)
	assert.Equal(t, lastPR.Annotations[keys.EventType], opscomments.OnCommentEventType.String())

	err = twait.RegexpMatchingInPodLog(context.Background(), g.Cnx, g.TargetNamespace, fmt.Sprintf("tekton.dev/pipelineRun=%s", lastPR.Name), "step-task", *regexp.MustCompile(triggerComment), "", 2, nil)
	assert.NilError(t, err)

	err = twait.RegexpMatchingInPodLog(context.Background(), g.Cnx, g.TargetNamespace, fmt.Sprintf("tekton.dev/pipelineRun=%s", lastPR.Name), "step-task", *regexp.MustCompile(fmt.Sprintf(
		"The event is %s", opscomments.OnCommentEventType.String(),
	)), "", 2, nil)
	assert.NilError(t, err)
}
