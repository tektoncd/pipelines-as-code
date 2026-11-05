package pipelinerungroup

import (
	"regexp"
	"sort"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// runsPlaceholderRe matches {{ runs.<definition>.<field> }} placeholders; the
// path is split on dependency names since definitions can contain dots.
var runsPlaceholderRe = regexp.MustCompile(`\{\{\s*runs\.([A-Za-z0-9_.-]+)\s*\}\}`)

// runFieldRe matches the field part of a runs placeholder.
var runFieldRe = regexp.MustCompile(`^(name|status|reason|results\.[A-Za-z0-9_.-]+)$`)

// InjectRunParams resolves {{ runs.* }} placeholders in spec.params as plain
// data, without re-templating, and returns the unresolved references.
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
	sort.Strings(unresolved)
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

// splitRunsPath splits a <definition>.<field> path; the longest dependency name
// followed by a valid field wins.
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

// DepsOf returns the dependency names from the on-pipelinerun annotation.
func DepsOf(pr *tektonv1.PipelineRun) ([]string, error) {
	return ParseDependsOn(pr.GetAnnotations()[keys.OnPipelineRun])
}
