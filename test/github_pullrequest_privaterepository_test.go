//go:build e2e

package test

import (
	"context"
	"regexp"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
)

func TestGithubGHEPullRequestGitCloneTask(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github GHE - Private Repo with git-clone task",
		YamlFiles: []string{"testdata/pipelinerun_git_clone_private.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)

	ctx, err := cctx.GetControllerCtxInfo(ctx, g.Cnx)
	assert.NilError(t, err)

	lines := int64(1000)
	assert.NilError(t, wait.RegexpMatchingInControllerLog(ctx, g.Cnx, *regexp.MustCompile(".*fetched git-clone task"),
		10, "ghe-controller", &lines, nil), "Error while checking the logs of the pipelines-as-code controller pod")
	defer g.TearDown(ctx, t)
}

func TestGithubGHEPullRequestGitClone(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github GHE - Private Repo",
		YamlFiles: []string{"testdata/pipelinerun_git_clone_private.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubGHEWebhookPullRequestPrivateRepository(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github GHE Rerequest",
		YamlFiles: []string{"testdata/pipelinerun_git_clone_private.yaml"},
		GHE:       true,
		Webhook:   true,
	}
	defer g.TearDown(ctx, t)
	g.RunPullRequest(ctx, t)
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -info TestPullRequestPrivateRepository$ ."
// End:
