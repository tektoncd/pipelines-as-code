package reconciler

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacAPIv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *Reconciler) queuePipelineRun(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) error {
	order, exist := pr.GetAnnotations()[keys.ExecutionOrder]
	if !exist {
		// if the pipelineRun doesn't have order label then wait
		return nil
	}

	// check if annotation exist
	repoName, exist := pr.GetAnnotations()[keys.Repository]
	if !exist {
		return fmt.Errorf("no %s annotation found", keys.Repository)
	}
	if repoName == "" {
		return fmt.Errorf("annotation %s is empty", keys.Repository)
	}
	repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
	if err != nil {
		// if repository is not found, then skip processing the pipelineRun and return nil
		if errors.IsNotFound(err) {
			r.qm.RemoveRepository(&pacAPIv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      repoName,
					Namespace: pr.Namespace,
				},
			})
			return nil
		}
		return fmt.Errorf("error getting PipelineRun: %w", err)
	}

	// merge local repo with global repo here in order to derive settings from global
	// for further concurrency and other operations.
	if globalRepo, err := r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); err == nil && globalRepo != nil {
		logger.Info("Merging global repository settings with local repository settings")
		if merged := copyRepositoryForMerge(repo); merged != nil {
			repo = merged
			repo.Spec.Merge(globalRepo.Spec)
		}
	}

	// if concurrency was set and later removed or changed to zero
	// then remove pipelineRun from Queue and update pending state to running
	if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit == 0 {
		_ = r.qm.RemoveAndTakeItemFromQueue(repo, pr)
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			return fmt.Errorf("failed to update PipelineRun to in_progress: %w", err)
		}
		return nil
	}

	var processed bool
	var itered int
	maxIterations := 5

	orderedList := queuepkg.FilterPipelineRunByState(ctx, r.run.Clients.Tekton, strings.Split(order, ","), tektonv1.PipelineRunSpecStatusPending, kubeinteraction.StateQueued)
	for {
		acquired, err := r.qm.AddListToRunningQueue(repo, orderedList)
		if err != nil {
			return fmt.Errorf("failed to add to queue: %s: %w", pr.GetName(), err)
		}
		if len(acquired) == 0 {
			logger.Infof("no new PipelineRun acquired for repo %s", repo.GetName())
			break
		}

		for _, prKeys := range acquired {
			nsName := strings.Split(prKeys, "/")
			repoKey := queuepkg.RepoKey(repo)
			pr, err = r.run.Clients.Tekton.TektonV1().PipelineRuns(nsName[0]).Get(ctx, nsName[1], metav1.GetOptions{})
			if err != nil {
				logger.Info("failed to get pr with namespace and name: ", nsName[0], nsName[1])
				_ = r.qm.RemoveFromQueue(repoKey, prKeys)
			} else {
				if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
					logger.Errorf("failed to update pipelineRun to in_progress: %w", err)
					_ = r.qm.RemoveFromQueue(repoKey, prKeys)
				} else {
					processed = true
				}
			}
		}
		if processed {
			break
		}
		if itered >= maxIterations {
			return fmt.Errorf("max iterations reached of %d times trying to get a pipelinerun started for %s", maxIterations, repo.GetName())
		}
		itered++
	}
	return nil
}
