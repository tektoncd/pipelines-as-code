package reconciler

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"
	"knative.dev/pkg/system"
)

func (r *Reconciler) FinalizeKind(ctx context.Context, pr *tektonv1.PipelineRun) pkgreconciler.Event {
	finalizeRun := *r.run
	reconciler := *r
	reconciler.run = &finalizeRun
	return reconciler.finalizeKind(ctx, pr)
}

func (r *Reconciler) finalizeKind(ctx context.Context, pr *tektonv1.PipelineRun) pkgreconciler.Event {
	logger := logging.FromContext(ctx)
	// Knative hands the reconciler a bare context, and reporting a cancellation
	// needs a provider client, which reads the trust policy from the controller
	// ConfigMap. Without the namespace that read targets "" and every deletion
	// of a running PipelineRun would silently stop reporting.
	ctx = info.StoreNS(ctx, system.Namespace())
	state, exist := pr.GetAnnotations()[keys.State]
	if !exist || state == kubeinteraction.StateCompleted {
		return nil
	}
	controllerInfo, err := controllerInfoForPipelineRun(pr, r.run.Info.Controller)
	if err != nil {
		return err
	}
	r.run.Info.Controller = controllerInfo

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
		// This releases the slot the deleted PipelineRun held and promotes the
		// next candidate, retrying past a malformed or already-gone queue key
		// instead of leaving it stuck in the running set forever with no real
		// PipelineRun ever completing to free it.
		r.startNextPipelineRunInQueue(ctx, logger, repo, pr)
		return nil
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
