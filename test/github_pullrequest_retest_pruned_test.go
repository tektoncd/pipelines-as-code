//go:build e2e

package test

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-github/v85/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// testRetestAfterPruning is the shared flow for both App and Webhook modes:
// create a PR with 2 pipelines (1 pass, 1 fail), wait for completion, delete
// all PipelineRuns to simulate pruning, post /retest, and assert that only the
// failed pipeline is re-run.
func testRetestAfterPruning(ctx context.Context, t *testing.T, g *tgithub.PRTest) {
	t.Helper()
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	sha := g.SHA
	labelSelector := fmt.Sprintf("%s=%s", keys.SHA, formatting.CleanValueKubernetes(sha))

	g.Cnx.Clients.Log.Infof("Waiting for 2 PipelineRuns to finish")
	pruns, err := twait.UntilPipelineRunsFinished(ctx, g.Cnx.Clients, twait.Opts{
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{sha},
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns), 2, "expected 2 initial PipelineRuns")

	g.Cnx.Clients.Log.Infof("Deleting all PipelineRuns to simulate pruning")
	err = g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).DeleteCollection(ctx,
		metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: labelSelector})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Waiting for PipelineRuns to be deleted")
	pollErr := kubeinteraction.PollImmediateWithContext(ctx, twait.DefaultTimeout, func() (bool, error) {
		pruns, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return false, err
		}
		return len(pruns.Items) == 0, nil
	})
	assert.NilError(t, pollErr, "PipelineRuns not fully deleted after polling")

	g.Cnx.Clients.Log.Infof("Posting /retest comment on PR %d", g.PRNumber)
	_, _, err = g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization, g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/retest")})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Waiting for retest PipelineRun to finish")
	_, err = twait.UntilPipelineRunsFinished(ctx, g.Cnx.Clients, twait.Opts{
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{sha},
	})
	assert.NilError(t, err)

	prunsAfterRetest, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	assert.NilError(t, err)
	assert.Equal(t, len(prunsAfterRetest.Items), 1,
		"expected exactly 1 new PipelineRun after /retest, but got %d", len(prunsAfterRetest.Items))
}

// TestGithubGHERetestAfterPipelineRunPruning verifies that /retest only re-runs
// failed pipelines when PipelineRun objects have been pruned from the cluster.
// It covers both GitHub App mode (Check Runs API) and direct webhook mode
// (webhook mode, Commit Statuses API).
func TestGithubGHERetestAfterPipelineRunPruning(t *testing.T) {
	tests := []struct {
		name      string
		isWebhook bool
	}{
		{
			name:      "Github GHE retest after pruning",
			isWebhook: false,
		},
		{
			name:      "Github GHE webhook retest after pruning",
			isWebhook: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testRetestAfterPruning(context.Background(), t, &tgithub.PRTest{
				Label: tt.name,
				YamlFiles: []string{
					"testdata/always-good-pipelinerun.yaml",
					"testdata/failures/pipelinerun-exit-1.yaml",
				},
				GHE:           true,
				Webhook:       tt.isWebhook,
				NoStatusCheck: true,
			})
		})
	}
}
