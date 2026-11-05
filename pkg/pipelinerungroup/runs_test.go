package pipelinerungroup

import (
	"strings"
	"testing"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
)

// donePR builds a finished PipelineRun with the given condition.
func donePR(name string, status corev1.ConditionStatus, reason string) *tektonv1.PipelineRun {
	pr := prTemplate(name, "", "")
	pr.Name = name
	return withSucceeded(pr, status, reason)
}

func withSucceeded(pr *tektonv1.PipelineRun, status corev1.ConditionStatus, reason string) *tektonv1.PipelineRun {
	pr.Status.Conditions = []apis.Condition{{Type: apis.ConditionSucceeded, Status: status, Reason: reason}}
	return pr
}

func TestStatusFromPR(t *testing.T) {
	tests := []struct {
		name     string
		pr       *tektonv1.PipelineRun
		expected string
	}{
		{
			name:     "succeeded",
			pr:       donePR("build", corev1.ConditionTrue, string(tektonv1.PipelineRunReasonSuccessful)),
			expected: StatusSucceeded,
		},
		{
			name:     "failed",
			pr:       donePR("build", corev1.ConditionFalse, string(tektonv1.PipelineRunReasonFailed)),
			expected: StatusFailed,
		},
		{
			name:     "timeout counts as failed",
			pr:       donePR("build", corev1.ConditionFalse, string(tektonv1.PipelineRunReasonTimedOut)),
			expected: StatusFailed,
		},
		{
			name:     "cancelled",
			pr:       donePR("build", corev1.ConditionFalse, string(tektonv1.PipelineRunReasonCancelled)),
			expected: StatusCancelled,
		},
		{
			name:     "cancelled while running finally is a failure, like the provider check",
			pr:       donePR("build", corev1.ConditionFalse, string(tektonv1.PipelineRunReasonCancelledRunningFinally)),
			expected: StatusFailed,
		},
		{
			name:     "could not cancel is a failure",
			pr:       donePR("build", corev1.ConditionFalse, string(tektonv1.PipelineRunReasonCouldntCancel)),
			expected: StatusFailed,
		},
		{
			name:     "an unknown condition status is an error",
			pr:       donePR("build", corev1.ConditionUnknown, "Running"),
			expected: StatusError,
		},
		{
			name:     "a false condition with an empty reason is a failure",
			pr:       donePR("build", corev1.ConditionFalse, ""),
			expected: StatusFailed,
		},
		{
			name:     "no condition",
			pr:       prTemplate("build", "", ""),
			expected: StatusError,
		},
		{
			name:     "still running is not a status the resolver trusts",
			pr:       donePR("build", corev1.ConditionUnknown, string(tektonv1.PipelineRunReasonRunning)),
			expected: StatusError,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, StatusFromPR(tt.pr))
		})
	}
}

func TestRunStateFromPR(t *testing.T) {
	pr := donePR("build-abc", corev1.ConditionTrue, string(tektonv1.PipelineRunReasonSuccessful))
	pr.Status.Results = []tektonv1.PipelineRunResult{
		{Name: "digest", Value: tektonv1.ResultValue{Type: tektonv1.ParamTypeString, StringVal: "sha256:abc"}},
	}

	state := RunStateFromPR(pr)
	assert.Equal(t, "build-abc", state.Name)
	assert.Equal(t, StatusSucceeded, state.Status)
	assert.Equal(t, string(tektonv1.PipelineRunReasonSuccessful), state.Reason)
	assert.Equal(t, "sha256:abc", state.Results["digest"])
}

