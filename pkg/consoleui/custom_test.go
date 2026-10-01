package consoleui

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCustomGood(t *testing.T) {
	consoleName := "MyCorp Console"
	consoleURL := "https://mycorp.console"
	consolePRdetail := "https://mycorp.console/{{ namespace }}/{{ pr }}/params/{{ foo }}"
	consolePRtasklog := "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ task }}/{{ pod }}/{{ firstFailedStep }}/params/{{ foo }}/{{ nonewline }}"

	c := NewCustomConsole(&info.PacOpts{
		Settings: settings.Settings{
			CustomConsoleName:      consoleName,
			CustomConsoleURL:       consoleURL,
			CustomConsolePRdetail:  consolePRdetail,
			CustomConsolePRTaskLog: consolePRtasklog,
		},
	}).WithParams(map[string]string{
		"foo":       "bar",
		"nonewline": "nonewline\n ",
	})
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "pr",
		},
	}
	trStatus := &tektonv1.PipelineRunTaskRunStatus{
		PipelineTaskName: "task",
		Status: &tektonv1.TaskRunStatus{
			TaskRunStatusFields: tektonv1.TaskRunStatusFields{
				PodName: "pod",
				Steps: []tektonv1.StepState{
					{
						Name: "failure",
						ContainerState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
							},
						},
					},
					{
						Name: "nextFailure",
						ContainerState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
							},
						},
					},
				},
			},
		},
	}
	assert.Equal(t, c.GetName(), consoleName)
	assert.Equal(t, c.URL(), consoleURL)
	assert.Equal(t, c.DetailURL(pr), "https://mycorp.console/ns/pr/params/bar")
	assert.Equal(t, c.TaskLogURL(pr, trStatus), "https://mycorp.console/ns/pr/task/pod/failure/params/bar/nonewline")

	scoped, ok := c.WithParams(map[string]string{"foo": "scoped"}).(*CustomConsole)
	assert.Assert(t, ok, "WithParams should return a *CustomConsole")
	assert.Equal(t, scoped.DetailURL(pr), "https://mycorp.console/ns/pr/params/scoped")
	assert.Equal(t, c.DetailURL(pr), "https://mycorp.console/ns/pr/params/bar")

	// test if we fallback properly
	f := NewCustomConsole(&info.PacOpts{
		Settings: settings.Settings{
			CustomConsoleName:      consoleName,
			CustomConsoleURL:       consoleURL,
			CustomConsolePRdetail:  "{{ notthere}}",
			CustomConsolePRTaskLog: "{{ notthere}}",
		},
	}).WithParams(map[string]string{})
	assert.Assert(t, strings.Contains(f.DetailURL(pr), consoleURL))
	assert.Assert(t, strings.Contains(f.TaskLogURL(pr, trStatus), consoleURL))

	o := NewCustomConsole(&info.PacOpts{
		Settings: settings.Settings{
			CustomConsoleName:         consoleName,
			CustomConsoleURL:          consoleURL,
			CustomConsolePRdetail:     "{{ notthere}}",
			CustomConsolePRTaskLog:    "{{ notthere}}",
			CustomConsoleNamespaceURL: "https://mycorp.console/{{ namespace }}",
		},
	})
	assert.Assert(t, strings.Contains(o.DetailURL(pr), consoleURL))
	assert.Assert(t, strings.Contains(o.TaskLogURL(pr, trStatus), consoleURL))
	assert.Assert(t, strings.Contains(o.NamespaceURL(pr), "https://mycorp.console/ns"))
}

