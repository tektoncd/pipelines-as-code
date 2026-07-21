package describe

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tcli "github.com/openshift-pipelines/pipelines-as-code/pkg/test/cli"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	knativeapis "knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func makePR(clock *clockwork.FakeClock, name, ns, reason string, conditionStatus corev1.ConditionStatus, annotations map[string]string, startShift, endShift time.Duration) *tektonv1.PipelineRun {
	return &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				keys.Repository: "test-run",
			},
			Annotations: annotations,
		},
		Status: tektonv1.PipelineRunStatus{
			Status: knativeduckv1.Status{
				Conditions: knativeduckv1.Conditions{
					{
						Type:   knativeapis.ConditionSucceeded,
						Status: conditionStatus,
						Reason: reason,
					},
				},
			},
			PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
				StartTime:      &metav1.Time{Time: clock.Now().Add(startShift)},
				CompletionTime: &metav1.Time{Time: clock.Now().Add(endShift)},
			},
		},
	}
}

func TestDescribe(t *testing.T) {
	t1 := time.Date(1999, time.February, 3, 4, 5, 6, 7, time.UTC)
	cw := clockwork.NewFakeClockAt(t1)
	ns := "ns"
	running := tektonv1.PipelineRunReasonRunning.String()
	type args struct {
		currentNamespace string
		repoName         string
		opts             *describeOpts
		pruns            []*tektonv1.PipelineRun
		events           []*corev1.Event
		eventListErr     error
	}
	tests := []struct {
		name          string
		args          args
		wantErr       bool
		wantErrOutput string
		skipGolden    bool
	}{
		{
			name: "one live run",
			args: args{
				repoName:         "test-run",
				currentNamespace: ns,
				opts:             &describeOpts{},
				pruns: []*tektonv1.PipelineRun{
					tektontest.MakePRCompletion(cw, "running", ns, running, map[string]string{
						keys.Branch: "tartanpion",
					}, map[string]string{
						keys.Repository: "test-run",
					}, 30),
				},
			},
			wantErr: false,
		},
		{
			name: "two pipelineruns",
			args: args{
				repoName:         "test-run",
				currentNamespace: ns,
				opts:             &describeOpts{},
				pruns: []*tektonv1.PipelineRun{
					tektontest.MakePRCompletion(cw, "running", ns, running, map[string]string{
						keys.Branch:    "tartanpion",
						keys.EventType: "papayolo",
					}, map[string]string{
						keys.Repository: "test-run",
					}, 30),
					makePR(cw, "pipelinerun1", ns, "Success", corev1.ConditionTrue,
						map[string]string{
							keys.SHA:       "SHA",
							keys.ShaURL:    "https://anurl.com/commit/SHA",
							keys.ShaTitle:  "A title",
							keys.Branch:    "TargetBranch",
							keys.EventType: "propseryouplaboun",
						}, -16*time.Minute, -15*time.Minute),
				},
			},
			wantErr: false,
		},
		{
			name: "collect failures",
			args: args{
				repoName:         "test-run",
				currentNamespace: "namespace",
				opts: &describeOpts{
					PacCliOpts: cli.PacCliOpts{
						Namespace: "optnamespace",
					},
				},
				pruns: []*tektonv1.PipelineRun{
					makePR(cw, "pipelinerun1", "optnamespace", "Failed", corev1.ConditionFalse,
						map[string]string{
							keys.SHA:      "SHA",
							keys.ShaURL:   "https://anurl.com/commit/SHA",
							keys.ShaTitle: "A title",
							keys.Branch:   "TargetBranch",
						}, -16*time.Minute, -15*time.Minute),
				},
			},
			wantErr: false,
		},
		{
			name: "target a pipelinerun",
			args: args{
				repoName:         "test-run",
				currentNamespace: ns,
				opts:             &describeOpts{TargetPipelineRun: "running2"},
				pruns: []*tektonv1.PipelineRun{
					tektontest.MakePRCompletion(cw, "running", ns, running, map[string]string{
						keys.Branch: "tartanpion",
					}, map[string]string{
						keys.Repository: "test-run",
					}, 30),
					tektontest.MakePRCompletion(cw, "running2", ns, running, map[string]string{
						keys.Branch: "vavaroom",
					}, map[string]string{
						keys.Repository: "test-run",
					}, 30),
				},
			},
			wantErr: false,
		},
		{
			name: "multiple live runs",
			args: args{
				repoName:         "test-run",
				currentNamespace: ns,
				opts:             &describeOpts{},
				pruns: []*tektonv1.PipelineRun{
					tektontest.MakePRCompletion(cw, "running", ns, running, map[string]string{
						keys.Branch: "tartanpion",
					}, map[string]string{
						keys.Repository: "test-run",
					}, 30),
					tektontest.MakePRCompletion(cw, "running2", ns, running, map[string]string{
						keys.Branch: "vavaroom",
					}, map[string]string{
						keys.Repository: "test-run",
					}, 30),
				},
			},
			wantErr: false,
		},
		{
			name: "use real time",
			args: args{
				repoName:         "test-run",
				currentNamespace: "namespace",
				opts: &describeOpts{
					PacCliOpts: cli.PacCliOpts{
						Namespace:   "optnamespace",
						UseRealTime: true,
					},
				},
				pruns: []*tektonv1.PipelineRun{
					makePR(cw, "pipelinerun1", "optnamespace", "Success", corev1.ConditionTrue,
						map[string]string{
							keys.SHA:      "SHA",
							keys.ShaURL:   "https://anurl.com/commit/SHA",
							keys.ShaTitle: "A title",
							keys.Branch:   "TargetBranch",
						}, -16*time.Minute, -15*time.Minute),
				},
			},
			wantErr: false,
		},
		{
			name: "one pipelinerun and optnamespace",
			args: args{
				repoName:         "test-run",
				currentNamespace: "namespace",
				opts: &describeOpts{
					PacCliOpts: cli.PacCliOpts{
						Namespace: "optnamespace",
					},
				},
				pruns: []*tektonv1.PipelineRun{
					makePR(cw, "pipelinerun1", "optnamespace", "Success", corev1.ConditionTrue,
						map[string]string{
							keys.SHA:      "SHA",
							keys.ShaURL:   "https://anurl.com/commit/SHA",
							keys.ShaTitle: "A title",
							keys.Branch:   "TargetBranch",
						}, -16*time.Minute, -15*time.Minute),
				},
			},
			wantErr: false,
		},
		{
			name: "repository events",
			args: args{
				repoName:         "test-run",
				currentNamespace: "namespace",
				opts: &describeOpts{
					PacCliOpts: cli.PacCliOpts{
						Namespace: "namespace",
					},
					ShowEvents: true,
				},
				events: []*corev1.Event{
					{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
							Namespace:         "namespace",
							Name:              "test-run-abcd",
						},
						Message: "Eeny, meeny, miny, moe, Catch a tiger by the toe.",
						Reason:  "ItchyBack",
						Type:    corev1.EventTypeNormal,
						InvolvedObject: corev1.ObjectReference{
							Name: "test-run", Kind: "Repository", Namespace: "namespace",
						},
					},
				},
				pruns: []*tektonv1.PipelineRun{
					makePR(cw, "pipelinerun1", "namespace", "Success", corev1.ConditionTrue,
						map[string]string{
							keys.SHA:      "SHA",
							keys.ShaURL:   "https://anurl.com/commit/SHA",
							keys.ShaTitle: "A title",
							keys.Branch:   "TargetBranch",
						}, -16*time.Minute, -15*time.Minute),
				},
			},
			wantErr: false,
		},
		{
			name: "repository multiple events",
			args: args{
				repoName:         "test-run",
				currentNamespace: "namespace",
				opts: &describeOpts{
					PacCliOpts: cli.PacCliOpts{
						Namespace: "namespace",
					},
					ShowEvents: true,
				},
				events: []*corev1.Event{
					{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
							Namespace:         "namespace",
							Name:              "test-run-a",
						},
						Message: "Eeny, meeny, miny, moe, Catch a tiger by the toe.",
						Reason:  "ItchyBack",
						Type:    corev1.EventTypeNormal,
						InvolvedObject: corev1.ObjectReference{
							Name: "test-run", Kind: "Repository", Namespace: "namespace",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: metav1.Time{Time: cw.Now().Add(-10 * time.Minute)},
							Namespace:         "namespace",
							Name:              "test-run-b",
						},
						Message: "Eeny, meeny, miny, moe, Catch a tiger by the toe.",
						Reason:  "ItchyBack",
						Type:    corev1.EventTypeNormal,
						InvolvedObject: corev1.ObjectReference{
							Name: "test-run", Kind: "Repository", Namespace: "namespace",
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: metav1.Time{Time: cw.Now().Add(-20 * time.Minute)},
							Namespace:         "namespace",
							Name:              "test-run-c",
						},
						Message: "Eeny, meeny, miny, moe, Catch a tiger by the toe.",
						Reason:  "ItchyBack",
						Type:    corev1.EventTypeNormal,
						InvolvedObject: corev1.ObjectReference{
							Name: "test-run", Kind: "Repository", Namespace: "namespace",
						},
					},
				},
				pruns: []*tektonv1.PipelineRun{
					makePR(cw, "pipelinerun1", "namespace", "Success", corev1.ConditionTrue,
						map[string]string{
							keys.SHA:      "SHA",
							keys.ShaURL:   "https://anurl.com/commit/SHA",
							keys.ShaTitle: "A title",
							keys.Branch:   "TargetBranch",
						}, -16*time.Minute, -15*time.Minute),
				},
			},
			wantErr: false,
		},
		{
			name: "repository event list failure is non-blocking",
			args: args{
				repoName:         "test-run",
				currentNamespace: "namespace",
				opts: &describeOpts{
					PacCliOpts: cli.PacCliOpts{
						Namespace: "namespace",
					},
					ShowEvents: true,
				},
				eventListErr: fmt.Errorf("events is forbidden"),
			},
			wantErrOutput: "warning: could not fetch repository events: events is forbidden\n",
			skipGolden:    true,
		},
		{
			name: "multiple pipelineruns",
			args: args{
				opts:             &describeOpts{},
				repoName:         "test-run",
				currentNamespace: "namespace",
				pruns: []*tektonv1.PipelineRun{
					makePR(cw, "pipelinerun1", "namespace", "Success", corev1.ConditionTrue,
						map[string]string{
							keys.SHA:       "SHA",
							keys.ShaURL:    "https://anurl.com/commit/SHA",
							keys.ShaTitle:  "A title",
							keys.Branch:    "TargetBranch",
							keys.EventType: "pull_request",
						}, -16*time.Minute, -15*time.Minute),
					makePR(cw, "pipelinerun2", "namespace", "Success", corev1.ConditionTrue,
						map[string]string{
							keys.SHA:       "SHA2",
							keys.ShaURL:    "https://anurl.com/commit/SHA2",
							keys.ShaTitle:  "Another Update",
							keys.Branch:    "TargetBranch",
							keys.EventType: "pull_request",
						}, -18*time.Minute, -17*time.Minute),
					makePR(cw, "pipelinerun3", "namespace", "Success", corev1.ConditionTrue,
						map[string]string{
							keys.SHA:       "SHA",
							keys.ShaURL:    "https://anurl.com/commit/SHA",
							keys.ShaTitle:  "Another title",
							keys.Branch:    "refs/heads/PushBranch",
							keys.EventType: "push",
						}, -20*time.Minute, -19*time.Minute),
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns := tt.args.currentNamespace
			if tt.args.opts.Namespace != "" {
				ns = tt.args.opts.Namespace
			}
			repositories := []*v1alpha1.Repository{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tt.args.repoName,
						Namespace: ns,
					},
					Spec: v1alpha1.RepositorySpec{
						URL: "https://anurl.com",
					},
				},
			}

			tdata := testclient.Data{
				Events: tt.args.events,
				Namespaces: []*corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: tt.args.currentNamespace,
						},
					},
				},
				PipelineRuns: tt.args.pruns,
				Repositories: repositories,
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			if tt.args.eventListErr != nil {
				stdata.Kube.PrependReactor("list", "events", func(_ k8stesting.Action) (bool, k8sruntime.Object, error) {
					return true, nil, tt.args.eventListErr
				})
			}
			cs := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
					Tekton:         stdata.Pipeline,
					Kube:           stdata.Kube,
				},
				Info: info.Info{Kube: &info.KubeOpts{Namespace: tt.args.currentNamespace}},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			io, out := tcli.NewIOStream()
			errOut := &bytes.Buffer{}
			io.ErrOut = errOut
			err := describe(
				ctx, cs, cw, tt.args.opts, io, tt.args.repoName,
			)
			if (err != nil) != tt.wantErr {
				t.Errorf("describe() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			assert.Equal(t, tt.wantErrOutput, errOut.String())
			if !tt.skipGolden {
				golden.Assert(t, out.String(), strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
			}
		})
	}
}
