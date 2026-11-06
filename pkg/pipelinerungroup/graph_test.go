package pipelinerungroup

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseDependsOn(t *testing.T) {
	tests := []struct {
		name       string
		annotation string
		expected   []string
		wantErr    string
	}{
		{
			name:       "empty annotation",
			annotation: "",
			expected:   nil,
		},
		{
			name:       "single dependency",
			annotation: `["build"]`,
			expected:   []string{"build"},
		},
		{
			name:       "multiple dependencies keep their order",
			annotation: `["build", "tests"]`,
			expected:   []string{"build", "tests"},
		},
		{
			name:       "duplicates are removed",
			annotation: `["build", "build", "tests"]`,
			expected:   []string{"build", "tests"},
		},
		{
			name:       "names are trimmed",
			annotation: `[" build "]`,
			expected:   []string{"build"},
		},
		{
			name:       "not a json array",
			annotation: `{"build": true}`,
			wantErr:    "expected a JSON array",
		},
		{
			name:       "empty definition name",
			annotation: `[""]`,
			wantErr:    "empty definition name",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps, err := ParseDependsOn(tt.annotation)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, tt.expected, deps)
		})
	}
}

func TestDefinitionName(t *testing.T) {
	tests := []struct {
		name     string
		pr       *tektonv1.PipelineRun
		expected string
	}{
		{
			name: "original prname annotation wins",
			pr: &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
				Name: "generated-abc",
				Annotations: map[string]string{
					keys.OriginalPRName: "build",
				},
			}},
			expected: "build",
		},
		{
			name: "falls back to the name",
			pr: &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
				Name: "build",
			}},
			expected: "build",
		},
		{
			name: "falls back to the generate name without its suffix",
			pr: &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
				GenerateName: "build-",
			}},
			expected: "build",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, DefinitionName(tt.pr))
		})
	}
}

// prTemplate builds a definition template with an original-prname annotation,
// like the ones MetadataResolve produces.
func prTemplate(name, onPipelineRun, cel string) *tektonv1.PipelineRun {
	annotations := map[string]string{keys.OriginalPRName: name}
	if onPipelineRun != "" {
		annotations[keys.OnPipelineRun] = onPipelineRun
	}
	if cel != "" {
		annotations[keys.OnCelExpression] = cel
	}
	return &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
		Name: name, Annotations: annotations,
	}}
}

func TestDefinitionsFromPRs(t *testing.T) {
	tests := []struct {
		name        string
		prs         []*tektonv1.PipelineRun
		wantDefs    []Definition
		wantErrName string
		wantErrMsg  string
	}{
		{
			name: "plain and follow-up definitions",
			prs: []*tektonv1.PipelineRun{
				prTemplate("build", "", ""),
				prTemplate("deploy", `["build"]`, "runs.build.status == 'Succeeded'"),
			},
			wantDefs: []Definition{
				{Name: "build"},
				{Name: "deploy", DependsOn: []string{"build"}, CEL: "runs.build.status == 'Succeeded'"},
			},
		},
		{
			name: "bad on-pipelinerun annotation",
			prs: []*tektonv1.PipelineRun{
				prTemplate("deploy", "not-json", ""),
			},
			wantErrName: "deploy",
			wantErrMsg:  "invalid",
		},
		{
			name: "on-pipelinerun listing no dependency",
			prs: []*tektonv1.PipelineRun{
				prTemplate("deploy", "[]", ""),
			},
			wantErrName: "deploy",
			wantErrMsg:  "at least one definition",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defs, errs := DefinitionsFromPRs(tt.prs)
			if tt.wantErrName != "" {
				assert.Assert(t, len(errs) == 1, "expected one error, got %d", len(errs))
				assert.Equal(t, tt.wantErrName, errs[0].Name)
				assert.ErrorContains(t, errs[0].Err, tt.wantErrMsg)
				return
			}
			assert.Assert(t, len(errs) == 0, "expected no errors, got %v", errs)
			assert.DeepEqual(t, tt.wantDefs, defs)
		})
	}
}

