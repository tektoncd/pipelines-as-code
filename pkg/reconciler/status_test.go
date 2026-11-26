package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	tprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestCreateStatusWithRetry(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	vcx := tprovider.TestProviderImp{}

	err := createStatusWithRetry(context.TODO(), fakelogger, &vcx, nil, providerstatus.StatusOpts{})
	assert.NilError(t, err)
}

func TestCreateStatusWithRetryErrorCase(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	vcx := tprovider.TestProviderImp{}
	vcx.CreateStatusErorring = true

	// Temporarily override backoffSchedule to speed up the test
	oldBackoffSchedule := backoffSchedule
	backoffSchedule = []time.Duration{time.Millisecond}
	defer func() { backoffSchedule = oldBackoffSchedule }()

	err := createStatusWithRetry(context.TODO(), fakelogger, &vcx, nil, providerstatus.StatusOpts{})
	assert.Error(t, err, "failed to report status: some provider error occurred while reporting status")
}

func TestPostFinalStatus(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	vcx := &tprovider.TestProviderImp{}

	labels := map[string]string{}
	ns := "namespace"
	clock := clockwork.NewFakeClock()
	pr1 := tektontest.MakePRCompletion(clock, "pipeline-newest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, labels, 10)
	pr1.Status.Conditions = append(pr1.Status.Conditions, apis.Condition{Status: "False", Message: "Hello not good", Type: "Succeeded", Reason: "CouldntGetTask"})
	ctx, _ := rtesting.SetupFakeContext(t)
	tdata := testclient.Data{PipelineRuns: []*tektonv1.PipelineRun{pr1}}
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)

	run := params.New()
	run.Clients = clients.Clients{
		Kube:   stdata.Kube,
		Tekton: stdata.Pipeline,
	}
	run.Clients.SetConsoleUI(consoleui.FallBackConsole{})

	r := &Reconciler{
		run: run,
	}
	pacInfo := &info.PacOpts{
		Settings: settings.Settings{
			ErrorLogSnippet: false,
		},
	}

	_, _, err := r.postFinalStatus(ctx, fakelogger, pacInfo, vcx, info.NewEvent(), pr1, run.Clients.ConsoleUI())
	assert.NilError(t, err)
}

func TestDetailURL(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		want        string
	}{
		{
			name:        "url recorded at start is reused",
			annotations: map[string]string{keys.LogURL: "https://mycorp.console/ns/pr/myparam"},
			want:        "https://mycorp.console/ns/pr/myparam",
		},
		{
			name:        "no annotation falls back to the console",
			annotations: nil,
			want:        "https://mycorp.console/ns/pr/{{ foo }}",
		},
		{
			name:        "empty annotation falls back to the console",
			annotations: map[string]string{keys.LogURL: ""},
			want:        "https://mycorp.console/ns/pr/{{ foo }}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := params.New()
			// a console whose template resolves everything, so the fallback is
			// visibly different from the recorded URL
			run.Clients.SetConsoleUI(&fakeDetailConsole{})
			r := &Reconciler{run: run}
			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "ns",
					Name:        "pr",
					Annotations: tt.annotations,
				},
			}
			assert.Equal(t, tt.want, r.detailURL(pr))
		})
	}
}

type fakeDetailConsole struct {
	consoleui.FallBackConsole
}

func (f *fakeDetailConsole) DetailURL(pr *tektonv1.PipelineRun) string {
	return "https://mycorp.console/" + pr.GetNamespace() + "/" + pr.GetName() + "/{{ foo }}"
}

func TestGetFailureSnippetShowsEveryFailedTask(t *testing.T) {
	const (
		proseBlock = "task <b>prose</b> has the status <b>\"Failed\"</b>:\n<pre>error: prose is not wrapped</pre>"
		linuxBlock = "task <b>linux-verify</b> has the status <b>\"Failed\"</b>:\n<pre>--- FAIL: TestBuild (2.01s)</pre>"
	)
	tests := []struct {
		name     string
		maxBytes int
		want     string
	}{
		{
			name:     "both failed tasks in completion order",
			maxBytes: maxCheckRunOutputTextBytes,
			want:     proseBlock + "\n" + linuxBlock,
		},
		{
			name:     "both fit exactly",
			maxBytes: len(proseBlock) + 1 + len(linuxBlock),
			want:     proseBlock + "\n" + linuxBlock,
		},
		{
			name:     "second task over the limit is counted",
			maxBytes: len(proseBlock) + len(linuxBlock),
			want:     proseBlock + "\nand 1 more failed task",
		},
		{
			name:     "first task is shown even over the limit",
			maxBytes: 10,
			want:     proseBlock + "\nand 1 more failed task",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := clockwork.NewFakeClock()
			ns := "namespace"
			pr := tektontest.MakePRCompletion(clock, "pipeline-run", ns, tektonv1.PipelineRunReasonFailed.String(), nil, map[string]string{}, 10)
			pr.Status.ChildReferences = []tektonv1.ChildStatusReference{
				{TypeMeta: runtime.TypeMeta{Kind: "TaskRun"}, Name: "tr-linux-verify", PipelineTaskName: "linux-verify"},
				{TypeMeta: runtime.TypeMeta{Kind: "TaskRun"}, Name: "tr-prose", PipelineTaskName: "prose"},
			}
			failedTaskRun := func(name string, timeshift int) *tektonv1.TaskRun {
				return tektontest.MakeTaskRunCompletion(clock, name, ns, "pipeline-run", map[string]string{},
					tektonv1.TaskRunStatusFields{
						PodName: name + "-pod",
						Steps: []tektonv1.StepState{{
							Name:           "step1",
							ContainerState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}},
						}},
					},
					knativeduckv1.Conditions{{
						Type:   apis.ConditionSucceeded,
						Status: corev1.ConditionFalse,
						Reason: tektonv1.TaskRunReasonFailed.String(),
					}},
					timeshift)
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pr},
				// prose finishes 9 minutes before linux-verify.
				TaskRuns: []*tektonv1.TaskRun{failedTaskRun("tr-linux-verify", 1), failedTaskRun("tr-prose", 10)},
			})
			run := params.New()
			run.Clients = clients.Clients{
				Kube:   stdata.Kube,
				Tekton: stdata.Pipeline,
				Log:    zap.NewNop().Sugar(),
			}
			r := &Reconciler{
				run: run,
				kinteract: &kubernetestint.KinterfaceTest{GetPodLogsOutput: map[string]string{
					"tr-prose-pod":        "error: prose is not wrapped\n",
					"tr-linux-verify-pod": "--- FAIL: TestBuild (2.01s)",
				}},
			}

			got := r.getFailureSnippet(ctx, pr, tt.maxBytes)
			assert.Equal(t, tt.want, got)
		})
	}
}
