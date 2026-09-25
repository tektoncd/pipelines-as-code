//go:build e2e

package test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v91/github"
	"gotest.tools/v3/assert"

	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	tstatus "github.com/openshift-pipelines/pipelines-as-code/test/pkg/status"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
)

// TestGithubGHEStatusStepBreakdown checks the step breakdown shown in the check
// run reported to GitHub. Two pipelines run from the same pull request, one
// which succeeds and one which fails in the middle, so we can check that every
// step is listed when all goes well and that the steps skipped after a failure
// are left out of the breakdown.
func TestGithubGHEStatusStepBreakdown(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label: "Github Step Breakdown",
		YamlFiles: []string{
			"testdata/pipelinerun-multiple-steps-success.yaml",
			"testdata/pipelinerun-multiple-steps-failure.yaml",
		},
		GHE: true,
		// one of the two pipelines is expected to fail.
		NoStatusCheck: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Waiting for the two pipelineruns of %s to finish", g.TargetNamespace)
	_, err := twait.UntilPipelineRunsFinished(ctx, g.Cnx.Clients, twait.Opts{
		Namespace:       g.TargetNamespace,
		MinNumberStatus: len(g.YamlFiles),
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{g.SHA},
	})
	assert.NilError(t, err)

	checkRuns := waitForCompletedCheckRuns(ctx, t, g, len(g.YamlFiles))

	var checkedSuccess, checkedFailure bool
	for _, checkRun := range checkRuns {
		text := checkRun.GetOutput().GetText()
		switch {
		case strings.Contains(checkRun.GetName(), "pipelinerun-multiple-steps-success"):
			checkedSuccess = true
			assert.Equal(t, checkRun.GetConclusion(), "success", "check run %s has not succeeded", checkRun.GetName())
			tstatus.AssertStepBreakdown(t, text,
				[]string{"alpha", "Bravo has a display name", "charlie", "delta", "echo"},
				nil)
		case strings.Contains(checkRun.GetName(), "pipelinerun-multiple-steps-failure"):
			checkedFailure = true
			assert.Equal(t, checkRun.GetConclusion(), "failure", "check run %s has not failed", checkRun.GetName())
			tstatus.AssertStepBreakdown(t, text,
				[]string{"alpha", "Bravo has a display name", "charlie"},
				[]string{"delta", "echo"})
		default:
			t.Logf("ignoring unrelated check run %s", checkRun.GetName())
		}
	}
	assert.Assert(t, checkedSuccess, "no check run for the pipeline which succeeds")
	assert.Assert(t, checkedFailure, "no check run for the pipeline which fails")
}

// waitForCompletedCheckRuns waits until the wanted number of completed check
// runs carrying a status report have been reported on the commit of the pull
// request.
func waitForCompletedCheckRuns(ctx context.Context, t *testing.T, g *tgithub.PRTest, want int) []*github.CheckRun {
	t.Helper()
	maxLoop := 30
	for i := range maxLoop {
		res, resp, err := g.Provider.Client().Checks.ListCheckRunsForRef(ctx, g.Options.Organization, g.Options.Repo, g.SHA,
			&github.ListCheckRunsOptions{
				AppID:  g.Provider.ApplicationID,
				Status: new("completed"),
			})
		assert.NilError(t, err)
		assert.Equal(t, resp.StatusCode, 200)

		checkRuns := []*github.CheckRun{}
		for _, checkRun := range res.CheckRuns {
			if checkRun.GetOutput().GetText() != "" {
				checkRuns = append(checkRuns, checkRun)
			}
		}
		if len(checkRuns) >= want {
			return checkRuns
		}
		g.Cnx.Clients.Log.Infof("Waiting for %d completed check runs, got %d (try %d/%d)", want, len(checkRuns), i+1, maxLoop)
		time.Sleep(10 * time.Second)
	}
	t.Fatalf("only got less than %d completed check runs on %s", want, g.SHA)
	return nil
}
