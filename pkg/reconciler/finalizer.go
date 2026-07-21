package reconciler

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"
)

func (r *Reconciler) FinalizeKind(ctx context.Context, pr *tektonv1.PipelineRun) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	state, exist := pr.GetAnnotations()[keys.State]
	if !exist || state == kubeinteraction.StateCompleted {
		return nil
	}

	if state == kubeinteraction.StateQueued || state == kubeinteraction.StateStarted {
		repoName, ok := pr.GetAnnotations()[keys.Repository]
		if !ok {
			return nil
		}
		repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
		// if repository is not found then remove the queue for that repository if exist
		if errors.IsNotFound(err) {
			r.qm.RemoveRepository(&v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: repoName, Namespace: pr.Namespace},
			})
			return nil
		}
		if err != nil {
			return err
		}

		// report the PipelineRun as cancelled as it was queued or started but it is deleted
		// but its status is still in progress or queued on git provider
		if err := r.reportPipelineRunAsCancelled(ctx, repo, pr); err != nil {
			logger.Errorf("failed to report deleted pipeline run as cancelled: %w", err)
		}

		if globalRepo, err := r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); err == nil && globalRepo != nil {
			if merged := copyRepositoryForMerge(repo); merged != nil {
				repo = merged
				repo.Spec.Merge(globalRepo.Spec)
			}
		}
		logger = logger.With("namespace", repo.Namespace)
		next := r.qm.RemoveAndTakeItemFromQueue(repo, pr)
		if next != "" {
			key := strings.Split(next, "/")
			pr, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(key[0]).Get(ctx, key[1], metav1.GetOptions{})
			if err != nil {
				return err
			}
			if err := r.
				updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
				logger.Errorf("failed to update status: %w", err)
				return err
			}
			return nil
		}
	}
	return nil
}

func (r *Reconciler) reportPipelineRunAsCancelled(ctx context.Context, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) error {
	logger := logging.FromContext(ctx)
	detectedProvider, event, err := r.initGitProviderClient(ctx, logger, repo, pr)
	if err != nil {
		return err
	}

	consoleURL := r.run.Clients.ConsoleUI().DetailURL(pr)
	status := status.StatusOpts{
		Conclusion:              "cancelled",
		Text:                    fmt.Sprintf("PipelineRun %s was deleted", pr.GetName()),
		DetailsURL:              consoleURL,
		PipelineRunName:         pr.GetName(),
		PipelineRun:             pr,
		OriginalPipelineRunName: pr.GetAnnotations()[keys.OriginalPRName],
	}

	if err := createStatusWithRetry(ctx, logger, detectedProvider, event, status); err != nil {
		return fmt.Errorf("failed to report cancelled status to provider: %w", err)
	}

	logger.Infof("updated cancelled status on provider platform for pipelineRun %s", pr.GetName())
	return nil
}
