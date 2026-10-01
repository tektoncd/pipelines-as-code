package sort

import (
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// terminationReasonSkipped is set by Tekton on the steps which have not been
// run because a previous step of the same task has failed.
const terminationReasonSkipped = "Skipped"

// stepStatus is the view of a step of a TaskRun as exposed to the status
// templates of the providers.
type stepStatus struct {
	// Name is the name of the step as set on the task.
	Name string
	// DisplayName is the user facing name of the step when the task sets one.
	DisplayName string
	// StepAction reports whether the step references a StepAction.
	StepAction bool

	logURL    string
	state     tektonv1.StepState
	skipEmoji bool
}

// Title is the name shown to the user for that step, the "step-" prefix that
// people often put in front of their step names adds nothing on a list of steps
// so it is left out. The links still use the real name of the step.
func (s stepStatus) Title() string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	if trimmed := strings.TrimPrefix(s.Name, "step-"); trimmed != "" {
		return trimmed
	}
	return s.Name
}

// ConsoleLogURL is a markdown link to the logs of that step on the console.
func (s stepStatus) ConsoleLogURL() string {
	return fmt.Sprintf("[%s](%s)", escapeMarkdownLinkText(s.Title()), s.logURL)
}

func escapeMarkdownLinkText(text string) string {
	return strings.NewReplacer(
		"\\", `\\`,
		"[", `\[`,
		"]", `\]`,
		"(", `\(`,
		")", `\)`,
		"|", `\|`,
		"\r", "",
		"\n", " ",
	).Replace(text)
}

// Status is the status of the step, with an emoji unless the provider has
// asked to skip them.
func (s stepStatus) Status() string {
	if s.skipEmoji {
		return formatting.StepStateSad(s.state)
	}
	return formatting.StepStateEmoji(s.state)
}

// Duration is how long the step took to run.
func (s stepStatus) Duration() string {
	if s.state.Terminated == nil {
		return "---"
	}
	return formatting.Duration(&s.state.Terminated.StartedAt, &s.state.Terminated.FinishedAt)
}

// collectSteps builds the list of steps to show for a TaskRun, the steps which
// have been skipped because a previous step has failed are left out since they
// carry no information.
func collectSteps(taskRunStatus *tektonv1.PipelineRunTaskRunStatus, skipEmoji bool, logURL func(stepName string) string) []stepStatus {
	if taskRunStatus == nil || taskRunStatus.Status == nil {
		return nil
	}

	// a task made of a single step says nothing more than the task row itself.
	// This looks at every step of the task and not only at the ones we show, so
	// that a task where all steps but one have been skipped after a failure
	// still shows which step has failed.
	if len(taskRunStatus.Status.Steps) < 2 {
		return nil
	}

	steps := []stepStatus{}
	for i, state := range taskRunStatus.Status.Steps {
		if state.TerminationReason == terminationReasonSkipped {
			continue
		}
		step := stepStatus{
			Name:      state.Name,
			state:     state,
			skipEmoji: skipEmoji,
			logURL:    logURL(state.Name),
		}
		if spec := specForStep(taskRunStatus.Status.TaskSpec, state.Name, i); spec != nil {
			step.DisplayName = spec.DisplayName
			step.StepAction = spec.Ref != nil
		}
		steps = append(steps, step)
	}

	return steps
}

// specForStep finds the spec of a step from its status, steps without a name
// on the task get a generated name in the status so we fall back on the
// position of the step in the task.
func specForStep(taskSpec *tektonv1.TaskSpec, name string, index int) *tektonv1.Step {
	if taskSpec == nil {
		return nil
	}
	for i := range taskSpec.Steps {
		if taskSpec.Steps[i].Name == name {
			return &taskSpec.Steps[i]
		}
	}
	if index < len(taskSpec.Steps) {
		return &taskSpec.Steps[index]
	}
	return nil
}