func TestPlanFollowUps(t *testing.T) {
	tests := []struct {
		name            string
		defs            []Definition
		started         map[string]bool
		expectedCreate  []string
		expectedSkip    []string
		expectedProblem []ProblemKind
	}{
		{
			name: "no follow-ups",
			defs: []Definition{
				{Name: "build"}, {Name: "tests"},
			},
			started: map[string]bool{"build": true, "tests": true},
		},
		{
			name: "follow-up with all dependencies started",
			defs: []Definition{
				{Name: "build"},
				{Name: "tests"},
				{Name: "deploy", DependsOn: []string{"build", "tests"}, CEL: "runs.build.status == 'Succeeded'"},
			},
			started:        map[string]bool{"build": true, "tests": true, "deploy": true},
			expectedCreate: []string{"deploy"},
		},
		{
			name: "chained follow-ups are created by the fixpoint",
			defs: []Definition{
				{Name: "build"},
				{Name: "tests", DependsOn: []string{"build"}, CEL: "true"},
				{Name: "deploy", DependsOn: []string{"tests"}, CEL: "true"},
			},
			started:        map[string]bool{"build": true},
			expectedCreate: []string{"deploy", "tests"},
		},
		{
			name: "dependency not started skips the follow-up",
			defs: []Definition{
				{Name: "build"},
				{Name: "tests"},
				{Name: "deploy", DependsOn: []string{"build"}, CEL: "true"},
			},
			started:      map[string]bool{"tests": true},
			expectedSkip: []string{"deploy"},
		},
		{
			name: "a skip propagates up the chain",
			defs: []Definition{
				{Name: "build"},
				{Name: "tests", DependsOn: []string{"build"}, CEL: "true"},
				{Name: "deploy", DependsOn: []string{"tests"}, CEL: "true"},
			},
			started:      map[string]bool{},
			expectedSkip: []string{"tests", "deploy"},
		},
		{
			name: "self reference is fatal",
			defs: []Definition{
				{Name: "deploy", DependsOn: []string{"deploy"}, CEL: "true"},
			},
			started:         map[string]bool{},
			expectedProblem: []ProblemKind{ProblemSelfReference},
		},
		{
			name: "cycle is fatal",
			defs: []Definition{
				{Name: "build", DependsOn: []string{"deploy"}, CEL: "true"},
				{Name: "deploy", DependsOn: []string{"build"}, CEL: "true"},
			},
			started:         map[string]bool{},
			expectedProblem: []ProblemKind{ProblemCycle, ProblemCycle},
		},
		{
			name: "a definition reaching a cycle is not on it",
			defs: []Definition{
				{Name: "build"},
				{Name: "check", DependsOn: []string{"build"}, CEL: "true"},
				{Name: "a", DependsOn: []string{"b"}, CEL: "true"},
				{Name: "b", DependsOn: []string{"a"}, CEL: "true"},
			},
			started:         map[string]bool{},
			expectedProblem: []ProblemKind{ProblemCycle, ProblemCycle},
		},
		{
			name: "missing dependency is fatal",
			defs: []Definition{
				{Name: "deploy", DependsOn: []string{"ghost"}, CEL: "true"},
			},
			started:         map[string]bool{},
			expectedProblem: []ProblemKind{ProblemMissingDependency},
		},
		{
			name: "missing cel expression is fatal",
			defs: []Definition{
				{Name: "build"},
				{Name: "deploy", DependsOn: []string{"build"}},
			},
			started:         map[string]bool{"build": true},
			expectedProblem: []ProblemKind{ProblemNoCEL},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := PlanFollowUps(tt.defs, tt.started)

			var problems []ProblemKind
			for _, problem := range plan.Problems {
				problems = append(problems, problem.Kind)
			}
			assert.DeepEqual(t, tt.expectedProblem, problems)

			var skips []string
			for _, problem := range plan.Skip {
				skips = append(skips, problem.Name)
			}
			assert.DeepEqual(t, tt.expectedSkip, skips)
			assert.DeepEqual(t, tt.expectedCreate, plan.Create)
		})
	}
}
