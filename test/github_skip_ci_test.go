//go:build e2e

package test

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// verifySkipCI is a helper function that verifies no PipelineRuns were created
// and that controller logs mention the skip command detection for a given event type.
func verifySkipCI(ctx context.Context, t *testing.T, g *tgithub.PRTest, eventType string) {
	t.Helper()

	// Wait a bit to ensure no PipelineRun is created
	time.Sleep(10 * time.Second)

	// Verify that NO PipelineRuns were created due to [skip ci]
	pruns, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, len(pruns.Items), "Expected no PipelineRuns to be created due to [skip ci] in commit message")

	// Setup context with controller namespace info before checking logs
	ctx, err = cctx.GetControllerCtxInfo(ctx, g.Cnx)
	assert.NilError(t, err)

	// Verify controller logs mention skip command detection
	controllerName := "controller"
	if g.GHE {
		controllerName = "ghe-controller"
	}
	numLines := int64(1000)
	skipLogRegex := regexp.MustCompile(fmt.Sprintf("CI skipped for %s event.*contains skip command in message", eventType))
	err = twait.RegexpMatchingInControllerLog(ctx, g.Cnx, *skipLogRegex, 10, controllerName, &numLines, nil)
	assert.NilError(t, err, "Expected controller logs to mention CI skip due to skip command")

	g.Cnx.Clients.Log.Infof("✓ Verified controller logs mention skip command detection")
}

// TestGithubSkipCIPullRequest tests that [skip ci] in commit message prevents
// PipelineRun execution on pull requests.
func TestGithubGHESkipCIPullRequest(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github Skip CI Pull Request [skip ci]",
		YamlFiles:     []string{"testdata/pipelinerun.yaml"},
		NoStatusCheck: true, // Don't wait for success since we expect no PipelineRun
		GHE:           true,
	}

	// The CommitTitle will be used as the commit message, so [skip ci] is included
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	verifySkipCI(ctx, t, g, "pull request")
}

// TestGithubSkipCIPush tests that [skip ci] in commit message prevents
// PipelineRun execution on push events.
func TestGithubGHESkipCIPush(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github Skip CI Push [skip ci]",
		YamlFiles:     []string{"testdata/pipelinerun-on-push.yaml"},
		NoStatusCheck: true, // Don't wait for success since we expect no PipelineRun
		GHE:           true,
	}

	g.RunPushRequest(ctx, t)
	defer g.TearDown(ctx, t)

	verifySkipCI(ctx, t, g, "push")
}

// TestGithubSkipCITestCommand tests that /test command can override [skip tkn].
func TestGithubGHESkipCITestCommand(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github Skip CI Test Command [skip tkn]",
		YamlFiles:     []string{"testdata/pipelinerun.yaml"},
		NoStatusCheck: true,
		GHE:           true,
	}

	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	// Wait a bit to ensure no PipelineRun is created
	time.Sleep(10 * time.Second)

	// Verify no PipelineRuns initially
	pruns, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, 0, len(pruns.Items), "Expected no PipelineRuns before /test command")

	// Setup context with controller namespace info before checking logs
	ctx, err = cctx.GetControllerCtxInfo(ctx, g.Cnx)
	assert.NilError(t, err)

	// Verify controller logs mention skip command detection
	numLines := int64(1000)
	skipLogRegex := regexp.MustCompile("CI skipped for pull request event.*contains skip command in message")
	err = twait.RegexpMatchingInControllerLog(ctx, g.Cnx, *skipLogRegex, 10, "ghe-controller", &numLines, nil)
	assert.NilError(t, err, "Expected controller logs to mention CI skip due to skip command")

	g.Cnx.Clients.Log.Infof("✓ Verified controller logs mention skip command detection")

	// Post a /test comment which should override the skip command
	g.Cnx.Clients.Log.Infof("Posting /test comment to override [skip tkn]")
	_, _, err = g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo,
		g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/test")})
	assert.NilError(t, err)

	// Wait for PipelineRun to be created
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	err = twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	// Verify PipelineRun was created
	pruns, err = g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Assert(t, len(pruns.Items) >= 1, "Expected at least one PipelineRun via /test")

	g.Cnx.Clients.Log.Infof("✓ Verified that /test override [skip tkn] and created %d PipelineRun(s)", len(pruns.Items))
}
