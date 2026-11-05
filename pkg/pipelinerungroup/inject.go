package pipelinerungroup

import (
	"regexp"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// runsPlaceholderRe matches the {{ runs.<definition>.<field> }} placeholders
// that event-time templating leaves untouched because their values only exist
// once the dependencies are done. A definition name can contain dots, so the
// path is split against the dependency names by resolvePlaceholder.
var runsPlaceholderRe = regexp.MustCompile(`\{\{\s*runs\.([A-Za-z0-9_.-]+)\s*\}\}`)

// runFieldRe matches the field part of a runs placeholder.
var runFieldRe = regexp.MustCompile(`^(name|status|reason|results\.[A-Za-z0-9_.-]+)$`)

// InjectRunParams resolves the {{ runs.* }} placeholders in spec.params values
// with the dependency states, structurally: it assigns the typed ParamValue
// fields and never re-parses the YAML or re-templates the object, so a result
// coming from untrusted pipeline code is inserted as plain data. It returns
// the unresolved references, if any.
//
// Only spec.params is processed; placeholders anywhere else are left as-is.
func InjectRunParams(pr *tektonv1.PipelineRun, states map[string]RunState) []string {
	var unresolved []string
	resolve := func(placeholder string) (string, bool) {
		return resolvePlaceholder(placeholder, states, &unresolved)
	}
	for i := range pr.Spec.Params {
		param := &pr.Spec.Params[i]
		param.Value.StringVal = replacePlaceholders(param.Value.StringVal, resolve)
		for j := range param.Value.ArrayVal {
			param.Value.ArrayVal[j] = replacePlaceholders(param.Value.ArrayVal[j], resolve)
		}
		for k, v := range param.Value.ObjectVal {
			param.Value.ObjectVal[k] = replacePlaceholders(v, resolve)
		}
	}
	return unresolved
}

func replacePlaceholders(value string, resolve func(string) (string, bool)) string {
	return runsPlaceholderRe.ReplaceAllStringFunc(value, func(placeholder string) string {
		if resolved, ok := resolve(placeholder); ok {
			return resolved
		}
		return placeholder
	})
}

func resolvePlaceholder(placeholder string, states map[string]RunState, unresolved *[]string) (string, bool) {
	// only called on runsPlaceholderRe matches
	path := runsPlaceholderRe.FindStringSubmatch(placeholder)[1]
	dep, field, ok := splitRunsPath(path, states)
	if !ok {
		*unresolved = append(*unresolved, placeholder)
		return "", false
	}
	state := states[dep]
	switch {
	case field == "name":
		return state.Name, true
	case field == "status":
		return state.Status, true
	case field == "reason":
		return state.Reason, true
	case strings.HasPrefix(field, "results."):
		result := strings.TrimPrefix(field, "results.")
		if value, ok := state.Results[result]; ok {
			return value, true
		}
	}
	*unresolved = append(*unresolved, placeholder)
	return "", false
}

// splitRunsPath splits a <definition>.<field> path on the dependency names:
// a definition name can contain dots, so the longest dependency name followed
// by a valid field wins.
func splitRunsPath(path string, states map[string]RunState) (string, string, bool) {
	dep, field := "", ""
	for name := range states {
		rest, ok := strings.CutPrefix(path, name+".")
		if !ok || !runFieldRe.MatchString(rest) || len(name) <= len(dep) {
			continue
		}
		dep, field = name, rest
	}
	return dep, field, dep != ""
}

// DepsOf returns the dependency definition names of a created follow-up, from
// its on-pipelinerun annotation.
func DepsOf(pr *tektonv1.PipelineRun) ([]string, error) {
	return ParseDependsOn(pr.GetAnnotations()[keys.OnPipelineRun])
}
