package sort

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
)

func terminatedStep(name string, exitCode int32, reason string) tektonv1.StepState {
	start := metav1.Time{Time: time.Date(2024, 7, 4, 10, 0, 0, 0, time.UTC)}
	return tektonv1.StepState{
		Name:              name,
		TerminationReason: reason,
		ContainerState: corev1.ContainerState{
			Terminated: &corev1.ContainerStateTerminated{
				ExitCode:   exitCode,
				StartedAt:  start,
				FinishedAt: metav1.Time{Time: start.Add(30 * time.Second)},
			},
		},
	}
}

func taskRunStatusWithSteps(steps []tektonv1.StepState, specSteps []tektonv1.Step) *tektonv1.PipelineRunTaskRunStatus {
	return &tektonv1.PipelineRunTaskRunStatus{
		PipelineTaskName: "task",
		Status: &tektonv1.TaskRunStatus{
			Status: knativeduckv1.Status{
				Conditions: knativeduckv1.Conditions{{Status: corev1.ConditionTrue}},
			},
			TaskRunStatusFields: tektonv1.TaskRunStatusFields{
				StartTime:      &metav1.Time{Time: time.Date(2024, 7, 4, 10, 0, 0, 0, time.UTC)},
				CompletionTime: &metav1.Time{Time: time.Date(2024, 7, 4, 10, 5, 0, 0, time.UTC)},
				Steps:          steps,
				TaskSpec:       &tektonv1.TaskSpec{Steps: specSteps},
			},
		},
	}
}

