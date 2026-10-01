package consoleui

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/templates"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/client-go/dynamic"
)

// CustomConsole is immutable once built: WithParams returns a new console
// rather than mutating the receiver, so a console shared between concurrent
// requests cannot be reconfigured under them.
type CustomConsole struct {
	pacInfo     *info.PacOpts
	extraParams map[string]string
}

// NewCustomConsole snapshots the settings it needs so a later change to the
// caller's PacOpts cannot alter the URLs this console renders.
func NewCustomConsole(pacInfo *info.PacOpts) *CustomConsole {
	snapshot := *pacInfo
	return &CustomConsole{pacInfo: &snapshot}
}

func (o *CustomConsole) GetName() string {
	if o.pacInfo.CustomConsoleName == "" {
		return fmt.Sprintf("https://url.setting.%s.is.not.configured", settings.CustomConsoleNameKey)
	}
	return o.pacInfo.CustomConsoleName
}

func (o *CustomConsole) URL() string {
	if o.pacInfo.CustomConsoleURL == "" {
		return fmt.Sprintf("https://url.setting.%s.is.not.configured", settings.CustomConsoleURLKey)
	}
	return o.pacInfo.CustomConsoleURL
}

// WithParams returns a copy of the console carrying its own extra substitution
// parameters, leaving the receiver and the caller's map untouched.
func (o *CustomConsole) WithParams(mt map[string]string) Interface {
	scoped := &CustomConsole{pacInfo: o.pacInfo}
	if mt != nil {
		scoped.extraParams = maps.Clone(mt)
	}
	return scoped
}

// consoleParams builds the built-in substitution values from the arguments of a
// single call. Values that the caller cannot provide are left empty.
func consoleParams(pr *tektonv1.PipelineRun, taskRunStatus *tektonv1.PipelineRunTaskRunStatus) map[string]string {
	dict := map[string]string{
		"namespace":       "",
		"pr":              "",
		"task":            "",
		"pod":             "",
		"firstFailedStep": "",
	}
	if pr != nil {
		dict["namespace"] = pr.GetNamespace()
		dict["pr"] = pr.GetName()
	}
	if taskRunStatus == nil {
		return dict
	}
	dict["task"] = taskRunStatus.PipelineTaskName
	if taskRunStatus.Status == nil {
		return dict
	}
	dict["pod"] = taskRunStatus.Status.PodName
	for _, step := range taskRunStatus.Status.Steps {
		if step.Terminated != nil && step.Terminated.ExitCode != 0 {
			dict["firstFailedStep"] = step.Name
			break
		}
	}
	return dict
}

func taskParams(pr *tektonv1.PipelineRun, taskRunStatus *tektonv1.PipelineRunTaskRunStatus, stepName string) map[string]string {
	base := consoleParams(pr, taskRunStatus)
	base["step"] = stepName
	return base
}

// generateURL will generate a URL from a template, trim some of the spaces and
// \n we get from yaml. The caller-owned dict gets the extra params merged into it,
// and those take precedence over the built-ins.
func (o *CustomConsole) generateURL(urlTmpl string, dict map[string]string) string {
	maps.Copy(dict, o.extraParams)

	newurl := templates.ReplacePlaceHoldersVariables(urlTmpl, dict, nil, nil, nil)
	newurl = strings.TrimSpace(strings.TrimSuffix(newurl, "\n"))
	if _, err := url.ParseRequestURI(newurl); err != nil {
		return o.URL()
	}
	if keys.ParamsRe.MatchString(newurl) {
		return o.URL()
	}
	return newurl
}

func (o *CustomConsole) DetailURL(pr *tektonv1.PipelineRun) string {
	if o.pacInfo.CustomConsolePRdetail == "" {
		return fmt.Sprintf("https://detailurl.setting.%s.is.not.configured", settings.CustomConsolePRDetailKey)
	}
	return o.generateURL(o.pacInfo.CustomConsolePRdetail, consoleParams(pr, nil))
}

func (o *CustomConsole) NamespaceURL(pr *tektonv1.PipelineRun) string {
	if o.pacInfo.CustomConsoleNamespaceURL == "" {
		return fmt.Sprintf("https://detailurl.setting.%s.is.not.configured", settings.CustomConsoleNamespaceURLKey)
	}
	return o.generateURL(o.pacInfo.CustomConsoleNamespaceURL, consoleParams(pr, nil))
}

func (o *CustomConsole) TaskLogURL(pr *tektonv1.PipelineRun, taskRunStatus *tektonv1.PipelineRunTaskRunStatus) string {
	if o.pacInfo.CustomConsolePRTaskLog == "" {
		return fmt.Sprintf("https://tasklogurl.setting.%s.is.not.configured", settings.CustomConsolePRTaskLogKey)
	}
	return o.generateURL(o.pacInfo.CustomConsolePRTaskLog, consoleParams(pr, taskRunStatus))
}

// StepLogURL points to the logs of a single step of a task, it falls back to
// the task log URL when no step specific template has been configured.
func (o *CustomConsole) StepLogURL(pr *tektonv1.PipelineRun, taskRunStatus *tektonv1.PipelineRunTaskRunStatus, stepName string) string {
	if o.pacInfo.CustomConsolePRStepLog == "" || stepName == "" {
		return o.TaskLogURL(pr, taskRunStatus)
	}
	return o.generateURL(o.pacInfo.CustomConsolePRStepLog, taskParams(pr, taskRunStatus, stepName))
}

func (o *CustomConsole) UI(_ context.Context, _ dynamic.Interface) error {
	return nil
}
