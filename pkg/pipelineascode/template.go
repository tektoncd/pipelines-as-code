package pipelineascode

import (
	"context"
	"fmt"
	"net/http"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/customparams"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/templates"
	"go.uber.org/zap"
)

// makeTemplate will process all templates replacing the value from the event and from the
// params as set on Repo CR.
func (p *PacRun) makeTemplate(ctx context.Context, repo *v1alpha1.Repository, template string) string {
	cp := customparams.NewCustomParams(p.event, repo, p.run, p.k8int, p.eventEmitter, p.vcx)
	maptemplate, changedFiles, err := cp.GetParams(ctx)
	if err != nil {
		p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "ParamsError",
			fmt.Sprintf("error processing repository CR custom params: %s", err.Error()))
	} else {
		p.debugf("makeTemplate: resolved %d params and %d changed file groups", len(maptemplate), len(changedFiles))
	}
	if maptemplate == nil {
		maptemplate = map[string]string{}
	}

	// convert pull request number to string
	if p.event.PullRequestNumber != 0 {
		maptemplate["pull_request_number"] = fmt.Sprintf("%d", p.event.PullRequestNumber)
	}

	// replace placeholders variable as well as evaluate cel expressions
	headers := http.Header{}
	if p.event.Request != nil && p.event.Request.Header != nil {
		headers = p.event.Request.Header
	}

	return templates.ReplacePlaceHoldersVariables(template, maptemplate, p.event.Event, headers, changedFiles)
}