func TestCollectSteps(t *testing.T) {
	tests := []struct {
		name            string
		taskRunStatus   *tektonv1.PipelineRunTaskRunStatus
		skipEmoji       bool
		wantNames       []string
		wantTitles      []string
		wantStatuses    []string
		wantStepActions []bool
	}{
		{
			name:          "nil status",
			taskRunStatus: &tektonv1.PipelineRunTaskRunStatus{PipelineTaskName: "task"},
		},
		{
			name: "single step is not broken down",
			taskRunStatus: taskRunStatusWithSteps(
				[]tektonv1.StepState{terminatedStep("only", 0, "Completed")},
				[]tektonv1.Step{{Name: "only"}},
			),
		},
		{
			name: "multiple steps with display names and stepactions",
			taskRunStatus: taskRunStatusWithSteps(
				[]tektonv1.StepState{
					terminatedStep("lint", 0, "Completed"),
					terminatedStep("test", 1, "Error"),
				},
				[]tektonv1.Step{
					{Name: "lint", DisplayName: "Linting", Ref: &tektonv1.Ref{Name: "golangci"}},
					{Name: "test"},
				},
			),
			wantNames:       []string{"lint", "test"},
			wantTitles:      []string{"Linting", "test"},
			wantStatuses:    []string{"🟢 Succeeded", "🔴 Failed"},
			wantStepActions: []bool{true, false},
		},
		{
			name: "skipped steps are not shown",
			taskRunStatus: taskRunStatusWithSteps(
				[]tektonv1.StepState{
					terminatedStep("first", 0, "Completed"),
					terminatedStep("failing", 1, "Error"),
					terminatedStep("never-ran", 0, "Skipped"),
				},
				[]tektonv1.Step{{Name: "first"}, {Name: "failing"}, {Name: "never-ran"}},
			),
			wantNames:       []string{"first", "failing"},
			wantTitles:      []string{"first", "failing"},
			wantStatuses:    []string{"🟢 Succeeded", "🔴 Failed"},
			wantStepActions: []bool{false, false},
		},
		{
			name: "a failing step is shown even when every other step was skipped",
			taskRunStatus: taskRunStatusWithSteps(
				[]tektonv1.StepState{
					terminatedStep("task-fail", 1, "Error"),
					terminatedStep("task-good", 0, "Skipped"),
				},
				[]tektonv1.Step{{Name: "task-fail"}, {Name: "task-good"}},
			),
			wantNames:       []string{"task-fail"},
			wantTitles:      []string{"task-fail"},
			wantStatuses:    []string{"🔴 Failed"},
			wantStepActions: []bool{false},
		},
		{
			name: "skip emoji",
			taskRunStatus: taskRunStatusWithSteps(
				[]tektonv1.StepState{
					terminatedStep("lint", 0, "Completed"),
					terminatedStep("test", 1, "Error"),
				},
				[]tektonv1.Step{{Name: "lint"}, {Name: "test"}},
			),
			skipEmoji:       true,
			wantNames:       []string{"lint", "test"},
			wantTitles:      []string{"lint", "test"},
			wantStatuses:    []string{"Succeeded", "Failed"},
			wantStepActions: []bool{false, false},
		},
		{
			name: "step prefix is hidden from the title",
			taskRunStatus: taskRunStatusWithSteps(
				[]tektonv1.StepState{
					terminatedStep("step-lint", 0, "Completed"),
					terminatedStep("step-", 0, "Completed"),
				},
				[]tektonv1.Step{{Name: "step-lint"}, {Name: "step-"}},
			),
			wantNames:       []string{"step-lint", "step-"},
			wantTitles:      []string{"lint", "step-"},
			wantStatuses:    []string{"🟢 Succeeded", "🟢 Succeeded"},
			wantStepActions: []bool{false, false},
		},
		{
			name: "unnamed steps are matched on their position",
			taskRunStatus: taskRunStatusWithSteps(
				[]tektonv1.StepState{
					terminatedStep("unnamed-0", 0, "Completed"),
					terminatedStep("unnamed-1", 0, "Completed"),
				},
				[]tektonv1.Step{{DisplayName: "First"}, {DisplayName: "Second"}},
			),
			wantNames:       []string{"unnamed-0", "unnamed-1"},
			wantTitles:      []string{"First", "Second"},
			wantStatuses:    []string{"🟢 Succeeded", "🟢 Succeeded"},
			wantStepActions: []bool{false, false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := collectSteps(tt.taskRunStatus, tt.skipEmoji, func(stepName string) string {
				return "https://console/step/" + stepName
			})
			assert.Equal(t, len(tt.wantNames), len(steps))
			for i, step := range steps {
				assert.Equal(t, tt.wantNames[i], step.Name)
				assert.Equal(t, tt.wantTitles[i], step.Title())
				assert.Equal(t, tt.wantStatuses[i], step.Status())
				assert.Equal(t, tt.wantStepActions[i], step.StepAction)
				assert.Equal(t, "30 seconds", step.Duration())
				assert.Equal(t, fmt.Sprintf("[%s](https://console/step/%s)", tt.wantTitles[i], tt.wantNames[i]), step.ConsoleLogURL())
			}
		})
	}
}

func TestCollectStepsDurationNotTerminated(t *testing.T) {
	trStatus := taskRunStatusWithSteps(
		[]tektonv1.StepState{
			terminatedStep("first", 0, "Completed"),
			{Name: "running", ContainerState: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			{Name: "waiting", ContainerState: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}}},
		},
		[]tektonv1.Step{{Name: "first"}, {Name: "running"}, {Name: "waiting"}},
	)

	steps := collectSteps(trStatus, false, func(string) string { return "https://console" })
	assert.Equal(t, 3, len(steps))
	assert.Equal(t, "🟡 Running", steps[1].Status())
	assert.Equal(t, "---", steps[1].Duration())
	assert.Equal(t, "🔄 Pending", steps[2].Status())
	assert.Equal(t, "---", steps[2].Duration())
}

func TestStepConsoleLogURLEscapesTitle(t *testing.T) {
	step := stepStatus{
		DisplayName: `build](/unexpected) | logs`,
		logURL:      "https://console/step/build",
	}

	assert.Equal(t, `[build\]\(/unexpected\) \| logs](https://console/step/build)`, step.ConsoleLogURL())
}

