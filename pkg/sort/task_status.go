package sort

import (
	"bytes"
	"fmt"
	"sort"
	"text/template"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

type tkr struct {
	taskLogURL string
	*tektonv1.PipelineRunTaskRunStatus
}

func (t tkr) ConsoleLogURL() string {
	name := t.PipelineTaskName
	if t.Status != nil && t.Status.TaskSpec != nil && t.Status.TaskSpec.DisplayName != "" {
		name = t.Status.TaskSpec.DisplayName
	}
	return fmt.Sprintf("[%s](%s)", name, t.taskLogURL)
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

// TaskStatusTmpl generate a template of all status of a TaskRuns sorted to a statusTemplate as defined by the git provider.
func TaskStatusTmpl(pr *tektonv1.PipelineRun, trStatus map[string]*tektonv1.PipelineRunTaskRunStatus, console consoleui.Interface, config *info.ProviderConfig) (string, error) {
	trl := taskrunList{}
	outputBuffer := bytes.Buffer{}

	if len(trStatus) == 0 {
		return "PipelineRun has no taskruns", nil
	}

	for _, taskrunStatus := range trStatus {
		trl = append(trl, tkr{
			taskLogURL:               console.TaskLogURL(pr, taskrunStatus),
			PipelineRunTaskRunStatus: taskrunStatus,
		})
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
