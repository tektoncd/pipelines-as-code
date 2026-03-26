package listcmd

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	knativeapis "knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func newIOStream() (*cli.IOStreams, *bytes.Buffer) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &cli.IOStreams{
		In:     io.NopCloser(in),
		Out:    out,
		ErrOut: errOut,
	}, out
}

func TestList(t *testing.T) {
	running := tektonv1.PipelineRunReasonRunning.String()
	t1 := time.Date(1999, time.February, 3, 4, 5, 6, 7, time.UTC)
	cw := clockwork.NewFakeClockAt(t1)
	namespace1 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace1",
		},
	}
	namespace2 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace2",
		},
	}
	repoNamespace1SHA := "abcd2"
	repoNamespace1 := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo1",
			Namespace: namespace1.GetName(),
		},
		Spec: pacv1alpha1.RepositorySpec{
			URL: "https://anurl.com/owner/repo",
		},
		Status: []pacv1alpha1.RepositoryRunStatus{
			{
				Status: knativeduckv1.Status{
					Conditions: []knativeapis.Condition{
						{
							Reason: "Success",
						},
					},
				},
				PipelineRunName: "pipelinerun1",
				StartTime:       &metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
				CompletionTime:  &metav1.Time{Time: cw.Now().Add(-15 * time.Minute)},
				SHA:             github.Ptr(repoNamespace1SHA),
				SHAURL:          github.Ptr("https://somewhereandnowhere/1"),
				Title:           github.Ptr("A title"),
				LogURL:          github.Ptr("https://help.me.obiwan.kenobi/1"),
			},
		},
	}
	repoNamespace2 := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo2",
			Namespace: namespace2.GetName(),
		},
		Spec: pacv1alpha1.RepositorySpec{
			URL: "https://anurl.com/owner/repo",
		},
		Status: []pacv1alpha1.RepositoryRunStatus{
			{
				Status: knativeduckv1.Status{
					Conditions: []knativeapis.Condition{
						{
							Reason: "Success",
						},
					},
				},
				PipelineRunName: "pipelinerun2",
				StartTime:       &metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
				CompletionTime:  &metav1.Time{Time: cw.Now().Add(-15 * time.Minute)},
				SHA:             github.Ptr("SHA"),
				SHAURL:          github.Ptr("https://somewhereandnowhere/2"),
				Title:           github.Ptr("A title"),
				LogURL:          github.Ptr("https://help.me.obiwan.kenobi"),
			},
		},
	}

	type args struct {
		namespaces       []*corev1.Namespace
		repositories     []*pacv1alpha1.Repository
		pipelineruns     []*tektonv1.PipelineRun
		currentNamespace string
		opts             *cli.PacCliOpts
		selectors        string
	}
	tests := []struct {
		name    string
		args    args
		wantErr string
	}{
		{
			name: "Test list when there are no repositories in the namespace",
			args: args{
				opts: &cli.PacCliOpts{
					Namespace: "default",
				},
			},
			wantErr: "no repo found",
		},
		{
			name: "Test list repositories only",
			args: args{
				opts:             &cli.PacCliOpts{},
				currentNamespace: namespace1.GetName(),
				namespaces: []*corev1.Namespace{
					namespace1,
				},
				repositories: []*pacv1alpha1.Repository{repoNamespace1},
			},
		},
		{
			name: "Test list repositories only all namespaces",
			args: args{
				opts:             &cli.PacCliOpts{AllNameSpaces: true},
				currentNamespace: "namespace",
				namespaces:       []*corev1.Namespace{namespace1, namespace2},
				repositories:     []*pacv1alpha1.Repository{repoNamespace1, repoNamespace2},
			},
		},
		{
			name: "Test list repositories only specific namespaces",
			args: args{
				opts:             &cli.PacCliOpts{},
				currentNamespace: namespace2.GetName(),
				namespaces:       []*corev1.Namespace{namespace1, namespace2},
				repositories:     []*pacv1alpha1.Repository{repoNamespace1, repoNamespace2},
			},
		},
		{
			name: "Test with real time",
			args: args{
				opts:             &cli.PacCliOpts{UseRealTime: true},
				currentNamespace: namespace2.GetName(),
				namespaces:       []*corev1.Namespace{namespace1, namespace2},
				repositories:     []*pacv1alpha1.Repository{repoNamespace1, repoNamespace2},
			},
		},
		{
			name: "Test list repositories only live PR",
			args: args{
				opts:             &cli.PacCliOpts{},
				currentNamespace: namespace1.GetName(),
				namespaces: []*corev1.Namespace{
					namespace1,
				},
				repositories: []*pacv1alpha1.Repository{repoNamespace1},
				pipelineruns: []*tektonv1.PipelineRun{
					tektontest.MakePRCompletion(cw, "running", namespace1.GetName(), running, nil, map[string]string{
						keys.Repository: repoNamespace1.GetName(),
						keys.SHA:        repoNamespace1SHA,
					}, 30),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tdata := testclient.Data{
				Namespaces:   tt.args.namespaces,
				Repositories: tt.args.repositories,
				PipelineRuns: tt.args.pipelineruns,
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
					Tekton:         stdata.Pipeline,
				},
				Info: info.Info{Kube: &info.KubeOpts{Namespace: tt.args.currentNamespace}},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			io, out := newIOStream()
			if err := list(ctx, cs, tt.args.opts, io,
				cw, tt.args.selectors); err != nil && tt.wantErr != "" {
				assert.Equal(t, err.Error(), tt.wantErr)
			} else {
				golden.Assert(t, out.String(), strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
			}
		})
	}
}
