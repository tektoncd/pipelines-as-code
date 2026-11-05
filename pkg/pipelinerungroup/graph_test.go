package pipelinerungroup

import (
	"slices"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/resolve"
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
		{
			name:       "a whitespace only name is empty",
			annotation: `["build", "   "]`,
			wantErr:    "empty definition name",
		},
		{
			name:       "duplicates differing only by whitespace are removed",
			annotation: `["build", " build"]`,
			expected:   []string{"build"},
		},
		{
			name:       "names are case sensitive",
			annotation: `["Build", "build"]`,
			expected:   []string{"Build", "build"},
		},
		{
			name:       "a bare string is not an array",
			annotation: `"build"`,
			wantErr:    "expected a JSON array",
		},
		{
			name:       "non string entries are rejected",
			annotation: `["build", 1]`,
			wantErr:    "expected a JSON array",
		},
		{
			name:       "nested arrays are rejected",
			annotation: `[["build"]]`,
			wantErr:    "expected a JSON array",
		},
		{
			name:       "an empty array lists nothing",
			annotation: `[]`,
			expected:   []string{},
		},
		{
			name:       "a json null lists nothing",
			annotation: `null`,
			expected:   []string{},
		},
		{
			name:       "a whitespace only annotation is empty",
			annotation: "  \n ",
			expected:   nil,
		},
		{
			name:       "a trailing comma is invalid json",
			annotation: `["build",]`,
			wantErr:    "expected a JSON array",
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
			name: "falls back to the generate name as is",
			pr: &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
				GenerateName: "build-",
			}},
			expected: "build-",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, DefinitionName(tt.pr))
		})
	}
}

// prTemplate builds a definition template with an original-prname annotation.
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

// followUp builds a follow-up definition with a condition.
func followUp(name string, deps ...string) Definition {
	return Definition{Name: name, DependsOn: deps, CEL: "true"}
}

func TestValidateGraph(t *testing.T) {
	tests := []struct {
		name     string
		defs     []Definition
		expected []Problem
	}{
		{
			name: "a valid graph has no problem",
			defs: []Definition{{Name: "build"}, {Name: "lint"}, followUp("tests", "build"), followUp("deploy", "build", "lint")},
		},
		{
			name:     "duplicate definition name",
			defs:     []Definition{{Name: "build"}, {Name: "build"}},
			expected: []Problem{{Name: "build", Kind: ProblemDuplicateName}},
		},
		{
			name:     "self reference",
			defs:     []Definition{followUp("deploy", "deploy")},
			expected: []Problem{{Name: "deploy", Kind: ProblemSelfReference}},
		},
		{
			name:     "a follow-up waiting on another follow-up is a chain",
			defs:     []Definition{{Name: "build"}, followUp("tests", "build"), followUp("deploy", "tests")},
			expected: []Problem{{Name: "deploy", Kind: ProblemChainedDependency}},
		},
		{
			name: "one follow-up can break several rules at once",
			defs: []Definition{
				{Name: "build"},
				followUp("tests", "build"),
				{Name: "deploy", DependsOn: []string{"deploy", "ghost", "tests", "build"}},
			},
			expected: []Problem{
				{Name: "deploy", Kind: ProblemSelfReference},
				{Name: "deploy", Kind: ProblemMissingDependency},
				{Name: "deploy", Kind: ProblemChainedDependency},
				{Name: "deploy", Kind: ProblemNoCEL},
			},
		},
		{
			name:     "two follow-ups with the same name are reported once",
			defs:     []Definition{{Name: "build"}, followUp("tests", "build"), followUp("tests", "build")},
			expected: []Problem{{Name: "tests", Kind: ProblemDuplicateName}},
		},
		{
			name:     "a follow-up sharing its name with a plain definition is a duplicate",
			defs:     []Definition{{Name: "build"}, followUp("build", "build")},
			expected: []Problem{{Name: "build", Kind: ProblemDuplicateName}, {Name: "build", Kind: ProblemSelfReference}},
		},
		{
			name: "a dependency listed twice is not a problem",
			defs: []Definition{{Name: "build"}, followUp("tests", "build", "build")},
		},
		{
			name:     "a follow-up name that changes under lowercasing is rejected",
			defs:     []Definition{{Name: "build"}, followUp("Deploy", "build")},
			expected: []Problem{{Name: "Deploy", Kind: ProblemCaseSensitiveName}},
		},
		{
			name: "no definitions has no problem",
		},
		{
			name:     "missing dependency",
			defs:     []Definition{followUp("deploy", "ghost")},
			expected: []Problem{{Name: "deploy", Kind: ProblemMissingDependency}},
		},
		{
			name:     "missing condition",
			defs:     []Definition{{Name: "build"}, {Name: "deploy", DependsOn: []string{"build"}}},
			expected: []Problem{{Name: "deploy", Kind: ProblemNoCEL}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := ValidateGraph(tt.defs)
			// the messages are for humans: compare who and why
			for i := range problems {
				problems[i].Msg = ""
			}
			assert.DeepEqual(t, tt.expected, problems)
		})
	}
}

