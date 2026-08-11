package reconciler

import (
	"context"
	stderrors "errors"
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

		var errs []error
		dropped := map[string]bool{}
		for _, prKeys := range acquired {
			repoKey := queuepkg.RepoKey(repo)
			nsName := strings.Split(prKeys, "/")
			if len(nsName) != 2 {
				logger.Errorf("invalid pipelineRun key %q queued for repository %s, dropping it", prKeys, repo.GetName())
				_ = r.qm.RemoveFromQueue(repoKey, prKeys)
				dropped[prKeys] = true
				continue
			}
			acquiredPR, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(nsName[0]).Get(ctx, nsName[1], metav1.GetOptions{})
			if err != nil {
				// Nothing has been written to the cluster yet, so this PipelineRun
				// is still pending and cannot be running. Hand the slot back: if we
				// kept it, the retry would find this PipelineRun already in the
				// running set, never re-acquire it, and never free the slot, since
				// only a completed PipelineRun releases one.
				_ = r.qm.RemoveFromQueue(repoKey, prKeys)
				if errors.IsNotFound(err) {
					// This key came straight from the ordered list built at the top
					// of this call. Drop it there too, or the next iteration of this
					// loop would re-add and re-acquire the same gone PipelineRun,
					// wasting a Get and possibly tripping maxIterations below even
					// though there is nothing left to do.
					logger.Infof("pipelineRun %s does not exist anymore, releasing its queue slot for repository %s", prKeys, repo.GetName())
					dropped[prKeys] = true
					continue
				}
				// Keep going rather than return: the other acquired PipelineRuns
				// are already holding slots, and abandoning them here would strand
				// those slots until the watcher restarts.
				errs = append(errs, fmt.Errorf("failed to get pipelineRun %s: %w", prKeys, err))
				continue
			}
			if err := r.updatePipelineRunToInProgress(ctx, logger, repo, acquiredPR); err != nil {
				if stderrors.Is(err, ErrPipelineRunNotStarted) {
					// The state patch never landed, so the PipelineRun is still
					// pending. Release the slot so the retry can pick it up again.
					logger.Infof("pipelineRun %s could not be started, releasing its queue slot for repository %s", prKeys, repo.GetName())
					_ = r.qm.RemoveFromQueue(repoKey, prKeys)
				}
				// Otherwise the state patch landed before this failed, so the
				// PipelineRun is already running in the cluster. Keep the slot:
				// releasing it would let the queue admit past the concurrency
				// limit and leave the in-memory queue permanently out of sync.
				// The slot is freed when the PipelineRun completes.
				//
				// Either way, keep processing the remaining acquired PipelineRuns
				// so their slots are not stranded, and report the error at the end.
				errs = append(errs, fmt.Errorf("failed to update pipelineRun %s to in_progress: %w", prKeys, err))
				continue
			}
			processed = true
		}
		if len(errs) > 0 {
			return stderrors.Join(errs...)
		}
		if processed {
			break
		}
		if len(dropped) > 0 {
			filtered := orderedList[:0]
			for _, key := range orderedList {
				if !dropped[key] {
					filtered = append(filtered, key)
				}
			}
			orderedList = filtered
		}
		if itered >= maxIterations {
			return fmt.Errorf("max iterations reached of %d times trying to get a pipelinerun started for %s", maxIterations, repo.GetName())
		}
		itered++
	}
	return nil
}
