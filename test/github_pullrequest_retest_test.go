//go:build e2e

package test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/go-github/v81/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestGithubGHEPullRequestGitopsCommentRetest will test the retest
// functionality of a GitHub pull request.
func TestGithubGHEPullRequestGitopsCommentRetest(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github retest comment",
		YamlFiles:     []string{"testdata/pipelinerun.yaml"},
		GHE:           true,
		NoStatusCheck: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Creating /retest in PullRequest")
	_, _, err := g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/retest")})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Wait for the second repository update to be updated")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	_, err = twait.UntilRepositoryUpdated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Check if we have the repository set as succeeded")
	repo, err := g.Cnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(g.TargetNamespace).Get(ctx, g.TargetNamespace, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, repo.Status[len(repo.Status)-1].Conditions[0].Status, corev1.ConditionTrue)
}

// TestGithubGHEPullRequestRetest tests the retest functionality of a GitHub pull request.
// It sets up a pull request, triggers a retest comment, waits for the repository to be updated,
// and verifies that the repository status is set to succeeded and the correct number of PipelineRuns are created.
func TestGithubGHEPullRequestGitopsCommentCancel(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github PullRequest Cancel",
		YamlFiles: []string{"testdata/pipelinerun.yaml", "testdata/pipelinerun-gitops.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	pruns, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), 2)

	g.Cnx.Clients.Log.Info("/test pr-gitops-comment on Pull Request before canceling")
	_, _, err = g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo,
		g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/test pr-gitops-comment")},
	)
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 3,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	assert.NilError(t, err)
	err = twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("/cancel pr-gitops-comment on Pull Request")
	_, _, err = g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/cancel pr-gitops-comment")})
	assert.NilError(t, err)

	waitOpts = twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 3,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	g.Cnx.Clients.Log.Info("Waiting for Repository to be updated")
	_, err = twait.UntilRepositoryUpdated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Check if we have the repository set as succeeded")
	repo, err := g.Cnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(g.TargetNamespace).Get(ctx, g.TargetNamespace, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, repo.Status[len(repo.Status)-1].Conditions[0].Status, corev1.ConditionFalse)

	pruns, err = g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), 3)

	// go over all pruns check that at least one is cancelled and the other two are succeeded
	cancelledCount := 0
	succeededCount := 0
	unknownCount := 0
	for _, prun := range pruns.Items {
		for _, condition := range prun.Status.Conditions {
			if condition.Type == "Succeeded" {
				switch condition.Status {
				case corev1.ConditionFalse:
					cancelledCount++
				case corev1.ConditionTrue:
					succeededCount++
				case corev1.ConditionUnknown:
					unknownCount++
				}
			}
		}
	}
	assert.Equal(t, cancelledCount, 1, "should have one cancelled PipelineRun")
	assert.Equal(t, succeededCount, 2, "should have two succeeded PipelineRuns")
	assert.Equal(t, unknownCount, 0, "should have zero unknown PipelineRuns: %+v", pruns.Items)
}

func TestGithubGHERetestWithMultipleFailedPipelineRuns(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label: "Github Retest with multiple failed PipelineRuns",
		YamlFiles: []string{
			"testdata/pipelinerun-tekton-validation.yaml",
			"testdata/failures/pipelinerun-exit-1.yaml", // failed pipelinerun to be re-trigger after retest
		},
		NoStatusCheck: true,
		GHE:           true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	err := twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		TargetSHA:       g.SHA,
		PollTimeout:     twait.DefaultTimeout,
	})
	assert.NilError(t, err)

	pruns, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), 1)

	_, _, err = g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo,
		g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/retest")},
	)
	assert.NilError(t, err)

	// here we only need to check that we have two failed check runs and nothing is gone
	// after making the retest comment.
	res, _, err := g.Provider.Client().Checks.ListCheckRunsForRef(ctx,
		g.Options.Organization,
		g.Options.Repo,
		g.SHA,
		&github.ListCheckRunsOptions{},
	)
	assert.NilError(t, err)

	assert.Equal(t, len(res.CheckRuns), 2)

	containsFailedPLRName := false
	for _, checkRun := range res.CheckRuns {
		// check if the check run is for the validation failed pipelinerun
		if strings.Contains(checkRun.GetExternalID(), "pipelinerun-tekton-validation") {
			containsFailedPLRName = true
		}
	}
	assert.Equal(t, containsFailedPLRName, true)
}
