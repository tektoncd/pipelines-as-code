//go:build e2e

package test

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/google/go-github/v85/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
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
		&github.IssueComment{Body: github.Ptr("/test pipeline")})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Wait for the second repository update to be updated")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
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
		&github.IssueComment{Body: github.Ptr(triggerComment)})
	assert.NilError(t, err)
	sopt := twait.SuccessOpt{
		Title:           fmt.Sprintf("Testing %s with Github APPS integration on %s", g.Label, g.TargetNamespace),
		OnEvent:         opscomments.OnCommentEventType.String(),
		TargetNS:        g.TargetNamespace,
		NumberofPRMatch: 1,
	}
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)

	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
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
		"The event is %s", opscomments.OnCommentEventType.String())), "", 2, nil)
	assert.NilError(t, err)
}
