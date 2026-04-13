package secrets

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	ktypes "github.com/openshift-pipelines/pipelines-as-code/pkg/secrets/types"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

const leakedReplacement = "*****"

// GetSecretsAttachedToPipelineRun get all secrets attached to a PipelineRun and
// grab their values attached to it.
func GetSecretsAttachedToPipelineRun(ctx context.Context, k kubeinteraction.Interface, pr *tektonv1.PipelineRun) []ktypes.SecretValue {
	ret := []ktypes.SecretValue{}
	// check if pipelineRef is defined or exist
	if pr.Spec.PipelineSpec == nil {
		return ret
	}

	for _, pt := range append(pr.Spec.PipelineSpec.Finally, pr.Spec.PipelineSpec.Tasks...) {
		if pt.TaskSpec == nil || pt.TaskSpec.Steps == nil {
			continue
		}
		for _, step := range pt.TaskSpec.Steps {
			for _, ev := range step.Env {
				if ev.ValueFrom == nil {
					continue
				}
				if ev.ValueFrom.SecretKeyRef == nil {
					continue
				}
				secretValue, err := k.GetSecret(ctx, ktypes.GetSecretOpt{
					Name:      ev.ValueFrom.SecretKeyRef.Name,
					Key:       ev.ValueFrom.SecretKeyRef.Key,
					Namespace: pr.GetNamespace(),
				})
				// that really should not happen but let's go on and continue if that's the case
				if err != nil {
					continue
				}
				keyv := fmt.Sprintf("%s-%s", ev.ValueFrom.SecretKeyRef.Name, ev.ValueFrom.SecretKeyRef.Key)
				there := false
				for _, value := range ret {
					if value.Name == keyv {
						there = true
					}
				}
				if !there {
					ret = append(ret, ktypes.SecretValue{
						Name:  keyv,
						Value: secretValue,
					})
				}
			}
		}
	}

	return ret
}

// sortSecretsByLongests sort all secrets by length, the longest first
// if we don't sort by longest then if there two passwords with the same prefix
// the shortest one will replace and would leak the end of the passwords of the longest after.
func sortSecretsByLongests(values []ktypes.SecretValue) []ktypes.SecretValue {
	ret := make([]ktypes.SecretValue, len(values))
	copy(ret, values)
	for i := 0; i < len(ret); i++ {
		for j := i + 1; j < len(ret); j++ {
			if len(ret[i].Value) < len(ret[j].Value) {
				ret[i], ret[j] = ret[j], ret[i]
			}
		}
	}
	return ret
}

// ReplaceSecretsInText this will take a text snippet and hide the leaked secret.
func ReplaceSecretsInText(text string, values []ktypes.SecretValue) string {
	for _, sv := range sortSecretsByLongests(values) {
		text = strings.ReplaceAll(text, sv.Value, leakedReplacement)
	}
	return text
}
