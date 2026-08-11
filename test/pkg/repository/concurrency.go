package repository

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// SetConcurrencyLimit changes the concurrency limit of a Repository, or removes
// it entirely when limit is nil. Nothing else about the Repository is touched,
// so a test can be sure that any queue movement it observes afterwards was
// caused by the limit change and not by an unrelated event.
func SetConcurrencyLimit(ctx context.Context, t *testing.T, runcnx *params.Run, ns, name string, limit *int) {
	t.Helper()
	client := runcnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(ns)
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		repo, err := client.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		repo.Spec.ConcurrencyLimit = limit
		_, err = client.Update(ctx, repo, metav1.UpdateOptions{})
		return err
	})
	assert.NilError(t, err)
	if limit == nil {
		runcnx.Clients.Log.Infof("removed concurrency limit from repository %s/%s", ns, name)
		return
	}
	runcnx.Clients.Log.Infof("set concurrency limit of repository %s/%s to %d", ns, name, *limit)
}