func TestEscapeMarkdownLinkText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "plain text",
			text: "build",
			want: "build",
		},
		{
			name: "square brackets",
			text: "build [linux]",
			want: `build \[linux\]`,
		},
		{
			name: "parentheses",
			text: "build (linux)",
			want: `build \(linux\)`,
		},
		{
			name: "pipe",
			text: "build | test",
			want: `build \| test`,
		},
		{
			name: "backslash",
			text: `build\test`,
			want: `build\\test`,
		},
		{
			name: "line breaks",
			text: "build\r\nlogs",
			want: "build logs",
		},
		{
			name: "combined markdown characters",
			text: `build [linux]\test (fast) | logs`,
			want: `build \[linux\]\\test \(fast\) \| logs`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, escapeMarkdownLinkText(tt.text))
		})
	}
}

func TestTaskStatusTmplWithSteps(t *testing.T) {
	tmpl := `{{- range $taskrun := .TaskRunList }}{{ $taskrun.ConsoleLogURL }}{{- range $step := $taskrun.Steps }}|{{ $step.Status }} {{ $step.ConsoleLogURL }}{{- end }}{{- end }}`
	trStatus := map[string]*tektonv1.PipelineRunTaskRunStatus{
		"task": taskRunStatusWithSteps(
			[]tektonv1.StepState{
				terminatedStep("lint", 0, "Completed"),
				terminatedStep("test", 1, "Error"),
			},
			[]tektonv1.Step{{Name: "lint"}, {Name: "test"}},
		),
	}

	tests := []struct {
		name       string
		showSteps  bool
		wantRegexp *regexp.Regexp
	}{
		{
			name:       "steps shown",
			showSteps:  true,
			wantRegexp: regexp.MustCompile(`task.*lint.*test`),
		},
		{
			name:       "steps disabled",
			showSteps:  false,
			wantRegexp: regexp.MustCompile(`^\[task\]\([^)]+\)$`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pacInfo := info.NewPacOpts()
			pacInfo.StatusShowSteps = tt.showSteps
			runs := params.New()
			runs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			output, err := TaskStatusTmpl(&tektonv1.PipelineRun{}, trStatus, runs, pacInfo, &info.ProviderConfig{TaskStatusTMPL: tmpl})
			assert.NilError(t, err)
			assert.Assert(t, tt.wantRegexp.MatchString(output), "%s != %s", output, tt.wantRegexp.String())
		})
	}
}

func TestTaskStatusTmplDropStepsWhenTooLarge(t *testing.T) {
	tmpl := `{{- range $taskrun := .TaskRunList }}{{ $taskrun.ConsoleLogURL }}{{- range $step := $taskrun.Steps }}{{ $step.ConsoleLogURL }}{{- end }}{{- end }}`
	longName := strings.Repeat("s", 1000)
	steps := make([]tektonv1.StepState, 0, 100)
	specSteps := make([]tektonv1.Step, 0, 100)
	for i := range 100 {
		name := fmt.Sprintf("%s-%d", longName, i)
		steps = append(steps, terminatedStep(name, 0, "Completed"))
		specSteps = append(specSteps, tektonv1.Step{Name: name})
	}
	trStatus := map[string]*tektonv1.PipelineRunTaskRunStatus{
		"task": taskRunStatusWithSteps(steps, specSteps),
	}

	pacInfo := info.NewPacOpts()
	runs := params.New()
	runs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
	output, err := TaskStatusTmpl(&tektonv1.PipelineRun{}, trStatus, runs, pacInfo, &info.ProviderConfig{TaskStatusTMPL: tmpl})
	assert.NilError(t, err)
	assert.Assert(t, len(output) < maxTaskStatusTextSize, "output should have been rendered without the steps, got %d bytes", len(output))
	assert.Assert(t, !strings.Contains(output, longName))
}
