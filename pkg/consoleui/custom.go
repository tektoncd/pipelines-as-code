package consoleui

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/templates"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/client-go/dynamic"
)

type CustomConsole struct {
	pacInfo              *info.PacOpts
	namespace, pr, task  string
	pod, firstFailedStep string
	extraParams          map[string]string
	mu                   sync.RWMutex
}

func NewCustomConsole(pacInfo *info.PacOpts) *CustomConsole {
	return &CustomConsole{pacInfo: pacInfo}
}

func (o *CustomConsole) GetName() string {
	if o.pacInfo.CustomConsoleName == "" {
		return fmt.Sprintf("https://url.setting.%s.%s", settings.CustomConsoleNameKey, NotConfiguredURLSuffix)
	}
	return o.pacInfo.CustomConsoleName
}

func (o *CustomConsole) URL() string {
	if o.pacInfo.CustomConsoleURL == "" {
		return fmt.Sprintf("https://url.setting.%s.%s", settings.CustomConsoleURLKey, NotConfiguredURLSuffix)
	}
	return o.pacInfo.CustomConsoleURL
}

func (o *CustomConsole) SetParams(mt map[string]string) {
	o.extraParams = mt
}

// generateURL will generate a URL from a template, trim some of the spaces and
// \n we get from yaml
// return the default URL if there it's not become a proper url or that it has
// some of the templates like {{}} left.
func (o *CustomConsole) generateURL(urlTmpl string) string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	dict := map[string]string{
		"namespace":       o.namespace,
		"pr":              o.pr,
		"task":            o.task,
		"pod":             o.pod,
		"firstFailedStep": o.firstFailedStep,
	}
	for k, v := range o.extraParams {
		dict[k] = v
	}

	newurl := templates.ReplacePlaceHoldersVariables(urlTmpl, dict, nil, nil, nil)
	// trim new line because yaml parser adds new line at the end of the string
	newurl = strings.TrimSpace(strings.TrimSuffix(newurl, "\n"))
	if _, err := url.ParseRequestURI(newurl); err != nil {
		return o.URL()
	}
	// detect if there is still some {{}} in the url
	if keys.ParamsRe.MatchString(newurl) {
		return o.URL()
	}
	return newurl
}

func (o *CustomConsole) DetailURL(pr *tektonv1.PipelineRun) string {
	if o.pacInfo.CustomConsolePRdetail == "" {
		return fmt.Sprintf("https://detailurl.setting.%s.%s", settings.CustomConsolePRDetailKey, NotConfiguredURLSuffix)
	}
	o.namespace = pr.GetNamespace()
	o.pr = pr.GetName()
	return o.generateURL(o.pacInfo.CustomConsolePRdetail)
}

func (o *CustomConsole) NamespaceURL(pr *tektonv1.PipelineRun) string {
	if o.pacInfo.CustomConsoleNamespaceURL == "" {
		return fmt.Sprintf("https://detailurl.setting.%s.%s", settings.CustomConsoleNamespaceURLKey, NotConfiguredURLSuffix)
	}
	o.namespace = pr.GetNamespace()
	return o.generateURL(o.pacInfo.CustomConsoleNamespaceURL)
}

func (o *CustomConsole) TaskLogURL(pr *tektonv1.PipelineRun, taskRunStatus *tektonv1.PipelineRunTaskRunStatus) string {
	if o.pacInfo.CustomConsolePRTaskLog == "" {
		return fmt.Sprintf("https://tasklogurl.setting.%s.%s", settings.CustomConsolePRTaskLogKey, NotConfiguredURLSuffix)
	}
	firstFailedStep := ""
	// search for the first failed steps in taskrunstatus
	for _, step := range taskRunStatus.Status.Steps {
		if step.Terminated != nil && step.Terminated.ExitCode != 0 {
			firstFailedStep = step.Name
			break
		}
	}

	o.namespace = pr.GetNamespace()
	o.pr = pr.GetName()
	o.task = taskRunStatus.PipelineTaskName
	o.pod = taskRunStatus.Status.PodName
	o.firstFailedStep = firstFailedStep

	return o.generateURL(o.pacInfo.CustomConsolePRTaskLog)
}

func (o *CustomConsole) UI(_ context.Context, _ dynamic.Interface) error {
	return nil
}
