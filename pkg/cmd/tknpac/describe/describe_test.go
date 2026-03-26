package describe

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
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
	"gotest.tools/v3/golden"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	knativeapis "knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestDescribe(t *testing.T) {
	t1 := time.Date(1999, time.February, 3, 4, 5, 6, 7, time.UTC)
	cw := clockwork.NewFakeClockAt(t1)
	ns := "ns"
	running := tektonv1.PipelineRunReasonRunning.String()
	type args struct {
		currentNamespace string
		repoName         string
		statuses         []v1alpha1.RepositoryRunStatus
		opts             *describeOpts
		pruns            []*tektonv1.PipelineRun
		events           []*corev1.Event
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "live run and repository run",
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
				},
				statuses: []v1alpha1.RepositoryRunStatus{
					{
						CollectedTaskInfos: &map[string]v1alpha1.TaskInfos{},
						Status: knativeduckv1.Status{
							Conditions: []knativeapis.Condition{
								{
									Reason: "Success",
								},
							},
						},
						PipelineRunName: "pipelinerun1",
						LogURL:          github.Ptr("https://everywhere.anwywhere"),
						StartTime:       &metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
						CompletionTime:  &metav1.Time{Time: cw.Now().Add(-15 * time.Minute)},
						SHA:             github.Ptr("SHA"),
						SHAURL:          github.Ptr("https://anurl.com/commit/SHA"),
						Title:           github.Ptr("A title"),
						TargetBranch:    github.Ptr("TargetBranch"),
						EventType:       github.Ptr("propseryouplaboun"),
					},
				},
			},
			wantErr: false,
		},
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
				statuses: []v1alpha1.RepositoryRunStatus{},
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
				statuses: []v1alpha1.RepositoryRunStatus{},
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
				statuses: []v1alpha1.RepositoryRunStatus{},
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
				statuses: []v1alpha1.RepositoryRunStatus{
					{
						Status: knativeduckv1.Status{
							Conditions: []knativeapis.Condition{
								{
									Reason: "Success",
								},
							},
						},
						CollectedTaskInfos: &map[string]v1alpha1.TaskInfos{
							"task1": {
								Reason:      tektonv1.PipelineRunReasonFailed.String(),
								DisplayName: "And here's to you, Mrs. Robinson",
								LogSnippet:  "We'd like to help you learn to help yourself",
							},

							"task2": {
								Message: "I was sleeping and I forgot to wake up",
								Reason:  tektonv1.PipelineRunReasonTimedOut.String(),
							},
						},
						PipelineRunName: "pipelinerun1",
						LogURL:          github.Ptr("https://everywhere.anwywhere"),
						StartTime:       &metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
						CompletionTime:  &metav1.Time{Time: cw.Now().Add(-15 * time.Minute)},
						SHA:             github.Ptr("SHA"),
						SHAURL:          github.Ptr("https://anurl.com/commit/SHA"),
						Title:           github.Ptr("A title"),
						TargetBranch:    github.Ptr("TargetBranch"),
					},
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
				statuses: []v1alpha1.RepositoryRunStatus{
					{
						Status: knativeduckv1.Status{
							Conditions: []knativeapis.Condition{
								{
									Reason: "Success",
								},
							},
						},
						CollectedTaskInfos: &map[string]v1alpha1.TaskInfos{},
						PipelineRunName:    "pipelinerun1",
						LogURL:             github.Ptr("https://everywhere.anwywhere"),
						StartTime:          &metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
						CompletionTime:     &metav1.Time{Time: cw.Now().Add(-15 * time.Minute)},
						SHA:                github.Ptr("SHA"),
						SHAURL:             github.Ptr("https://anurl.com/commit/SHA"),
						Title:              github.Ptr("A title"),
						TargetBranch:       github.Ptr("TargetBranch"),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "one repository status and optnamespace",
			args: args{
				repoName:         "test-run",
				currentNamespace: "namespace",
				opts: &describeOpts{
					PacCliOpts: cli.PacCliOpts{
						Namespace: "optnamespace",
					},
				},
				statuses: []v1alpha1.RepositoryRunStatus{
					{
						Status: knativeduckv1.Status{
							Conditions: []knativeapis.Condition{
								{
									Reason: "Success",
								},
							},
						},
						CollectedTaskInfos: &map[string]v1alpha1.TaskInfos{},
						PipelineRunName:    "pipelinerun1",
						LogURL:             github.Ptr("https://everywhere.anwywhere"),
						StartTime:          &metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
						CompletionTime:     &metav1.Time{Time: cw.Now().Add(-15 * time.Minute)},
						SHA:                github.Ptr("SHA"),
						SHAURL:             github.Ptr("https://anurl.com/commit/SHA"),
						Title:              github.Ptr("A title"),
						TargetBranch:       github.Ptr("TargetBranch"),
					},
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
				statuses: []v1alpha1.RepositoryRunStatus{
					{
						Status: knativeduckv1.Status{
							Conditions: []knativeapis.Condition{
								{
									Reason: "Success",
								},
							},
						},
						CollectedTaskInfos: &map[string]v1alpha1.TaskInfos{},
						PipelineRunName:    "pipelinerun1",
						LogURL:             github.Ptr("https://everywhere.anwywhere"),
						StartTime:          &metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
						CompletionTime:     &metav1.Time{Time: cw.Now().Add(-15 * time.Minute)},
						SHA:                github.Ptr("SHA"),
						SHAURL:             github.Ptr("https://anurl.com/commit/SHA"),
						Title:              github.Ptr("A title"),
						TargetBranch:       github.Ptr("TargetBranch"),
					},
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
				statuses: []v1alpha1.RepositoryRunStatus{
					{
						Status: knativeduckv1.Status{
							Conditions: []knativeapis.Condition{
								{
									Reason: "Success",
								},
							},
						},
						CollectedTaskInfos: &map[string]v1alpha1.TaskInfos{},
						PipelineRunName:    "pipelinerun1",
						LogURL:             github.Ptr("https://everywhere.anwywhere"),
						StartTime:          &metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
						CompletionTime:     &metav1.Time{Time: cw.Now().Add(-15 * time.Minute)},
						SHA:                github.Ptr("SHA"),
						SHAURL:             github.Ptr("https://anurl.com/commit/SHA"),
						Title:              github.Ptr("A title"),
						TargetBranch:       github.Ptr("TargetBranch"),
					},
				},
			},
			wantErr: false,
		},
		{
			name: "multiple repo status",
			args: args{
				opts:             &describeOpts{},
				repoName:         "test-run",
				currentNamespace: "namespace",
				statuses: []v1alpha1.RepositoryRunStatus{
					{
						CollectedTaskInfos: &map[string]v1alpha1.TaskInfos{},
						Status: knativeduckv1.Status{
							Conditions: []knativeapis.Condition{
								{
									Reason: "Success",
								},
							},
						},
						PipelineRunName: "pipelinerun1",
						LogURL:          github.Ptr("https://everywhere.anwywhere"),
						StartTime:       &metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
						CompletionTime:  &metav1.Time{Time: cw.Now().Add(-15 * time.Minute)},
						SHA:             github.Ptr("SHA"),
						SHAURL:          github.Ptr("https://anurl.com/commit/SHA"),
						Title:           github.Ptr("A title"),
						TargetBranch:    github.Ptr("TargetBranch"),
						EventType:       github.Ptr("pull_request"),
					},
					{
						Status: knativeduckv1.Status{
							Conditions: []knativeapis.Condition{
								{
									Reason: "Success",
								},
							},
						},
						PipelineRunName: "pipelinerun2",
						LogURL:          github.Ptr("https://everywhere.anwywhere"),
						StartTime:       &metav1.Time{Time: cw.Now().Add(-18 * time.Minute)},
						CompletionTime:  &metav1.Time{Time: cw.Now().Add(-17 * time.Minute)},
						SHA:             github.Ptr("SHA2"),
						SHAURL:          github.Ptr("https://anurl.com/commit/SHA2"),
						Title:           github.Ptr("Another Update"),
						TargetBranch:    github.Ptr("TargetBranch"),
						EventType:       github.Ptr("pull_request"),
					},
					{
						CollectedTaskInfos: &map[string]v1alpha1.TaskInfos{},
						Status: knativeduckv1.Status{
							Conditions: []knativeapis.Condition{
								{
									Reason: "Success",
								},
							},
						},
						PipelineRunName: "pipelinerun3",
						LogURL:          github.Ptr("https://everywhere.anwywhere"),
						StartTime:       &metav1.Time{Time: cw.Now().Add(-20 * time.Minute)},
						CompletionTime:  &metav1.Time{Time: cw.Now().Add(-19 * time.Minute)},
						SHA:             github.Ptr("SHA"),
						SHAURL:          github.Ptr("https://anurl.com/commit/SHA"),
						Title:           github.Ptr("Another title"),
						TargetBranch:    github.Ptr("refs/heads/PushBranch"),
						EventType:       github.Ptr("push"),
					},
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
					Status: tt.args.statuses,
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
			if err := describe(
				ctx, cs, cw, tt.args.opts, io, tt.args.repoName); (err != nil) != tt.wantErr {
				t.Errorf("describe() error = %v, wantErr %v", err, tt.wantErr)
			} else {
				golden.Assert(t, out.String(), strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
			}
		})
	}
}
