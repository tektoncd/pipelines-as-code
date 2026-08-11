package gitea

import (
	"context"
	"testing"

	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AssertConcurrencyRespected checks that the repository never ran more than
// limit PipelineRuns at once.
//
// Waiting for every PipelineRun to succeed, which is all the concurrency tests
// used to do, passes just as happily when the queue ignores the limit and runs
// them all at the same time. This is the assertion that tests the queue.
func AssertConcurrencyRespected(ctx context.Context, t *testing.T, topts *TestOpts, limit int) {
	t.Helper()
	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prs.Items) > 0, "no PipelineRun found in %s", topts.TargetNS)
	twait.AssertMaxConcurrency(t, prs.Items, limit)
	topts.ParamsRun.Clients.Log.Infof("concurrency limit of %d respected across %d PipelineRuns in %s",
		limit, len(prs.Items), topts.TargetNS)
}