func TestCustomStepLogURL(t *testing.T) {
	consoleURL := "https://mycorp.console"
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pr"},
	}
	trStatus := &tektonv1.PipelineRunTaskRunStatus{
		PipelineTaskName: "task",
		Status: &tektonv1.TaskRunStatus{
			TaskRunStatusFields: tektonv1.TaskRunStatusFields{PodName: "pod"},
		},
	}

	c := NewCustomConsole(&info.PacOpts{
		Settings: settings.Settings{
			CustomConsoleURL:       consoleURL,
			CustomConsolePRTaskLog: "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ task }}",
			CustomConsolePRStepLog: "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ task }}/{{ step }}",
		},
	})
	assert.Equal(t, c.StepLogURL(pr, trStatus, "unit-tests"), "https://mycorp.console/ns/pr/task/unit-tests")
	// no step name falls back to the task log url
	assert.Equal(t, c.StepLogURL(pr, trStatus, ""), "https://mycorp.console/ns/pr/task")
	// the step of a previous call does not leak on the task log url
	assert.Equal(t, c.TaskLogURL(pr, trStatus), "https://mycorp.console/ns/pr/task")

	// no step template configured falls back to the task log url
	noStep := NewCustomConsole(&info.PacOpts{
		Settings: settings.Settings{
			CustomConsoleURL:       consoleURL,
			CustomConsolePRTaskLog: "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ task }}",
		},
	})
	assert.Equal(t, noStep.StepLogURL(pr, trStatus, "unit-tests"), "https://mycorp.console/ns/pr/task")

	// the console is shared by concurrent handlers, a step URL must not pick
	// up the step of another call
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			step := fmt.Sprintf("step-%d", i)
			assert.Check(t, c.StepLogURL(pr, trStatus, step) == "https://mycorp.console/ns/pr/task/"+step)
		}()
	}
	wg.Wait()
}

func TestCustomBad(t *testing.T) {
	c := NewCustomConsole(&info.PacOpts{Settings: settings.Settings{}})
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "pr",
		},
	}
	assert.Assert(t, strings.Contains(c.GetName(), "is.not.configured"))
	assert.Assert(t, strings.Contains(c.URL(), "is.not.configured"))
	assert.Assert(t, strings.Contains(c.DetailURL(pr), "is.not.configured"))
	assert.Assert(t, strings.Contains(c.TaskLogURL(pr, nil), "is.not.configured"))
	assert.Assert(t, strings.Contains(c.StepLogURL(pr, nil, "step"), "is.not.configured"))
	assert.Assert(t, strings.Contains(c.NamespaceURL(pr), "is.not.configured"), c.NamespaceURL(pr))
}

func TestCustomConcurrentURLs(t *testing.T) {
	c := NewCustomConsole(&info.PacOpts{
		Settings: settings.Settings{
			CustomConsoleName:         "MyCorp Console",
			CustomConsoleURL:          "https://mycorp.console",
			CustomConsolePRdetail:     "https://mycorp.console/{{ namespace }}/{{ pr }}",
			CustomConsoleNamespaceURL: "https://mycorp.console/{{ namespace }}",
			CustomConsolePRTaskLog:    "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ task }}/{{ pod }}/{{ firstFailedStep }}",
		},
	}).WithParams(map[string]string{"foo": "bar"})

	const workers = 16
	const iterations = 25

	type result struct {
		got  string
		want string
	}
	results := make([][]result, workers)
	start := make(chan struct{})
	wg := sync.WaitGroup{}
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ns := fmt.Sprintf("ns%d", i)
			prName := fmt.Sprintf("pr%d", i)
			task := fmt.Sprintf("task%d", i)
			pod := fmt.Sprintf("pod%d", i)
			step := fmt.Sprintf("step%d", i)
			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: prName},
			}
			trStatus := &tektonv1.PipelineRunTaskRunStatus{
				PipelineTaskName: task,
				Status: &tektonv1.TaskRunStatus{
					TaskRunStatusFields: tektonv1.TaskRunStatusFields{
						PodName: pod,
						Steps: []tektonv1.StepState{
							{
								Name: step,
								ContainerState: corev1.ContainerState{
									Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
								},
							},
						},
					},
				},
			}
			<-start
			for range iterations {
				results[i] = append(
					results[i],
					result{
						got:  c.DetailURL(pr),
						want: fmt.Sprintf("https://mycorp.console/%s/%s", ns, prName),
					},
					result{
						got:  c.NamespaceURL(pr),
						want: fmt.Sprintf("https://mycorp.console/%s", ns),
					},
					result{
						got:  c.TaskLogURL(pr, trStatus),
						want: fmt.Sprintf("https://mycorp.console/%s/%s/%s/%s/%s", ns, prName, task, pod, step),
					},
				)
			}
		}()
	}
	close(start)
	wg.Wait()

	for _, worker := range results {
		for _, res := range worker {
			assert.Equal(t, res.want, res.got)
		}
	}
}

