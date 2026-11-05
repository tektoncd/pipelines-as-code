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
	// ReasonMissing is the reason of a dependency that cannot be found in the
	// cluster anymore, pruned or never created.
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
	// Results holds the dependency's Pipeline results, stringified. Array and
	// object results are JSON encoded.
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

// missingRunState is the state of a dependency that cannot be found in the
// cluster anymore (pruned or never created): surfaced as Error so a condition
// like "all succeeded" goes false and the follow-up is skipped, not stuck.
func missingRunState() RunState {
	return RunState{Status: StatusError, Reason: ReasonMissing, Results: map[string]string{}}
}

// cancelledRunState is a dependency follow-up cancelled before its run was
// created.
func cancelledRunState() RunState {
	return RunState{Status: StatusCancelled, Reason: string(tektonv1.PipelineRunReasonCancelled), Results: map[string]string{}}
}

// StatusFromPR maps the PipelineRun Succeeded condition to a status string.
// A timeout counts as Failed, the Tekton reason keeps the detail.
func StatusFromPR(pr *tektonv1.PipelineRun) string {
	cond := pr.Status.GetCondition(apis.ConditionSucceeded)
	switch {
	case cond == nil || cond.Type == "":
		return StatusError
	case cond.Status == corev1.ConditionTrue:
		return StatusSucceeded
	case cond.Status == corev1.ConditionFalse:
		switch cond.Reason {
		case string(tektonv1.PipelineRunReasonCancelled),
			string(tektonv1.PipelineRunReasonCancelledRunningFinally),
			string(tektonv1.PipelineRunReasonCouldntCancel):
			return StatusCancelled
		}
		return StatusFailed
	default:
		// Unknown or still running: the caller treats it as not done, this is
		// only a defensive mapping.
		return StatusError
	}
}

// ResultsFromPR extracts the PipelineRun results as strings, with a basic
// guard on count and size: results come from pipeline code, which may be a
// pull request author's, so they are untrusted data.
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
			// Array and object results are JSON encoded.
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

// BuildRunsMap builds the CEL `runs` activation value: a map keyed by
// dependency definition name, each holding the run name, status, reason and
// results. A dependency without a state (missing or pruned run) maps to the
// Error status.
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
