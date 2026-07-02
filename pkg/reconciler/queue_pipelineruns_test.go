package reconciler

import (
	"fmt"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testconcurrency "github.com/openshift-pipelines/pipelines-as-code/pkg/test/concurrency"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestQueuePipelineRun(t *testing.T) {
	tests := []struct {
		name          string
		wantErrString string
		wantLog       string
		pipelineRun   *tektonv1.PipelineRun
		testRepo      *pacv1alpha1.Repository
		globalRepo    *pacv1alpha1.Repository
		runningQueue  []string
	}{
		{
			name: "no existing order annotation",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
		},
		{
			name: "no repo name annotation",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
					},
				},
			},
			wantErrString: fmt.Sprintf("no %s annotation found", keys.Repository),
		},
		{
			name: "empty repo name annotation",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "",
					},
				},
			},
			wantErrString: fmt.Sprintf("annotation %s is empty", keys.Repository),
		},
		{
			name: "no repo found",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "foo",
					},
				},
			},
		},
		{
			name: "merging global repository settings",
			globalRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "global",
					Namespace: "global",
				},
				Spec: pacv1alpha1.RepositorySpec{
					Settings: &pacv1alpha1.Settings{
						PipelineRunProvenance: "somewhere",
					},
				},
			},
			runningQueue: []string{},
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "test",
					},
				},
			},
			testRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: pacv1alpha1.RepositorySpec{
					URL: randomURL,
				},
			},
			wantLog: "Merging global repository settings with local repository settings",
		},
		{
			name:         "no new PR acquired",
			runningQueue: []string{},
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "test",
					},
				},
			},
			testRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: pacv1alpha1.RepositorySpec{
					URL: randomURL,
				},
			},
			wantLog: "no new PipelineRun acquired for repo test",
		},
		{
			name:         "failed to get PR from the Q after many iterations",
			runningQueue: []string{"test/test2"},
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
					Annotations: map[string]string{
						keys.ExecutionOrder: "repo/foo1",
						keys.Repository:     "test",
					},
				},
			},
			testRepo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: pacv1alpha1.RepositorySpec{
					URL: randomURL,
				},
			},
			wantLog:       "failed to get PR",
			wantErrString: "max iterations reached of",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, logcatch := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			repos := []*pacv1alpha1.Repository{}
			if tt.testRepo != nil {
				repos = append(repos, tt.testRepo)
			}
			if tt.globalRepo != nil {
				repos = append(repos, tt.globalRepo)
			}
			testData := testclient.Data{
				Repositories: repos,
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)
			r := &Reconciler{
				qm: testconcurrency.TestQMI{
					RunningQueue: tt.runningQueue,
				},
				repoLister: informers.Repository.Lister(),
				run: &params.Run{
					Info: info.Info{
						Kube: &info.KubeOpts{
							Namespace: "global",
						},
						Controller: &info.ControllerInfo{},
					},
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
						Kube:           stdata.Kube,
						Log:            fakelogger,
					},
				},
			}
			if tt.globalRepo != nil {
				r.run.Info.Controller.GlobalRepository = tt.globalRepo.GetName()
			}
			err := r.queuePipelineRun(ctx, fakelogger, tt.pipelineRun)
			if tt.wantErrString != "" {
				assert.ErrorContains(t, err, tt.wantErrString)
				return
			}
			assert.NilError(t, err)

			if tt.wantLog != "" {
				assert.Assert(t, logcatch.FilterMessage(tt.wantLog).Len() != 0, "We didn't get the expected log message", logcatch.All())
			}
			if tt.globalRepo != nil && tt.testRepo != nil {
				cachedRepo, err := informers.Repository.Lister().Repositories(tt.testRepo.Namespace).Get(tt.testRepo.Name)
				assert.NilError(t, err)
				assert.Assert(t, cachedRepo.Spec.Settings == nil, "global settings should not mutate the cached Repository")
			}
		})
	}
}