func TestCustomConcurrentWithParams(t *testing.T) {
	base := NewCustomConsole(&info.PacOpts{
		Settings: settings.Settings{
			CustomConsoleURL:      "https://mycorp.console",
			CustomConsolePRdetail: "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ foo }}",
		},
	})
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pr"},
	}

	const workers = 8
	const iterations = 50
	got := make([][]string, workers)
	want := make([]string, workers)
	start := make(chan struct{})
	wg := sync.WaitGroup{}
	for i := range workers {
		want[i] = fmt.Sprintf("https://mycorp.console/ns/pr/bar%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range iterations {
				scoped := base.WithParams(map[string]string{"foo": fmt.Sprintf("bar%d", i)})
				got[i] = append(got[i], scoped.DetailURL(pr))
			}
		}()
	}
	close(start)
	wg.Wait()

	for i, worker := range got {
		for _, url := range worker {
			assert.Equal(t, want[i], url)
		}
	}
	// the shared console keeps no parameters of its own
	assert.Equal(t, "https://mycorp.console", base.DetailURL(pr))
}

func TestCustomWithParamsIsolation(t *testing.T) {
	base := NewCustomConsole(&info.PacOpts{
		Settings: settings.Settings{
			CustomConsoleURL:      "https://mycorp.console",
			CustomConsolePRdetail: "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ foo }}",
		},
	})
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pr"},
	}

	first := base.WithParams(map[string]string{"foo": "one"})
	second := base.WithParams(map[string]string{"foo": "two"})

	assert.Equal(t, "https://mycorp.console/ns/pr/one", first.DetailURL(pr))
	assert.Equal(t, "https://mycorp.console/ns/pr/two", second.DetailURL(pr))
	// a scoped console is not affected by a later one
	assert.Equal(t, "https://mycorp.console/ns/pr/one", first.DetailURL(pr))
	// an unresolved parameter still falls back to the console URL
	assert.Equal(t, "https://mycorp.console", base.WithParams(nil).DetailURL(pr))
}

func TestCustomWithParamsCopiesMap(t *testing.T) {
	params := map[string]string{"foo": "bar"}
	c := NewCustomConsole(&info.PacOpts{
		Settings: settings.Settings{
			CustomConsoleURL:      "https://mycorp.console",
			CustomConsolePRdetail: "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ foo }}",
		},
	}).WithParams(params)
	params["foo"] = "mutated"

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pr"},
	}
	assert.Equal(t, "https://mycorp.console/ns/pr/bar", c.DetailURL(pr))
}

