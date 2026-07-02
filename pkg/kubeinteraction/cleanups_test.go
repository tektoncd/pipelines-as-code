package kubeinteraction

import (
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"golang.org/x/exp/maps"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestCleanupPipelines(t *testing.T) {
	ns := "namespace"
	cleanupRepoName := "clean-me-up-before-you-go-go-go-go"
	cleanupPRName := "clean-me-pleaze"
	cleanupLabels := map[string]string{
		keys.OriginalPRName: cleanupPRName,
		keys.Repository:     cleanupRepoName,
		keys.State:          StateCompleted,
	}
	// copy of cleanupLabels to be used in annotations
	cleanupAnnotations := maps.Clone(cleanupLabels)

	clock := clockwork.NewFakeClock()

	type args struct {
		logSnippet       string
		namespace        string
		repositoryName   string
		maxKeep          int
		pruns            []*tektonv1.PipelineRun
		prunCurrent      *tektonv1.PipelineRun
		kept             int
		prunLatestInList string
		secrets          []*corev1.Secret
		sList            int
	}

	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "cleanup",
			args: args{
				namespace:      ns,
				repositoryName: cleanupRepoName,
				maxKeep:        1,
				kept:           1,
				prunCurrent:    &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Labels: cleanupLabels, Annotations: cleanupAnnotations}},
				pruns: []*tektonv1.PipelineRun{
					tektontest.MakePRCompletion(clock, "pipeline-newest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 10),
					tektontest.MakePRCompletion(clock, "pipeline-middest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 20),
					tektontest.MakePRCompletion(clock, "pipeline-oldest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 30),
				},
				prunLatestInList: "pipeline-newest",
			},
		},
		{
			name: "cleanup-skip-running",
			args: args{
				namespace:      ns,
				repositoryName: cleanupRepoName,
				maxKeep:        1,
				kept:           1, // see my comment in code why only 1 is kept.
				prunCurrent:    &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Labels: cleanupLabels, Annotations: cleanupAnnotations}},
				pruns: []*tektonv1.PipelineRun{
					tektontest.MakePRCompletion(clock, "pipeline-running", ns, tektonv1.PipelineRunReasonRunning.String(), nil, cleanupLabels, 10),
					tektontest.MakePRCompletion(clock, "pipeline-toclean", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 30),
					tektontest.MakePRCompletion(clock, "pipeline-tokeep", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 20),
				},
				prunLatestInList: "pipeline-running",
			},
		},
		{
			name: "cleanup-skip-pending",
			args: args{
				namespace:      ns,
				repositoryName: cleanupRepoName,
				maxKeep:        1,
				kept:           1, // see my comment in code why only 1 is kept.
				prunCurrent:    &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Labels: cleanupLabels, Annotations: cleanupAnnotations}},
				pruns: []*tektonv1.PipelineRun{
					tektontest.MakePRCompletion(clock, "pipeline-pending", ns, tektonv1.PipelineRunReasonPending.String(), nil, cleanupLabels, 10),
					tektontest.MakePRCompletion(clock, "pipeline-toclean", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 30),
					tektontest.MakePRCompletion(clock, "pipeline-tokeep", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, cleanupLabels, 20),
				},
				prunLatestInList: "pipeline-pending",
			},
		},
		{
			name: "cleanup with secrets",
			args: args{
				namespace:      ns,
				repositoryName: cleanupRepoName,
				logSnippet:     "secret pac-gitauth-secret attached to pipelinerun pipeline-toclean has been deleted",
				prunCurrent: &tektonv1.PipelineRun{
					ObjectMeta: metav1.ObjectMeta{
						Labels: cleanupLabels,
						Annotations: map[string]string{
							keys.OriginalPRName: cleanupPRName,
							keys.Repository:     cleanupRepoName,
							keys.GitAuthSecret:  "pac-gitauth-secret",
						},
					},
				},
				maxKeep: 0,
				kept:    0,
				pruns: []*tektonv1.PipelineRun{
					tektontest.MakePRCompletion(clock, "pipeline-toclean", ns, tektonv1.PipelineRunReasonSuccessful.String(), map[string]string{
						keys.OriginalPRName: cleanupPRName,
						keys.Repository:     cleanupRepoName,
						keys.GitAuthSecret:  "pac-gitauth-secret",
					}, cleanupLabels, 30),
				},
				secrets: []*corev1.Secret{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pac-gitauth-secret",
							Namespace: ns,
						},
						Data: map[string][]byte{
							"username": []byte("test"),
							"password": []byte("test"),
						},
					},
				},
			},
		},
		{
			name: "cleanup the secrets related to pipelinerun but not the other secret",
			args: args{
				namespace:      ns,
				repositoryName: cleanupRepoName,
				logSnippet:     "secret pac-gitauth-secret attached to pipelinerun pipeline-toclean has been deleted",
				prunCurrent: &tektonv1.PipelineRun{
					ObjectMeta: metav1.ObjectMeta{
						Labels: cleanupLabels,
						Annotations: map[string]string{
							keys.OriginalPRName: cleanupPRName,
							keys.Repository:     cleanupRepoName,
							keys.GitAuthSecret:  "pac-gitauth-secret",
						},
					},
				},
				maxKeep: 1,
				kept:    1,
				pruns: []*tektonv1.PipelineRun{
					tektontest.MakePRCompletion(clock, "pipeline-notoclean", ns, tektonv1.PipelineRunReasonSuccessful.String(), map[string]string{
						keys.OriginalPRName: cleanupPRName,
						keys.Repository:     cleanupRepoName,
						keys.GitAuthSecret:  "no-cleanuppac-gitauth-secret",
					}, cleanupLabels, 30),
					tektontest.MakePRCompletion(clock, "pipeline-toclean", ns, tektonv1.PipelineRunReasonSuccessful.String(), map[string]string{
						keys.OriginalPRName: cleanupPRName,
						keys.Repository:     cleanupRepoName,
						keys.GitAuthSecret:  "pac-gitauth-secret",
					}, cleanupLabels, 30),
				},
				secrets: []*corev1.Secret{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pac-gitauth-secret",
							Namespace: ns,
						},
						Data: map[string][]byte{
							"username": []byte("test"),
							"password": []byte("test"),
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "no-cleanuppac-gitauth-secret",
							Namespace: ns,
						},
						Data: map[string][]byte{
							"username": []byte("test"),
							"password": []byte("test"),
						},
					},
				},
				sList: 1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.args.repositoryName,
					Namespace: tt.args.namespace,
				},
			}

			tdata := testclient.Data{
				PipelineRuns: tt.args.pruns,
				Namespaces: []*corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: ns,
						},
					},
				},
				Secret: tt.args.secrets,
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			observer, logCatcher := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()
			kint := Interaction{
				Run: &params.Run{
					Clients: clients.Clients{
						Kube:   stdata.Kube,
						Tekton: stdata.Pipeline,
					},
				},
			}

			err := kint.CleanupPipelines(ctx, fakelogger, repo, tt.args.prunCurrent, tt.args.maxKeep)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			}

			plist, err := kint.Run.Clients.Tekton.TektonV1().PipelineRuns(tt.args.namespace).List(
				ctx, metav1.ListOptions{},
			)
			assert.NilError(t, err)
			assert.Equal(t, tt.args.kept, len(plist.Items), "we have %d pruns kept when we wanted only %d", len(plist.Items), tt.args.kept)
			if tt.args.prunLatestInList != "" {
				assert.Equal(t, tt.args.prunLatestInList, plist.Items[0].Name)
			}
			if tt.args.logSnippet != "" {
				assert.Assert(t, logCatcher.FilterMessageSnippet(tt.args.logSnippet).Len() > 0, logCatcher.All())
			}

			secretList, err := kint.Run.Clients.Kube.CoreV1().Secrets(tt.args.namespace).List(ctx, metav1.ListOptions{})
			assert.NilError(t, err)
			assert.Equal(t, len(secretList.Items), tt.args.sList)
		})
	}
}
