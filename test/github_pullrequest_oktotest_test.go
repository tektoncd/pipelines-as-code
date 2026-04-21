//go:build e2e

package test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGithubGHEPullRequestOkToTest(t *testing.T) {
	ctx := context.TODO()
	g := &tgithub.PRTest{
		Label:     "Github OkToTest comment",
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

	runevent := info.Event{
		DefaultBranch: repoinfo.GetDefaultBranch(),
		Organization:  g.Options.Organization,
		Repository:    g.Options.Repo,
		URL:           repoinfo.GetHTMLURL(),
	}

	repo, err := g.Cnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(g.TargetNamespace).Get(ctx, g.TargetNamespace, metav1.GetOptions{})
	assert.NilError(t, err)
	initialStatusCount := len(repo.Status)

	pruns, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	initialPipelineRunCount := len(pruns.Items)

	installID, err := strconv.ParseInt(os.Getenv("TEST_GITHUB_SECOND_REPO_INSTALLATION_ID"), 10, 64)
	assert.NilError(t, err)

	sendIssueComment := func(t *testing.T, sender string) {
		t.Helper()

		event := github.IssueCommentEvent{
			Comment: &github.IssueComment{
				Body: github.Ptr(`/ok-to-test`),
			},
			Installation: &github.Installation{
				ID: &installID,
			},
			Action: github.Ptr("created"),
			Issue: &github.Issue{
				State: github.Ptr("open"),
				PullRequestLinks: &github.PullRequestLinks{
					HTMLURL: github.Ptr(fmt.Sprintf("%s/pull/%d", runevent.URL, g.PRNumber)),
				},
				Number: github.Ptr(g.PRNumber),
			},
			Repo: &github.Repository{
				DefaultBranch: &runevent.DefaultBranch,
				HTMLURL:       &runevent.URL,
				Name:          &runevent.Repository,
				Owner:         &github.User{Login: &runevent.Organization},
			},
			Sender: &github.User{
				Login: github.Ptr(sender),
			},
		}

		err = payload.Send(ctx,
			g.Cnx,
			os.Getenv("TEST_GITHUB_SECOND_EL_URL"),
			os.Getenv("TEST_GITHUB_SECOND_WEBHOOK_SECRET"),
			os.Getenv("TEST_GITHUB_SECOND_API_URL"),
			os.Getenv("TEST_GITHUB_SECOND_REPO_INSTALLATION_ID"),
			event,
			"issue_comment",
		)
		assert.NilError(t, err)
	}

	g.Cnx.Clients.Log.Infof("Sending /ok-to-test from untrusted sender on same-repo pull request")
	sendIssueComment(t, "nonowner")

	time.Sleep(10 * time.Second)

	pruns, err = g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, initialPipelineRunCount, len(pruns.Items), "untrusted issue_comment must not create a new PipelineRun")

	repo, err = g.Cnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(g.TargetNamespace).Get(ctx, g.TargetNamespace, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, initialStatusCount, len(repo.Status), "untrusted issue_comment must not add a new Repository status")

	ctx, err = cctx.GetControllerCtxInfo(ctx, g.Cnx)
	assert.NilError(t, err)
	numLines := int64(1000)
	logRegex := regexp.MustCompile(`Skipping same-repo pull request shortcut for untrusted event \*github\.IssueCommentEvent`)
	err = twait.RegexpMatchingInControllerLog(ctx, g.Cnx, *logRegex, 10, "ghe-controller", &numLines, nil)
	assert.NilError(t, err)
}
