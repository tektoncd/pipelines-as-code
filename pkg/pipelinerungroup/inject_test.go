package pipelinerungroup

import (
	"testing"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
)

// followUpWithParams builds a waiting follow-up with the given spec.params.
func followUpWithParams(params []tektonv1.Param) *tektonv1.PipelineRun {
	pr := prTemplate("deploy", `["build"]`, "true")
	pr.Spec.Params = params
	return pr
}

func TestInjectRunParams(t *testing.T) {
	states := map[string]RunState{
		"build": {
			Name:   "build-abc",
			Status: StatusSucceeded,
			Reason: "Succeeded",
			Results: map[string]string{
				"digest": "sha256:abc",
				"images": `["a","b"]`,
			},
		},
		"build.v2": {
			Name:    "build.v2-abc",
			Status:  StatusFailed,
			Results: map[string]string{"digest": "sha256:v2"},
		},
	}
	tests := []struct {
		name       string
		params     tektonv1.Params
		expected   tektonv1.Params
		unresolved []string
	}{
		{
			name: "string param",
			params: tektonv1.Params{{
				Name: "digest", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{ runs.build.results.digest }}",
				},
			}},
			expected: tektonv1.Params{{
				Name: "digest", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "sha256:abc",
				},
			}},
		},
		{
			name: "placeholder inside a larger string",
			params: tektonv1.Params{{
				Name: "image", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "registry/app@{{ runs.build.results.digest }}",
				},
			}},
			expected: tektonv1.Params{{
				Name: "image", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "registry/app@sha256:abc",
				},
			}},
		},
		{
			name: "array param entries",
			params: tektonv1.Params{{
				Name: "images", Value: tektonv1.ParamValue{
					Type:     tektonv1.ParamTypeArray,
					ArrayVal: []string{"{{ runs.build.name }}", "{{ runs.build.results.digest }}"},
				},
			}},
			expected: tektonv1.Params{{
				Name: "images", Value: tektonv1.ParamValue{
					Type:     tektonv1.ParamTypeArray,
					ArrayVal: []string{"build-abc", "sha256:abc"},
				},
			}},
		},
		{
			name: "object param values",
			params: tektonv1.Params{{
				Name: "info", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeObject,
					ObjectVal: map[string]string{
						"status": "{{ runs.build.status }}",
						"plain":  "untouched",
					},
				},
			}},
			expected: tektonv1.Params{{
				Name: "info", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeObject,
					ObjectVal: map[string]string{
						"status": "Succeeded",
						"plain":  "untouched",
					},
				},
			}},
		},
		{
			name: "status and reason fields",
			params: tektonv1.Params{{
				Name: "meta", Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "{{ runs.build.status }}/{{ runs.build.reason }}/{{ runs.build.name }}",
				},
			}},
			expected: tektonv1.Params{{
				Name: "meta", Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "Succeeded/Succeeded/build-abc",
				},
			}},
		},
		{
			name: "dotted dependency name",
			params: tektonv1.Params{{
				Name: "meta", Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "{{ runs.build.v2.name }}/{{ runs.build.v2.status }}/{{ runs.build.v2.results.digest }}/{{ runs.build.results.digest }}",
				},
			}},
			expected: tektonv1.Params{{
				Name: "meta", Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "build.v2-abc/Failed/sha256:v2/sha256:abc",
				},
			}},
		},
		{
			name: "unknown field is unresolved",
			params: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{ runs.build.v3.name }}",
				},
			}},
			unresolved: []string{"{{ runs.build.v3.name }}"},
		},
		{
			name: "unknown dependency is unresolved",
			params: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{ runs.ghost.results.digest }}",
				},
			}},
			unresolved: []string{"{{ runs.ghost.results.digest }}"},
		},
		{
			name: "placeholder spacing variants resolve",
			params: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{runs.build.status}}-{{   runs.build.reason   }}",
				},
			}},
			expected: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "Succeeded-Succeeded",
				},
			}},
		},
		{
			name: "a resolved and an unresolved placeholder in one value",
			params: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{ runs.build.status }}/{{ runs.build.results.ghost }}",
				},
			}},
			expected: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "Succeeded/{{ runs.build.results.ghost }}",
				},
			}},
			unresolved: []string{"{{ runs.build.results.ghost }}"},
		},
		{
			name: "the longest dependency name wins over its prefix",
			params: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{ runs.build.v2.results.digest }}",
				},
			}},
			expected: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "sha256:v2",
				},
			}},
		},
		{
			name: "a status named like a result is not mistaken for one",
			params: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{ runs.build.status.extra }}",
				},
			}},
			unresolved: []string{"{{ runs.build.status.extra }}"},
		},
		{
			name: "a non runs template is left alone",
			params: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{ revision }} {{ runs }} {{ runs. }} {{ runs.build",
				},
			}},
			expected: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{ revision }} {{ runs }} {{ runs. }} {{ runs.build",
				},
			}},
		},
		{
			name: "a pipelinerun without params has nothing to resolve",
		},
		{
			name: "unresolved references are sorted",
			params: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{ runs.build.results.zed }} {{ runs.build.results.alpha }}",
				},
			}},
			unresolved: []string{"{{ runs.build.results.alpha }}", "{{ runs.build.results.zed }}"},
		},
		{
			name: "unknown result is unresolved",
			params: tektonv1.Params{{
				Name: "x", Value: tektonv1.ParamValue{
					Type: tektonv1.ParamTypeString, StringVal: "{{ runs.build.results.ghost }}",
				},
			}},
			unresolved: []string{"{{ runs.build.results.ghost }}"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := followUpWithParams(tt.params)
			unresolved := InjectRunParams(pr, states)
			assert.DeepEqual(t, tt.unresolved, unresolved)
			if tt.expected != nil {
				assert.DeepEqual(t, tt.expected, pr.Spec.Params)
			}
		})
	}
}

func TestHostileResultIsInjectedAsData(t *testing.T) {
	// untrusted results must not add a second level of templating
	states := map[string]RunState{
		"build": {Results: map[string]string{"digest": "{{ secret }}"}},
	}
	pr := followUpWithParams(tektonv1.Params{{
		Name: "digest", Value: tektonv1.ParamValue{
			Type: tektonv1.ParamTypeString, StringVal: "{{ runs.build.results.digest }}",
		},
	}})
	unresolved := InjectRunParams(pr, states)
	assert.DeepEqual(t, []string(nil), unresolved)
	assert.Equal(t, "{{ secret }}", pr.Spec.Params[0].Value.StringVal)
}

func TestTektonSubstitutionInResultsIsInertData(t *testing.T) {
	states := map[string]RunState{
		"build": {Results: map[string]string{"x": "$(params.secret) $(tasks.t.results.r) {{ runs.build.results.x }}"}},
	}
	pr := followUpWithParams(tektonv1.Params{{
		Name: "p", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "{{ runs.build.results.x }}"},
	}})
	assert.Equal(t, 0, len(InjectRunParams(pr, states)))
	assert.Equal(t, "$(params.secret) $(tasks.t.results.r) {{ runs.build.results.x }}", pr.Spec.Params[0].Value.StringVal)
}

func TestDepsOf(t *testing.T) {
	pr := prTemplate("deploy", `["build", "tests"]`, "")
	deps, err := DepsOf(pr)
	assert.NilError(t, err)
	assert.DeepEqual(t, []string{"build", "tests"}, deps)

	plain := prTemplate("build", "", "")
	deps, err = DepsOf(plain)
	assert.NilError(t, err)
	assert.DeepEqual(t, []string(nil), deps)
}
