package pipelinerungroup

import (
	"encoding/json"
	"sort"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
)

// Statuses exposed to CEL and templating for each dependency.
const (
	StatusSucceeded = "Succeeded"
	StatusFailed    = "Failed"
	StatusCancelled = "Cancelled"
	StatusError     = "Error"
	// ReasonMissing marks a dependency pruned or never created.
	ReasonMissing    = "Missing"
	maxResultSize    = 4096
	maxResultsPerRun = 32
)

// RunState is one dependency as seen by a waiting follow-up.
type RunState struct {
	// Name is the created PipelineRun name.
	Name string
	// Status is one of Succeeded, Failed, Cancelled, Error or Missing.
	Status string
	// Reason is the Tekton condition reason, kept as detail.
	Reason string
	// Results holds the stringified Pipeline results; arrays and objects are JSON.
	Results map[string]string
}

// RunStateFromPR maps a done PipelineRun to its RunState.
func RunStateFromPR(pr *tektonv1.PipelineRun) RunState {
	return RunState{
		Name:    pr.GetName(),
		Status:  StatusFromPR(pr),
		Reason:  pr.Status.GetCondition(apis.ConditionSucceeded).GetReason(),
		Results: ResultsFromPR(pr),
	}
}

// missingRunState is a dependency that no longer exists, surfaced as Error.
func missingRunState() RunState {
	return RunState{Status: StatusError, Reason: ReasonMissing, Results: map[string]string{}}
}

// StatusFromPR maps the Succeeded condition to a status, matching the check
// shown on the provider: only the Cancelled reason is Cancelled, a timeout is
// Failed.
func StatusFromPR(pr *tektonv1.PipelineRun) string {
	cond := pr.Status.GetCondition(apis.ConditionSucceeded)
	switch {
	case cond == nil || cond.Type == "":
		return StatusError
	case cond.Status == corev1.ConditionTrue:
		return StatusSucceeded
	case cond.Status == corev1.ConditionFalse:
		if cond.Reason == tektonv1.PipelineRunSpecStatusCancelled {
			return StatusCancelled
		}
		return StatusFailed
	default:
		// unknown or still running: defensive mapping
		return StatusError
	}
}

// ResultsFromPR extracts the results as strings, bounded in count and size
// since they are untrusted.
func ResultsFromPR(pr *tektonv1.PipelineRun) map[string]string {
	results := map[string]string{}
	names := make([]string, 0, len(pr.Status.Results))
	byName := map[string]tektonv1.PipelineRunResult{}
	for _, result := range pr.Status.Results {
		names = append(names, result.Name)
		byName[result.Name] = result
	}
	sort.Strings(names)
	for _, name := range names {
		if len(results) >= maxResultsPerRun {
			break
		}
		value := byName[name].Value
		var serialized string
		if value.Type == tektonv1.ParamTypeString {
			serialized = value.StringVal
		} else {
			raw, err := json.Marshal(value)
			if err != nil {
				continue
			}
			serialized = string(raw)
		}
		if len(serialized) > maxResultSize {
			continue
		}
		results[name] = serialized
	}
	return results
}

// BuildRunsMap builds the CEL `runs` value keyed by dependency name; a
// dependency without a state maps to Error.
func BuildRunsMap(deps []string, states map[string]RunState) map[string]any {
	runs := map[string]any{}
	for _, dep := range deps {
		state, ok := states[dep]
		if !ok {
			state = missingRunState()
		}
		results := map[string]any{}
		for k, v := range state.Results {
			results[k] = v
		}
		runs[dep] = map[string]any{
			"name":    state.Name,
			"status":  state.Status,
			"reason":  state.Reason,
			"results": results,
		}
	}
	return runs
}
