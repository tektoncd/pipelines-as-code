package logs

import (
	"testing"

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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestLogs(t *testing.T) {
	cw := clockwork.NewFakeClock()
	ns := "ns"

	completed := tektonv1.PipelineRunReasonCompleted.String()

	tests := []struct {
		name             string
		wantErr          bool
		repoName         string
		currentNamespace string
		pruns            []*tektonv1.PipelineRun
		useLastPR        bool
	}{
		{
			name:             "good/show logs",
			wantErr:          false,
			repoName:         "test",
			currentNamespace: ns,
			pruns: []*tektonv1.PipelineRun{
				tektontest.MakePRCompletion(cw, "test-pipeline", ns, completed, nil, map[string]string{
					keys.Repository: "test",
				}, 30),
			},
		},
		{
			name:             "good/show logs with useLastPR",
			wantErr:          false,
			repoName:         "test",
			currentNamespace: ns,
			useLastPR:        true,
			pruns: []*tektonv1.PipelineRun{
				tektontest.MakePRCompletion(cw, "test-pipeline", ns, completed, nil, map[string]string{
					keys.Repository: "test",
				}, 30),
				tektontest.MakePRCompletion(cw, "test-pipeline2", ns, completed, nil, map[string]string{
					keys.Repository: "test",
				}, 30),
			},
		},
		{
			name:             "bad/no prs",
			wantErr:          true,
			repoName:         "test",
			currentNamespace: ns,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repositories := []*v1alpha1.Repository{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      tt.repoName,
						Namespace: tt.currentNamespace,
					},
					Spec: v1alpha1.RepositorySpec{
						URL: "https://anurl.com",
					},
				},
			}
			tdata := testclient.Data{
				Namespaces: []*corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: tt.currentNamespace,
						},
					},
				},
				PipelineRuns: tt.pruns,
				Repositories: repositories,
			}

			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
					Tekton:         stdata.Pipeline,
				},
				Info: info.Info{Kube: &info.KubeOpts{Namespace: tt.currentNamespace}},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			io, _ := tcli.NewIOStream()
			lopts := &logOption{
				cs: cs,
				cw: clockwork.NewFakeClock(),
				opts: &cli.PacCliOpts{
					Namespace: tt.currentNamespace,
				},
				repoName:  tt.repoName,
				limit:     1,
				tknPath:   "/fake/tkn",
				ioStreams: io,
				useLastPR: tt.useLastPR,
				execFunc: func(_ string, _, _ []string) error {
					return nil
				},
			}

			err := log(ctx, lopts)
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected an error but got none")
			} else {
				assert.NilError(t, err)
			}
		})
	}
}
