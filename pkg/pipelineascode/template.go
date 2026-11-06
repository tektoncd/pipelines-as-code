package pipelineascode

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/customparams"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/templates"
	"go.uber.org/zap"
)

// makeTemplate will process all templates replacing the value from the event and from the
// params as set on Repo CR.
func (p *PacRun) makeTemplate(ctx context.Context, repo *v1alpha1.Repository, template string) string {
	return replaceTemplateVariables(ctx, p.run, p.k8int, p.eventEmitter, p.vcx, p.event, repo, template, nil)
}

func replaceTemplateVariables(ctx context.Context, run *params.Run, k8int kubeinteraction.Interface,
	eventEmitter *events.EventEmitter, vcx provider.Interface, event *info.Event,
	repo *v1alpha1.Repository, template string, paramFilters map[string]string,
) string {
	cp := customparams.NewCustomParams(event, repo, run, k8int, eventEmitter, vcx)
	if paramFilters != nil {
		cp = cp.WithFilterOutcomes(paramFilters)
	}
	maptemplate, changedFiles, err := cp.GetParams(ctx)
	if err != nil {
		eventEmitter.EmitMessage(repo, zap.ErrorLevel, "ParamsError",
			fmt.Sprintf("error processing repository CR custom params: %s", err.Error()))
	}
	if maptemplate == nil {
		maptemplate = map[string]string{}
	}

	// convert pull request number to string
	if event.PullRequestNumber != 0 {
		maptemplate["pull_request_number"] = fmt.Sprintf("%d", event.PullRequestNumber)
	}

	// replace placeholders variable as well as evaluate cel expressions
	headers := http.Header{}
	if event.Request != nil && event.Request.Header != nil {
		headers = event.Request.Header
	}

	return templates.ReplacePlaceHoldersVariables(template, maptemplate, event.Event, headers, changedFiles)
}
