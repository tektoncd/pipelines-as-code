package reconciler

import (
	"strconv"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestCleanupPipelineRuns(t *testing.T) {
	ns := "namespace"
	cleanupRepoName := "clean-me-up-before-you-go-go-go-go"
	cleanupPRName := "clean-me-pleaze"
	cleanupLabels := map[string]string{
		keys.OriginalPRName: cleanupPRName,
		keys.Repository:     cleanupRepoName,
		keys.State:          kubeinteraction.StateCompleted,
	}

	cleanupAnnotation := map[string]string{
		keys.OriginalPRName: cleanupPRName,
		keys.Repository:     cleanupRepoName,
	}

	maxRunsAnno := func(run int) map[string]string {
		return map[string]string{
			keys.MaxKeepRuns:    strconv.Itoa(run),
			keys.OriginalPRName: cleanupPRName,
			keys.Repository:     cleanupRepoName,
		}
	}

	clock := clockwork.NewFakeClock()
	tests := []struct {
		name               string
		pruns              []*tektonv1.PipelineRun
		currentpr          *tektonv1.PipelineRun
		maxkeepruns        int
		defaultmaxkeepruns int
		repoNs             string
		repoName           string
		afterCleanup       int
	}{
		{
			name: "using from annotation",
			pruns: []*tektonv1.PipelineRun{
				tektontest.MakePRCompletion(clock, "pipeline-newest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 10),
				tektontest.MakePRCompletion(clock, "pipeline-middest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 20),
				tektontest.MakePRCompletion(clock, "pipeline-oldest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 30),
			},
			repoNs:   ns,
			repoName: cleanupRepoName,
			currentpr: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Labels: cleanupLabels, Annotations: maxRunsAnno(2),
				},
			},
			afterCleanup: 2,
		},
		{
			name: "using from config, as annotation value is more than config",
			pruns: []*tektonv1.PipelineRun{
				tektontest.MakePRCompletion(clock, "pipeline-newest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 10),
				tektontest.MakePRCompletion(clock, "pipeline-middest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 20),
				tektontest.MakePRCompletion(clock, "pipeline-oldest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 30),
			},
			repoNs:   ns,
			repoName: cleanupRepoName,
			currentpr: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Labels: cleanupLabels, Annotations: maxRunsAnno(2),
				},
			},
			afterCleanup: 1,
			maxkeepruns:  1,
		},
		{
			name: "using from annotation, as annotation value is less than config",
			pruns: []*tektonv1.PipelineRun{
				tektontest.MakePRCompletion(clock, "pipeline-newest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 10),
				tektontest.MakePRCompletion(clock, "pipeline-middest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 20),
				tektontest.MakePRCompletion(clock, "pipeline-oldest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 30),
			},
			repoNs:   ns,
			repoName: cleanupRepoName,
			currentpr: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Labels: cleanupLabels, Annotations: maxRunsAnno(2),
				},
			},
			afterCleanup: 2,
			maxkeepruns:  3,
		},
		{
			name: "no max-keep-runs annotation, using default from config",
			pruns: []*tektonv1.PipelineRun{
				tektontest.MakePRCompletion(clock, "pipeline-newest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 10),
				tektontest.MakePRCompletion(clock, "pipeline-middest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 20),
				tektontest.MakePRCompletion(clock, "pipeline-oldest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 30),
			},
			repoNs:             ns,
			repoName:           cleanupRepoName,
			currentpr:          &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Labels: cleanupLabels, Annotations: cleanupAnnotation}},
			afterCleanup:       2,
			defaultmaxkeepruns: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.repoName,
					Namespace: tt.repoNs,
				},
			}

			tdata := testclient.Data{
				PipelineRuns: tt.pruns,
				Namespaces: []*corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: ns,
						},
					},
				},
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			kint := kubeinteraction.Interaction{
				Run: &params.Run{
					Clients: clients.Clients{
						Kube:   stdata.Kube,
						Tekton: stdata.Pipeline,
					},
				},
			}

			r := &Reconciler{
				run:       &params.Run{},
				kinteract: kint,
			}

			pacInfo := &info.PacOpts{
				Settings: settings.Settings{
					MaxKeepRunsUpperLimit: tt.maxkeepruns,
					DefaultMaxKeepRuns:    tt.defaultmaxkeepruns,
				},
			}

			err := r.cleanupPipelineRuns(ctx, fakelogger, pacInfo, repo, tt.currentpr)
			assert.NilError(t, err)

			plist, err := kint.Run.Clients.Tekton.TektonV1().PipelineRuns(tt.repoNs).List(
				ctx, metav1.ListOptions{},
			)
			assert.NilError(t, err)
			assert.Equal(t, tt.afterCleanup, len(plist.Items))
		})
	}
}