func TestCustomTaskLogURLFirstFailedStep(t *testing.T) {
	step := func(name string, exitCode int32) tektonv1.StepState {
		return tektonv1.StepState{
			Name: name,
			ContainerState: corev1.ContainerState{
				Terminated: &corev1.ContainerStateTerminated{ExitCode: exitCode},
			},
		}
	}
	tests := []struct {
		name          string
		taskRunStatus *tektonv1.PipelineRunTaskRunStatus
		want          string
	}{
		{
			name: "no failed step",
			taskRunStatus: &tektonv1.PipelineRunTaskRunStatus{
				PipelineTaskName: "task",
				Status: &tektonv1.TaskRunStatus{
					TaskRunStatusFields: tektonv1.TaskRunStatusFields{
						PodName: "pod",
						Steps:   []tektonv1.StepState{step("first", 0), step("second", 0)},
					},
				},
			},
			want: "https://mycorp.console/ns/pr/task/pod/",
		},
		{
			name: "failure on a later step",
			taskRunStatus: &tektonv1.PipelineRunTaskRunStatus{
				PipelineTaskName: "task",
				Status: &tektonv1.TaskRunStatus{
					TaskRunStatusFields: tektonv1.TaskRunStatusFields{
						PodName: "pod",
						Steps:   []tektonv1.StepState{step("first", 0), step("second", 2), step("third", 1)},
					},
				},
			},
			want: "https://mycorp.console/ns/pr/task/pod/second",
		},
		{
			name: "running step is not a failure",
			taskRunStatus: &tektonv1.PipelineRunTaskRunStatus{
				PipelineTaskName: "task",
				Status: &tektonv1.TaskRunStatus{
					TaskRunStatusFields: tektonv1.TaskRunStatusFields{
						PodName: "pod",
						Steps:   []tektonv1.StepState{{Name: "running"}, step("failed", 1)},
					},
				},
			},
			want: "https://mycorp.console/ns/pr/task/pod/failed",
		},
		{
			name:          "no taskrun status",
			taskRunStatus: nil,
			want:          "https://mycorp.console/ns/pr///",
		},
		{
			name:          "taskrun status without status field",
			taskRunStatus: &tektonv1.PipelineRunTaskRunStatus{PipelineTaskName: "task"},
			want:          "https://mycorp.console/ns/pr/task//",
		},
	}

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCustomConsole(&info.PacOpts{
				Settings: settings.Settings{
					CustomConsoleURL:       "https://mycorp.console",
					CustomConsolePRTaskLog: "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ task }}/{{ pod }}/{{ firstFailedStep }}",
				},
			})
			assert.Equal(t, tt.want, c.TaskLogURL(pr, tt.taskRunStatus))
		})
	}
}

func TestCustomURLsDoNotLeakAcrossCalls(t *testing.T) {
	c := NewCustomConsole(&info.PacOpts{
		Settings: settings.Settings{
			CustomConsoleURL:          "https://mycorp.console",
			CustomConsolePRdetail:     "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ task }}/{{ pod }}/{{ firstFailedStep }}",
			CustomConsoleNamespaceURL: "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ task }}",
			CustomConsolePRTaskLog:    "https://mycorp.console/{{ namespace }}/{{ pr }}/{{ task }}/{{ pod }}/{{ firstFailedStep }}",
		},
	})
	first := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "pr1"},
	}
	trStatus := &tektonv1.PipelineRunTaskRunStatus{
		PipelineTaskName: "task1",
		Status: &tektonv1.TaskRunStatus{
			TaskRunStatusFields: tektonv1.TaskRunStatusFields{
				PodName: "pod1",
				Steps: []tektonv1.StepState{
					{
						Name: "step1",
						ContainerState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{ExitCode: 1},
						},
					},
				},
			},
		},
	}
	assert.Equal(t, "https://mycorp.console/ns1/pr1/task1/pod1/step1", c.TaskLogURL(first, trStatus))

	second := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns2", Name: "pr2"},
	}
	assert.Equal(t, "https://mycorp.console/ns2/pr2///", c.DetailURL(second))
	assert.Equal(t, "https://mycorp.console/ns2/pr2/", c.NamespaceURL(second))
}

func TestCustomConsoleSnapshotsSettings(t *testing.T) {
	pacInfo := &info.PacOpts{
		Settings: settings.Settings{
			CustomConsoleURL:      "https://mycorp.console",
			CustomConsolePRdetail: "https://mycorp.console/{{ namespace }}/{{ pr }}",
		},
	}
	c := NewCustomConsole(pacInfo)
	pacInfo.CustomConsolePRdetail = "https://elsewhere.console/{{ namespace }}"
	pacInfo.CustomConsoleURL = "https://elsewhere.console"

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "pr"},
	}
	assert.Equal(t, "https://mycorp.console/ns/pr", c.DetailURL(pr))
	assert.Equal(t, "https://mycorp.console/ns/pr", c.WithParams(nil).DetailURL(pr))
}
