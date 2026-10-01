package sort

import (
	"bytes"
	"fmt"
	"html"
	"sort"
	"text/template"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

type tkr struct {
	taskLogURL string
	steps      []stepStatus
	*tektonv1.PipelineRunTaskRunStatus
}

func (t tkr) taskName() string {
	if t.Status != nil && t.Status.TaskSpec != nil && t.Status.TaskSpec.DisplayName != "" {
		return t.Status.TaskSpec.DisplayName
	}
	return t.PipelineTaskName
}

func (t tkr) ConsoleLogURL() string {
	return fmt.Sprintf("[%s](%s)", t.taskName(), t.taskLogURL)
}

// ConsoleLogHTMLLink is the link to the logs of the task written in plain HTML,
// for the places where the providers do not render markdown, like the summary
// of a folded block.
func (t tkr) ConsoleLogHTMLLink() string {
	return fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(t.taskLogURL), html.EscapeString(t.taskName()))
}

// Steps is the breakdown of the steps of that task, it is empty when the
// breakdown has been disabled or when the task has only one step to show.
func (t tkr) Steps() []stepStatus {
	return t.steps
}

type taskrunList []tkr

func (trs taskrunList) Len() int      { return len(trs) }
func (trs taskrunList) Swap(i, j int) { trs[i], trs[j] = trs[j], trs[i] }
func (trs taskrunList) Less(i, j int) bool {
	if trs[j].Status == nil || trs[j].Status.StartTime == nil {
		return false
	}

	if trs[i].Status == nil || trs[i].Status.StartTime == nil {
		return true
	}

	if trs[i].Status.StartTime.Equal(trs[j].Status.StartTime) {
		return trs[i].PipelineTaskName > trs[j].PipelineTaskName
	}
	return trs[j].Status.StartTime.Before(trs[i].Status.StartTime)
}

// maxTaskStatusTextSize is a conservative budget for the rendered task status,
// the providers cap the size of the status they accept (GitHub rejects a check
// run whose text goes over 65535 bytes) and the task status is only a part of
// the message we send them. When the step breakdown makes us go over that
// budget we render the status again without it.
const maxTaskStatusTextSize = 55000

// TaskStatusTmpl generate a template of all status of a TaskRuns sorted to a statusTemplate as defined by the git provider.
func TaskStatusTmpl(pr *tektonv1.PipelineRun, trStatus map[string]*tektonv1.PipelineRunTaskRunStatus, runs *params.Run, pacInfo *info.PacOpts, config *info.ProviderConfig) (string, error) {
	if len(trStatus) == 0 {
		return "PipelineRun has no taskruns", nil
	}

	showSteps := pacInfo != nil && pacInfo.StatusShowSteps
	output, err := renderTaskStatus(pr, trStatus, runs, config, showSteps)
	if err != nil {
		return "", err
	}
	if showSteps && len(output) > maxTaskStatusTextSize {
		return renderTaskStatus(pr, trStatus, runs, config, false)
	}
	return output, nil
}

func renderTaskStatus(pr *tektonv1.PipelineRun, trStatus map[string]*tektonv1.PipelineRunTaskRunStatus, runs *params.Run, config *info.ProviderConfig, showSteps bool) (string, error) {
	trl := make(taskrunList, 0, len(trStatus))
	outputBuffer := bytes.Buffer{}

	for _, taskrunStatus := range trStatus {
		task := tkr{
			taskLogURL:               runs.Clients.ConsoleUI().TaskLogURL(pr, taskrunStatus),
			PipelineRunTaskRunStatus: taskrunStatus,
		}
		if showSteps {
			task.steps = collectSteps(taskrunStatus, config.SkipEmoji, func(stepName string) string {
				return runs.Clients.ConsoleUI().StepLogURL(pr, taskrunStatus, stepName)
			})
		}
		trl = append(trl, task)
	}
	sort.Sort(sort.Reverse(trl))

	funcMap := template.FuncMap{
		"formatDuration":  formatting.Duration,
		"formatCondition": formatting.ConditionEmoji,
	}

	if config.SkipEmoji {
		funcMap["formatCondition"] = formatting.ConditionSad
	}

	data := struct{ TaskRunList taskrunList }{TaskRunList: trl}
	t := template.Must(template.New("Task Status").Funcs(funcMap).Parse(config.TaskStatusTMPL))
	if err := t.Execute(&outputBuffer, data); err != nil {
		_, _ = fmt.Fprintf(&outputBuffer, "failed to execute template: ")
		return "", err
	}

	return outputBuffer.String(), nil
}
