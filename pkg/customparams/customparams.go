package customparams

import (
	"context"
	"fmt"

	celTypes "cel.dev/cel-go/common/types"
	"go.uber.org/zap"

	apincoming "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/incoming"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pacCel "github.com/openshift-pipelines/pipelines-as-code/pkg/cel"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	sectypes "github.com/openshift-pipelines/pipelines-as-code/pkg/secrets/types"
)

type CustomParams struct {
	event        *info.Event
	run          *params.Run
	k8int        kubeinteraction.Interface
	eventEmitter *events.EventEmitter
	repo         *v1alpha1.Repository
	vcx          provider.Interface
}

func NewCustomParams(event *info.Event, repo *v1alpha1.Repository, run *params.Run, k8int kubeinteraction.Interface, eventEmitter *events.EventEmitter, prov provider.Interface) CustomParams {
	return CustomParams{
		event:        event,
		repo:         repo,
		run:          run,
		k8int:        k8int,
		eventEmitter: eventEmitter,
		vcx:          prov,
	}
}

// applyIncomingParams apply incoming params to an existing map (overwriting existing keys).
func (p *CustomParams) applyIncomingParams(ret map[string]string) map[string]string {
	if p.event.Request == nil {
		return ret
	}
	if incomingParams, err := apincoming.ParseIncomingPayload(p.event.Request.Payload); err == nil {
		for k, v := range incomingParams.Params {
			if vs, ok := v.(string); ok {
				ret[k] = vs
			} else {
				p.eventEmitter.EmitMessage(p.repo, zap.WarnLevel, "IncomingParamsNotString", fmt.Sprintf("cannot convert incoming param key: %s value: %v as string", k, v))
			}
		}
	}
	return ret
}

// GetParams will process the parameters as set in the repo.Spec CR.
// value can come from a string or from a secretKeyRef or from a string value
// if both is set we pick the value and issue a warning in the user namespace
// we let the user specify a cel filter. If false then we skip the parameters.
// if multiple params name has a filter we pick up the first one that has
// matched true.
func (p *CustomParams) GetParams(ctx context.Context) (map[string]string, map[string]any, error) {
	stdParams, changedFiles := p.makeStandardParamsFromEvent(ctx)
	resolvedParams, mapFilters, parsedFromComment := map[string]string{}, map[string]string{}, map[string]string{}
	if p.event.TriggerComment != "" {
		parsedFromComment = opscomments.ParseKeyValueArgs(p.event.TriggerComment)
		for k, v := range parsedFromComment {
			if _, ok := stdParams[k]; ok {
				stdParams[k] = v
			}
		}
	}

	if p.repo.Spec.Params == nil {
		return p.applyIncomingParams(stdParams), changedFiles, nil
	}

	for index, value := range *p.repo.Spec.Params {
		// if the name is empty we skip it
		if value.Name == "" {
			p.eventEmitter.EmitMessage(p.repo, zap.ErrorLevel,
				"ParamsFilterSkipped", fmt.Sprintf("no name has been set in params[%d] of repo %s", index, p.repo.GetName()))
			continue
		}
		if value.Filter != "" {
			// if we already have a filter that has matched we skip it
			if _, ok := mapFilters[value.Name]; ok {
				p.eventEmitter.EmitMessage(p.repo, zap.WarnLevel,
					"ParamsFilterSkipped", fmt.Sprintf("skipping params name %s, filter has already been matched previously", value.Name))
				continue
			}

			// if the cel filter condition is false we skip it
			// TODO: add headers to customparams?
			cond, err := pacCel.Value(value.Filter, p.event.Event, nil, stdParams, changedFiles)
			if err != nil {
				p.eventEmitter.EmitMessage(p.repo, zap.ErrorLevel,
					"ParamsFilterError", fmt.Sprintf("there is an error on the cel filter: %s: %s", value.Name, err.Error()))
				return map[string]string{}, changedFiles, err
			}
			switch cond.(type) {
			case celTypes.Bool:
				if cond == celTypes.False {
					p.eventEmitter.EmitMessage(p.repo, zap.InfoLevel,
						"ParamsFilterSkipped", fmt.Sprintf("skipping params name %s, filter condition is false", value.Name))
					continue
				}
			default:
				p.eventEmitter.EmitMessage(p.repo, zap.InfoLevel,
					"ParamsFilterSkipped", fmt.Sprintf("skipping params name %s, filter condition is not a boolean reply: %s", value.Name, cond.Type().TypeName()))
				continue
			}
			mapFilters[value.Name] = value.Value
		}

		if value.SecretRef != nil && value.Value != "" {
			p.eventEmitter.EmitMessage(p.repo, zap.InfoLevel,
				"ParamsFilterUsedValue",
				fmt.Sprintf("repo %s, param name %s has a value and secretref, picking value", p.repo.GetName(), value.Name))
		}

		_, paramIsStd := stdParams[value.Name]
		_, paramParsedFromContent := parsedFromComment[value.Name]

		switch {
		case value.Value != "":
			resolvedParams[value.Name] = value.Value
		case paramParsedFromContent && !paramIsStd:
			// If the param is standard, it's initial value will be set later so we don't set it here.
			// Setting to empty string allows the parsedFromComment overrides to set the overridden value below.
			resolvedParams[value.Name] = ""
		case value.SecretRef != nil:
			secretValue, err := p.k8int.GetSecret(ctx, sectypes.GetSecretOpt{
				Namespace: p.repo.GetNamespace(),
				Name:      value.SecretRef.Name,
				Key:       value.SecretRef.Key,
			})
			if err != nil {
				return resolvedParams, changedFiles, err
			}
			resolvedParams[value.Name] = secretValue
		}
	}

	// TODO: Should we let the user override the standard params?
	// we don't let them here
	for k, v := range stdParams {
		// check if not already there
		if _, ok := resolvedParams[k]; !ok && v != "" {
			resolvedParams[k] = v
		}
	}

	// overwrite stdParams with parsed ones from the trigger comment
	for k, v := range parsedFromComment {
		if _, ok := resolvedParams[k]; ok && v != "" {
			resolvedParams[k] = v
		}
	}

	return p.applyIncomingParams(resolvedParams), changedFiles, nil
}
