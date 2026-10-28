package formatting

import (
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipelines-as-code/pkg/provider/status"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
)

// PipelineRunStatus return status of PR  success failed or skipped.
func PipelineRunStatus(pr *tektonv1.PipelineRun) status.Conclusion {
	if len(pr.Status.Conditions) == 0 {
		return status.ConclusionNeutral
	}
	if pr.Status.GetCondition(apis.ConditionSucceeded).GetReason() == tektonv1.PipelineRunSpecStatusCancelled {
		return status.ConclusionCancelled
	}
	if pr.Status.Conditions[0].Status == corev1.ConditionFalse {
		return status.ConclusionFailure
	}
	return status.ConclusionSuccess
}
