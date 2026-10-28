package repository

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/random"
	"gotest.tools/v3/assert"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NSTearDown(ctx context.Context, t *testing.T, runcnx *params.Run, targetNS string) {
	repos, err := runcnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(targetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)

	// Add random suffix to the repo URL so we can keep it for logs collection (if PAC_E2E_KEEP_NS is set)
	// so we can investigate later if needed and capture its status it captured.
	// We need to rename it so webhooks don't trigger any more runs and
	// the admission webhook does not block creation of a new repository with the same URL.
	for _, repo := range repos.Items {
		if err := renameRepoURLWithRetry(ctx, runcnx, targetNS, repo.Name); err != nil {
			runcnx.Clients.Log.Warnf("Failed to rename Repository URL %s after retries: %v", repo.Name, err)
		}
	}

	if os.Getenv("PAC_E2E_KEEP_NS") != "true" {
		runcnx.Clients.Log.Infof("Deleting NS %s", targetNS)
		err = runcnx.Clients.Kube.CoreV1().Namespaces().Delete(ctx, targetNS, metav1.DeleteOptions{})
		assert.NilError(t, err)
	}
}

// renameRepoURLWithRetry renames the repository URL with a random suffix,
// retrying on optimistic lock conflicts (the resource version may change
// between the initial List and the Update if the controller updates status).
func renameRepoURLWithRetry(ctx context.Context, runcnx *params.Run, targetNS, repoName string) error {
	const maxRetries = 3
	pacClient := runcnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(targetNS)
	for attempt := 0; attempt < maxRetries; attempt++ {
		repo, err := pacClient.Get(ctx, repoName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get repository %s: %w", repoName, err)
		}
		repo.Spec.URL += random.AlphaString(5)
		if _, err = pacClient.Update(ctx, repo, metav1.UpdateOptions{}); err != nil {
			if errors.IsConflict(err) {
				runcnx.Clients.Log.Infof("Conflict renaming Repository URL %s, retrying (%d/%d)", repoName, attempt+1, maxRetries)
				time.Sleep(time.Duration(attempt+1) * time.Second)
				continue
			}
			return fmt.Errorf("failed to update repository %s: %w", repoName, err)
		}
		return nil
	}
	return fmt.Errorf("failed to rename Repository URL %s after %d retries due to conflicts", repoName, maxRetries)
}
