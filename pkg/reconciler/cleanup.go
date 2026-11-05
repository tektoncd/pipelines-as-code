package reconciler

import (
	"context"
	"slices"
	"strconv"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	prgroup "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerungroup"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
)

func (r *Reconciler) cleanupPipelineRuns(ctx context.Context, logger *zap.SugaredLogger, pacInfo *info.PacOpts, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) error {
	keepMaxPipeline, ok := pr.Annotations[keys.MaxKeepRuns]
	if ok {
		maxVal, err := strconv.Atoi(keepMaxPipeline)
		if err != nil {
			return err
		}
		// if annotation value is more than max limit defined in config then use from config
		if pacInfo.MaxKeepRunsUpperLimit > 0 && maxVal > pacInfo.MaxKeepRunsUpperLimit {
			logger.Infof("max-keep-run value in annotation (%v) is more than max-keep-run-upper-limit (%v), so using upper-limit", maxVal, pacInfo.MaxKeepRunsUpperLimit)
			maxVal = pacInfo.MaxKeepRunsUpperLimit
		}
		err = r.kinteract.CleanupPipelines(ctx, logger, repo, pr, maxVal, r.keepForFollowUps(logger))
		if err != nil {
			return err
		}
		return nil
	}

	// if annotation is not defined but default max-keep-run value is defined then use that
	if pacInfo.DefaultMaxKeepRuns > 0 {
		maxVal := pacInfo.DefaultMaxKeepRuns

		err := r.kinteract.CleanupPipelines(ctx, logger, repo, pr, maxVal, r.keepForFollowUps(logger))
		if err != nil {
			return err
		}
	}
	return nil
}

// keepForFollowUps keeps the old runs a pending follow-up still needs: a
// completed dependency it waits on (pruned, the follow-up would see it as
// missing and skip itself wrongly) and a run storing a pending follow-up (the
// follow-up would go down with it). It is evaluated on each run about to be
// pruned, read fresh by the cleanup; when that cannot be decided the run is
// kept.
func (r *Reconciler) keepForFollowUps(logger *zap.SugaredLogger) kubeinteraction.KeepFunc {
	return func(ctx context.Context, pr *tektonv1.PipelineRun) (bool, string) {
		waiting, err := r.hasWaitingDependent(ctx, logger, pr)
		if err != nil {
			logger.Errorf("cannot determine if a follow-up waits on pipelinerun %s, keeping it: %v", pr.GetName(), err)
			return true, "cannot list its follow-up group"
		}
		if waiting {
			return true, "a pending follow-up still needs it"
		}
		return false, ""
	}
}

// hasWaitingDependent reports whether a PipelineRun is still needed by a
// pending follow-up of its own group: because a dependency is named by
// the follow-up, or because it is the run the follow-up is stored on.
func (r *Reconciler) hasWaitingDependent(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) (bool, error) {
	groupID := prgroup.GroupIDOf(pr)
	if groupID == "" {
		// not part of any follow-up group: nothing can be waiting on it
		return false, nil
	}
	// the decision is taken on the deduplicated state of the group: a failed
	// migration can leave a stale pending copy on this run after the copy
	// that counts went terminal, and it must not keep the run forever
	group, err := r.listGroup(ctx, pr.GetNamespace(), groupID)
	if err != nil {
		return false, err
	}
	collected, err := prgroup.CollectFollowUps(group)
	if err != nil {
		// A broken follow-up annotation on another run must not block this
		// run's cleanup, so it is pruned like any other run. The error
		// says which run is corrupt so it can be repaired: until then the
		// pending follow-ups of the group it breaks surface as missing
		// dependencies.
		logger.Errorf("cannot collect the follow-ups of group %s, pruning pipelinerun %s: %v",
			groupID, pr.GetName(), err)
		return false, nil //nolint:nilerr
	}
	depName := prgroup.DefinitionName(pr)
	for _, fu := range collected {
		if !fu.FollowUp.Pending() {
			continue
		}
		// a pending follow-up stored on this run would be deleted with it,
		// one depending on it would see it missing
		if fu.Run.GetName() == pr.GetName() || slices.Contains(fu.FollowUp.Deps, depName) {
			return true, nil
		}
	}
	return false, nil
}
