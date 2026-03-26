//go:build e2e

package test

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"testing"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestGithubPullRerequest is a test that will create a pull request and check
// if we can rerequest a specific check or the full check suite.
func TestGithubGHEPullRerequest(t *testing.T) {
	ctx := context.TODO()
	g := &tgithub.PRTest{
		Label:     "Github Rerequest",
		YamlFiles: []string{"testdata/pipelinerun.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	repoinfo, resp, err := g.Provider.Client().Repositories.Get(ctx, g.Options.Organization, g.Options.Repo)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Errorf("Repository %s not found in %s", g.Options.Organization, g.Options.Repo)
	}

	runinfo := info.Event{
		DefaultBranch: repoinfo.GetDefaultBranch(),
		HeadBranch:    g.TargetRefName,
		Organization:  g.Options.Organization,
		Repository:    g.Options.Repo,
		URL:           repoinfo.GetHTMLURL(),
		SHA:           g.SHA,
		Sender:        g.Options.Organization,
	}

	installID, err := strconv.ParseInt(os.Getenv("TEST_GITHUB_SECOND_REPO_INSTALLATION_ID"), 10, 64)
	assert.NilError(t, err)
	event := github.CheckRunEvent{
		Action: github.Ptr("rerequested"),
		Installation: &github.Installation{
			ID: &installID,
		},
		CheckRun: &github.CheckRun{
			CheckSuite: &github.CheckSuite{
				HeadBranch: &runinfo.HeadBranch,
				HeadSHA:    &runinfo.SHA,
				PullRequests: []*github.PullRequest{
					{
						Number: github.Ptr(g.PRNumber),
					},
				},
			},
		},
		Repo: &github.Repository{
			DefaultBranch: &runinfo.DefaultBranch,
			HTMLURL:       &runinfo.URL,
			Name:          &runinfo.Repository,
			Owner:         &github.User{Login: &runinfo.Organization},
		},
		Sender: &github.User{
			Login: &runinfo.Sender,
		},
	}

	err = payload.Send(ctx,
		g.Cnx,
		os.Getenv("TEST_GITHUB_SECOND_EL_URL"),
		os.Getenv("TEST_GITHUB_SECOND_WEBHOOK_SECRET"),
		os.Getenv("TEST_GITHUB_SECOND_API_URL"),
		os.Getenv("TEST_GITHUB_SECOND_REPO_INSTALLATION_ID"),
		event,
		"check_run",
	)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Wait for the second repository update to be updated")
	_, err = twait.UntilRepositoryUpdated(ctx, g.Cnx.Clients, twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Check if we have the repository set as succeeded")
	repo, err := g.Cnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(g.TargetNamespace).Get(ctx, g.TargetNamespace, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Assert(t, repo.Status[len(repo.Status)-1].Conditions[0].Status == corev1.ConditionTrue)
	csEvent := github.CheckSuiteEvent{
		Action: github.Ptr("rerequested"),
		Installation: &github.Installation{
			ID: &installID,
		},
		CheckSuite: &github.CheckSuite{
			HeadBranch: &runinfo.HeadBranch,
			HeadSHA:    &runinfo.SHA,
			PullRequests: []*github.PullRequest{
				{
					Number: github.Ptr(g.PRNumber),
				},
			},
		},
		Repo: &github.Repository{
			DefaultBranch: &runinfo.DefaultBranch,
			HTMLURL:       &runinfo.URL,
			Name:          &runinfo.Repository,
			Owner:         &github.User{Login: &runinfo.Organization},
		},
		Sender: &github.User{
			Login: &runinfo.Sender,
		},
	}

	err = payload.Send(ctx,
		g.Cnx,
		os.Getenv("TEST_GITHUB_SECOND_EL_URL"),
		os.Getenv("TEST_GITHUB_SECOND_WEBHOOK_SECRET"),
		os.Getenv("TEST_GITHUB_SECOND_API_URL"),
		os.Getenv("TEST_GITHUB_SECOND_APPLICATION_ID"),
		csEvent,
		"check_suite",
	)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Wait for the second repository update to be updated")
	_, err = twait.UntilRepositoryUpdated(ctx, g.Cnx.Clients, twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	})
	assert.NilError(t, err)
}