func TestResultsFromPR(t *testing.T) {
	tests := []struct {
		name     string
		results  []tektonv1.PipelineRunResult
		expected map[string]string
	}{
		{
			name: "string results are kept raw",
			results: []tektonv1.PipelineRunResult{
				{Name: "digest", Value: tektonv1.ResultValue{Type: tektonv1.ParamTypeString, StringVal: "sha256:abc"}},
			},
			expected: map[string]string{"digest": "sha256:abc"},
		},
		{
			name: "array results are json encoded",
			results: []tektonv1.PipelineRunResult{
				{Name: "images", Value: tektonv1.ResultValue{
					Type:     tektonv1.ParamTypeArray,
					ArrayVal: []string{"a", "b"},
				}},
			},
			expected: map[string]string{"images": `["a","b"]`},
		},
		{
			name: "object results are json encoded",
			results: []tektonv1.PipelineRunResult{
				{Name: "info", Value: tektonv1.ResultValue{
					Type:      tektonv1.ParamTypeObject,
					ObjectVal: map[string]string{"k": "v"},
				}},
			},
			expected: map[string]string{"info": `{"k":"v"}`},
		},
		{
			name: "hostile result values are data, not templates",
			results: []tektonv1.PipelineRunResult{
				{Name: "evil", Value: tektonv1.ResultValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{ runs.build.status }}",
				}},
			},
			expected: map[string]string{"evil": "{{ runs.build.status }}"},
		},
		{
			name: "a result of exactly the maximum size is kept",
			results: []tektonv1.PipelineRunResult{
				{Name: "edge", Value: tektonv1.ResultValue{
					Type: tektonv1.ParamTypeString, StringVal: strings.Repeat("a", maxResultSize),
				}},
			},
			expected: map[string]string{"edge": strings.Repeat("a", maxResultSize)},
		},
		{
			name: "an oversized array result is dropped once encoded",
			results: []tektonv1.PipelineRunResult{
				{Name: "list", Value: tektonv1.ResultValue{
					Type: tektonv1.ParamTypeArray, ArrayVal: []string{strings.Repeat("a", maxResultSize)},
				}},
			},
			expected: map[string]string{},
		},
		{
			name: "an empty and a unicode result are kept",
			results: []tektonv1.PipelineRunResult{
				{Name: "empty", Value: tektonv1.ResultValue{Type: tektonv1.ParamTypeString}},
				{Name: "uni", Value: tektonv1.ResultValue{Type: tektonv1.ParamTypeString, StringVal: "héllo ✓"}},
			},
			expected: map[string]string{"empty": "", "uni": "héllo ✓"},
		},
		{
			name:     "a pipelinerun without results has none",
			expected: map[string]string{},
		},
		{
			name: "oversized results are dropped",
			results: []tektonv1.PipelineRunResult{
				{Name: "big", Value: tektonv1.ResultValue{
					Type: tektonv1.ParamTypeString, StringVal: strings.Repeat("a", maxResultSize+1),
				}},
			},
			expected: map[string]string{},
		},
		{
			name: "too many results are truncated",
			results: func() []tektonv1.PipelineRunResult {
				results := make([]tektonv1.PipelineRunResult, 0, maxResultsPerRun+5)
				for i := 0; i < maxResultsPerRun+5; i++ {
					results = append(results, tektonv1.PipelineRunResult{
						Name:  string(rune('a'+i%26)) + strings.Repeat("a", i/26+1),
						Value: tektonv1.ResultValue{Type: tektonv1.ParamTypeString, StringVal: "v"},
					})
				}
				return results
			}(),
			expected: nil, // asserted below: exactly maxResultsPerRun entries
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := donePR("build", corev1.ConditionTrue, "Succeeded")
			pr.Status.Results = tt.results
			got := ResultsFromPR(pr)
			if tt.expected != nil {
				assert.DeepEqual(t, tt.expected, got)
				return
			}
			assert.Equal(t, maxResultsPerRun, len(got))
		})
	}
}

func TestBuildRunsMap(t *testing.T) {
	states := map[string]RunState{
		"build": {
			Name:    "build-abc",
			Status:  StatusSucceeded,
			Reason:  "Succeeded",
			Results: map[string]string{"digest": "sha256:abc"},
		},
	}
	runs := BuildRunsMap([]string{"build", "ghost"}, states)

	build, ok := runs["build"].(map[string]any)
	assert.Assert(t, ok, "build entry is not a map")
	assert.Equal(t, "build-abc", build["name"])
	assert.Equal(t, StatusSucceeded, build["status"])
	assert.Equal(t, "Succeeded", build["reason"])
	buildResults, ok := build["results"].(map[string]any)
	assert.Assert(t, ok, "build results is not a map")
	assert.Equal(t, "sha256:abc", buildResults["digest"])

	ghost, ok := runs["ghost"].(map[string]any)
	assert.Assert(t, ok, "ghost entry is not a map")
	assert.Equal(t, StatusError, ghost["status"])
	assert.Equal(t, ReasonMissing, ghost["reason"])
}