func TestPlanFollowUps(t *testing.T) {
	pair := []Definition{{Name: "build"}, followUp("tests", "build")}

	tests := []struct {
		name    string
		defs    []Definition
		started []string
		create  []string
		skip    []string
	}{
		{
			name:    "plain definitions have nothing to plan",
			defs:    []Definition{{Name: "build"}, {Name: "tests"}},
			started: []string{"build", "tests"},
		},
		{
			name:    "a follow-up is planned when all its dependencies run",
			defs:    []Definition{{Name: "build"}, {Name: "tests"}, followUp("deploy", "build", "tests")},
			started: []string{"build", "tests"},
			create:  []string{"deploy"},
		},
		{
			name:    "a follow-up is skipped when a dependency does not run",
			defs:    []Definition{{Name: "build"}, {Name: "tests"}, followUp("deploy", "build", "tests")},
			started: []string{"tests"},
			skip:    []string{"deploy"},
		},
		{
			name: "a follow-up with no dependency running is not planned",
			defs: pair,
		},
		{
			name:    "a plain definition that is not started does not run its follow-ups",
			defs:    []Definition{{Name: "build"}, {Name: "tests"}, followUp("deploy", "tests"), followUp("report", "build")},
			started: []string{"build"},
			create:  []string{"report"},
		},
		{
			name:    "started names that are not definitions are ignored",
			defs:    pair,
			started: []string{"ghost"},
		},
		{
			name:    "a dependency listed twice counts once",
			defs:    []Definition{{Name: "build"}, followUp("tests", "build", "build")},
			started: []string{"build"},
			create:  []string{"tests"},
		},
		{
			name: "the plan is sorted whatever the definition order",
			defs: []Definition{
				followUp("zeta", "build"), followUp("alpha", "build"), {Name: "build"}, followUp("mid", "build"),
			},
			started: []string{"build"},
			create:  []string{"alpha", "mid", "zeta"},
		},
		{
			name:    "a follow-up with only some dependencies running is skipped",
			defs:    []Definition{{Name: "build"}, {Name: "lint"}, followUp("tests", "build", "lint")},
			started: []string{"build"},
			skip:    []string{"tests"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			started := map[string]bool{}
			for _, name := range tt.started {
				started[name] = true
			}
			plan := PlanFollowUps(tt.defs, started)

			assert.Equal(t, 0, len(plan.Problems))
			assert.DeepEqual(t, tt.create, plan.Create)
			var skipped []string
			for _, problem := range plan.Skip {
				assert.Equal(t, ProblemDependencyNotStarted, problem.Kind)
				skipped = append(skipped, problem.Name)
			}
			assert.DeepEqual(t, tt.skip, skipped)

			// no follow-up gets two outcomes
			outcomes := slices.Concat(plan.Create, skipped)
			slices.Sort(outcomes)
			assert.Equal(t, len(outcomes), len(slices.Compact(slices.Clone(outcomes))))
		})
	}
}

func TestPlanFollowUpsWithNothing(t *testing.T) {
	plan := PlanFollowUps(nil, nil)
	assert.DeepEqual(t, Plan{}, plan)
}

func TestPlanFollowUpsSkipReason(t *testing.T) {
	defs := []Definition{{Name: "build"}, {Name: "lint"}, {Name: "scan"}, followUp("deploy", "scan", "lint", "build")}
	plan := PlanFollowUps(defs, map[string]bool{"build": true})

	assert.Equal(t, 1, len(plan.Skip))
	assert.Equal(t, "dependency lint, scan did not run for this event", plan.Skip[0].Msg)
}

func TestPlanFollowUpsPlansNothingOnProblems(t *testing.T) {
	defs := []Definition{{Name: "build"}, followUp("tests", "build"), followUp("deploy", "ghost")}
	plan := PlanFollowUps(defs, map[string]bool{"build": true})

	assert.Equal(t, 1, len(plan.Problems))
	assert.Equal(t, ProblemMissingDependency, plan.Problems[0].Kind)
	assert.Equal(t, 0, len(plan.Create)+len(plan.Skip))
}

func TestDefinitionNameSurvivesMetadataResolution(t *testing.T) {
	pr := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{GenerateName: "build-"}}
	before := DefinitionName(pr)

	resolved, err := resolve.MetadataResolve([]*tektonv1.PipelineRun{pr})
	assert.NilError(t, err)
	assert.Equal(t, before, DefinitionName(resolved[0]))
}
