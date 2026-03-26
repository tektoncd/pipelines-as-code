//go:build e2e

package test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-github/v81/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestGithubPullRequestCustomGitOpsCommandPrefix(t *testing.T) {
	ctx := context.Background()
	customPrefix := "pac"

	g := &tgithub.PRTest{
		Label:     "Github test custom GitOps command prefix",
		YamlFiles: []string{"testdata/pipelinerun-gitops.yaml"},
		Options: options.E2E{
			Settings: &v1alpha1.Settings{
				GitOpsCommandPrefix: customPrefix,
			},
		},
	}

	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	customTestComment := fmt.Sprintf("/%s test", customPrefix)
	g.Cnx.Clients.Log.Infof("Creating %s comment on PullRequest", customTestComment)
	_, _, err := g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr(customTestComment)})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Wait for repository to be updated with custom prefix command")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	repo, err := twait.UntilRepositoryUpdated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Check if repository status shows succeeded")
	assert.Equal(t, corev1.ConditionTrue, repo.Status[len(repo.Status)-1].Conditions[0].Status)

	customTestComment = fmt.Sprintf("/%s test pr-gitops-comment", customPrefix)
	g.Cnx.Clients.Log.Infof("Creating %s comment on PullRequest", customTestComment)
	_, _, err = g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr(customTestComment)})
	assert.NilError(t, err)

	twait.Succeeded(ctx, t, g.Cnx, g.Options, twait.SuccessOpt{
		TargetNS:        g.TargetNamespace,
		OnEvent:         opscomments.TestSingleCommentEventType.String(),
		NumberofPRMatch: 3,
		MinNumberStatus: 3,
		SHA:             g.SHA,
	})
}

func TestGithubPullRequestCustomPrefixCancel(t *testing.T) {
	ctx := context.Background()
	customPrefix := "pac"

	g := &tgithub.PRTest{
		Label:     "Github test custom prefix cancel",
		YamlFiles: []string{"testdata/pipelinerun-gitops.yaml"},
		Options: options.E2E{
			Settings: &v1alpha1.Settings{
				GitOpsCommandPrefix: customPrefix,
			},
		},
		NoStatusCheck: true,
	}

	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	err := twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)
	// Cancel with custom prefix
	customCancelComment := fmt.Sprintf("/%s cancel", customPrefix)
	g.Cnx.Clients.Log.Infof("Creating %s comment on PullRequest", customCancelComment)
	_, _, err = g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr(customCancelComment)})
	assert.NilError(t, err)

	err = twait.UntilPipelineRunHasReason(ctx, g.Cnx.Clients, tektonv1.PipelineRunReasonCancelled, waitOpts)
	assert.NilError(t, err)
}
