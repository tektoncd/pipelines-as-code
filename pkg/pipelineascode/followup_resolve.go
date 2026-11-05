package pipelineascode

import (
	"context"
	stderrors "errors"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	prgroup "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerungroup"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/resolve"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
)

// ErrFollowUpDefinitionNotFound reports a follow-up definition that is not in
// the .tekton directory fetched at the event's pinned revision: removed after
// the event, or provenance drift. Permanent: the entry is skipped, not
// requeued.
var ErrFollowUpDefinitionNotFound = stderrors.New("follow-up definition not found in the .tekton directory")

// ResolveFollowUpDefinition fetches the .tekton directory at the revision the
// event pinned and resolves the named definition the way the event-time
// matching did: template variables, remote tasks, the git auth secret. The
// returned PipelineRun is ready to be decorated and created.
//
// The definition is re-resolved instead of stored on the entry, so no
// annotation size limit applies to it: in-repository content is pinned by the
// revision, remote tasks are fetched as they are at creation time.
func ResolveFollowUpDefinition(ctx context.Context, run *params.Run, k8int kubeinteraction.Interface,
	eventEmitter *events.EventEmitter, logger *zap.SugaredLogger, vcx provider.Interface,
	event *info.Event, repo *v1alpha1.Repository, definitionName string, paramFilters map[string]string,
) (*tektonv1.PipelineRun, error) {
	provenance := pipelineRunProvenance(repo)
	raw, err := vcx.GetTektonDir(ctx, event, tektonDir, provenance)
	if err != nil {
		return nil, fmt.Errorf("cannot fetch the %s directory: %w", tektonDir, err)
	}

	templated := replaceTemplateVariables(ctx, run, k8int, eventEmitter, vcx, event, repo, raw, paramFilters)
	types, err := resolve.ReadTektonTypes(ctx, logger, templated)
	if err != nil {
		return nil, fmt.Errorf("cannot parse the %s directory: %w", tektonDir, err)
	}
	pipelineRuns, err := resolve.MetadataResolve(types.PipelineRuns)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve the pipelinerun metadata: %w", err)
	}

	pr := pipelineRunByDefinition(pipelineRuns, definitionName)
	if pr == nil {
		return nil, fmt.Errorf("%w: %s", ErrFollowUpDefinitionNotFound, definitionName)
	}

	remoteTasks := run.Info.Pac != nil && run.Info.GetPacOpts().RemoteTasks
	if remoteTasks {
		// like the event time, only the definition being run is resolved: a
		// broken unrelated definition must not fail it
		types.PipelineRuns = []*tektonv1.PipelineRun{pr}
		resolved, err := resolve.Resolve(ctx, run, logger, vcx, types, event, &resolve.Opts{
			GenerateName:       true,
			RemoteTasks:        true,
			RepositoryRevision: getRepositoryRevisionForProvenance(event, provenance),
		})
		if err != nil {
			return nil, fmt.Errorf("cannot resolve the remote tasks of the follow-up %s: %w", definitionName, err)
		}
		if pr = pipelineRunByDefinition(resolved, definitionName); pr == nil {
			return nil, fmt.Errorf("%w: %s", ErrFollowUpDefinitionNotFound, definitionName)
		}
	}

	// the git auth secret, like changePipelineRun does for the event-time runs
	return withGitAuthSecret(pr, definitionName, func(s string) string {
		return replaceTemplateVariables(ctx, run, k8int, eventEmitter, vcx, event, repo, s, paramFilters)
	})
}

// pipelineRunByDefinition finds the PipelineRun of a definition name, the way
// the group identifies runs: by the original name stamped at metadata
// resolution, falling back to the template's own name.
func pipelineRunByDefinition(prs []*tektonv1.PipelineRun, definitionName string) *tektonv1.PipelineRun {
	for _, pr := range prs {
		if prgroup.DefinitionName(pr) == definitionName {
			return pr
		}
	}
	return nil
}
