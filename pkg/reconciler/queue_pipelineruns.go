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

	orderedList, err := queuepkg.FilterPipelineRunByState(ctx, r.run.Clients.Tekton, strings.Split(order, ","), tektonv1.PipelineRunSpecStatusPending, kubeinteraction.StateQueued)
	if err != nil {
		return err
	}
	return r.processQueueAdmission(ctx, logger, repo, pr, orderedList, queuepkg.AdmissionInitial)
}

func (r *Reconciler) processQueueAdmission(ctx context.Context, logger *zap.SugaredLogger, repo *pacAPIv1alpha1.Repository, owner *tektonv1.PipelineRun, orderedList []string, mode queuepkg.AdmissionMode) error {
	// Resolve current limits for every admission, including resume, without
	// changing the repository used to resolve inherited provider secrets.
	queueRepo := repo
	if globalRepo, err := r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); err == nil && globalRepo != nil {
		logger.Info("Merging global repository settings with local repository settings")
		if merged := copyRepositoryForMerge(repo); merged != nil {
			queueRepo = merged
			queueRepo.Spec.Merge(globalRepo.Spec)
		}
	}
	seen := map[string]bool{}
	for {
		work, err := r.qm.AcquireAdmission(queueRepo, owner, orderedList, mode)
		if err != nil {
			return fmt.Errorf("failed to acquire queue admission: %w", err)
		}
		if work == nil {
			return nil
		}
		if len(work.Candidates) == 0 {
			logger.Infof("no new PipelineRun acquired for repo %s", repo.GetName())
			return r.qm.FinishAdmission(work)
		}
		var errs []error
		dropped := map[string]bool{}
		started := false
		for i := range work.Candidates {
			candidate := &work.Candidates[i]
			if seen[candidate.Key] {
				errs = append(errs, fmt.Errorf("queue returned %s twice during admission", candidate.Key))
				continue
			}
			seen[candidate.Key] = true
			err := r.startQueueCandidate(ctx, logger, repo, candidate)
			switch candidate.Outcome {
			case queuepkg.StartRetry:
			case queuepkg.StartGone:
				dropped[candidate.Key] = true
			case queuepkg.StartSucceeded:
				started = true
			}
			if err != nil {
				if mode == queuepkg.AdmissionPromotion && candidate.Outcome == queuepkg.StartSucceeded {
					// Reporting is best effort after a confirmed start; it is
					// not unfinished queue work.
					logger.Errorf("started pipelineRun %s but could not report it: %v", candidate.Key, err)
				} else {
					errs = append(errs, err)
				}
			}
		}
		// Account for the whole batch, including candidates after an error.
		errs = append(errs, r.qm.FinishAdmission(work))
		err = stderrors.Join(errs...)
		if err != nil {
			return err
		}
		if started {
			return nil
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
	}
}

func (r *Reconciler) startQueueCandidate(ctx context.Context, logger *zap.SugaredLogger, repo *pacAPIv1alpha1.Repository, candidate *queuepkg.StartCandidate) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	namespace, name, ok := queuepkg.SplitPrKey(candidate.Key)
	if !ok {
		logger.Errorf("invalid pipelineRun key %q queued for repository %s, dropping it", candidate.Key, repo.Name)
		candidate.Outcome = queuepkg.StartGone
		return nil
	}
	pr, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			candidate.Outcome = queuepkg.StartGone
			return nil
		}
		return fmt.Errorf("failed to get pipelineRun %s: %w", candidate.Key, err)
	}
	if candidate.Attempted && candidate.UID != pr.UID {
		candidate.Outcome = queuepkg.StartGone
		return nil
	}
	candidate.UID = pr.UID
	candidate.Attempted = true
	err = r.updatePipelineRunToInProgress(ctx, logger, repo, pr)
	switch {
	case stderrors.Is(err, errPipelineRunGone):
		candidate.Outcome = queuepkg.StartGone
		return nil
	case stderrors.Is(err, ErrPipelineRunNotStarted):
		// The attempt may still commit. Only this owner may resume its slot.
	default:
		candidate.Outcome = queuepkg.StartSucceeded
	}
	if err != nil {
		return fmt.Errorf("failed to update pipelineRun %s to in_progress: %w", candidate.Key, err)
	}
	return nil
}
