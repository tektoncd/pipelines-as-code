package formatting

import (
	"fmt"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	knative1 "knative.dev/pkg/apis/duck/v1"
)

const nonAttributedStr = "---"

// formatCondition knative formatcondition with emoji or not.
func formatCondition(c knative1.Conditions, skipemoji bool) string {
	var status, emoji string
	if len(c) == 0 {
		return nonAttributedStr
	}

	switch c[0].Status {
	case corev1.ConditionFalse:
		emoji = "🔴"
		status = "Failed"
	case corev1.ConditionTrue:
		emoji = "🟢"
		status = "Succeeded"
	case corev1.ConditionUnknown:
		emoji = "🟡"
		status = "Running"
	default:
		emoji = "🔄"
		status = "Pending"
	}

	if !skipemoji {
		status = fmt.Sprintf("%s %s", emoji, status)
	}

	return status
}

func ConditionEmoji(c knative1.Conditions) string {
	return formatCondition(c, false)
}

func ConditionSad(c knative1.Conditions) string {
	return formatCondition(c, true)
}

// formatStepState formats the state of a step of a TaskRun with emoji or not.
func formatStepState(s tektonv1.StepState, skipemoji bool) string {
	var status, emoji string
	switch {
	case s.Terminated != nil && s.Terminated.ExitCode == 0:
		emoji = "🟢"
		status = "Succeeded"
	case s.Terminated != nil:
		emoji = "🔴"
		status = "Failed"
	case s.Running != nil:
		emoji = "🟡"
		status = "Running"
	case s.Waiting != nil:
		emoji = "🔄"
		status = "Pending"
	default:
		return nonAttributedStr
	}

	if !skipemoji {
		status = fmt.Sprintf("%s %s", emoji, status)
	}

	return status
}

func StepStateEmoji(s tektonv1.StepState) string {
	return formatStepState(s, false)
}

func StepStateSad(s tektonv1.StepState) string {
	return formatStepState(s, true)
}
