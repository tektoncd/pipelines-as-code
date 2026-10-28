package reconciler

import (
	"context"
	"strconv"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
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
		err = r.kinteract.CleanupPipelines(ctx, logger, repo, pr, maxVal)
		if err != nil {
			return err
		}
		return nil
	}

	// if annotation is not defined but default max-keep-run value is defined then use that
	if pacInfo.DefaultMaxKeepRuns > 0 {
		maxVal := pacInfo.DefaultMaxKeepRuns

		err := r.kinteract.CleanupPipelines(ctx, logger, repo, pr, maxVal)
		if err != nil {
			return err
		}
	}
	return nil
}
