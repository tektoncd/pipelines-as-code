package reconciler

import (
	"context"
	"path"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/controller"
	rtesting "knative.dev/pkg/reconciler/testing"
)

type fakeReconciler struct{}

func (r *fakeReconciler) Reconcile(_ context.Context, _ string) error {
	return nil
}

func TestCheckStateAndEnqueue(t *testing.T) {
	observer, catcher := zapobserver.New(zap.DebugLevel)
	logger := zap.New(observer).Sugar()
	// set debug level
	wh := &fakeReconciler{}
	// Create a new controller implementation.
	impl := controller.NewContext(context.TODO(), wh, controller.ControllerOptions{
		WorkQueueName: "ValidationWebhook",
		Logger:        logger.Named("ValidationWebhook"),
	})

	// Create a new PipelineRun object with the "started" state label.
	testPR := tektontest.MakePRStatus("namespace", "force-me", []pipelinev1.ChildStatusReference{
		tektontest.MakeChildStatusReference("first"),
		tektontest.MakeChildStatusReference("last"),
		tektontest.MakeChildStatusReference("middle"),
	}, nil)
	testPR.SetAnnotations(map[string]string{
		keys.State: "started",
	})

	// Call the checkStateAndEnqueue function with the PipelineRun object.
	checkStateAndEnqueue(impl)(testPR)
	assert.Equal(t, impl.Name, "ValidationWebhook")
	assert.Equal(t, impl.Concurrency, 2)
	assert.Equal(t, catcher.FilterMessageSnippet("Adding to queue namespace/force-me").Len(), 1)
}

func TestCtrlOpts(t *testing.T) {
	observer, _ := zapobserver.New(zap.DebugLevel)
	logger := zap.New(observer).Sugar()
	// Create a new controller implementation.
	wh := &fakeReconciler{}
	// Create a new controller implementation.
	impl := controller.NewContext(context.TODO(), wh, controller.ControllerOptions{
		WorkQueueName: "ValidationWebhook",
		Logger:        logger.Named("ValidationWebhook"),
	})
	// Call the ctrlOpts function to get the controller options.
	opts := ctrlOpts()(impl)

	// Assert that the finalizer name is set correctly.
	assert.Equal(t, path.Join(pipelinesascode.GroupName, pipelinesascode.FinalizerName), opts.FinalizerName)
	assert.Assert(t, opts.SkipStatusUpdates)

	// Create a new PipelineRun object with the "started" state label.
	pr := &pipelinev1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-pipeline-run",
			Namespace:   "test-namespace",
			Annotations: map[string]string{keys.State: "started"},
		},
	}

	// Call the promote filter function with the PipelineRun object.
	promote := opts.PromoteFilterFunc(pr)

	// Assert that the promote filter function returns true.
	assert.Assert(t, promote)
}

func TestEnqueueQueuedPipelineRuns(t *testing.T) {
	const ns = "test-ns"

	queuedPR := func(name, repoName string) *pipelinev1.PipelineRun {
		return &pipelinev1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Labels: map[string]string{
					keys.Repository: repoName,
					keys.State:      kubeinteraction.StateQueued,
				},
			},
		}
	}
	startedPR := &pipelinev1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "already-running",
			Namespace: ns,
			Labels: map[string]string{
				keys.Repository: "myrepo",
				keys.State:      kubeinteraction.StateStarted,
			},
		},
	}

	tests := []struct {
		name    string
		repo    *pacv1alpha1.Repository
		wantLog []string
		notLog  []string
	}{
		{
			name: "enqueues only the queued runs of that repository",
			repo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "myrepo", Namespace: ns},
			},
			wantLog: []string{"Adding to queue " + ns + "/queued-one", "Adding to queue " + ns + "/queued-two"},
			notLog:  []string{"Adding to queue " + ns + "/already-running", "Adding to queue " + ns + "/other-repo-run"},
		},
		{
			name: "repository with no queued runs enqueues nothing",
			repo: &pacv1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "emptyrepo", Namespace: ns},
			},
			notLog: []string{"Adding to queue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, catcher := zapobserver.New(zap.DebugLevel)
			logger := zap.New(observer).Sugar()

			_, informers := testclient.SeedTestData(t, ctx, testclient.Data{
				PipelineRuns: []*pipelinev1.PipelineRun{
					queuedPR("queued-one", "myrepo"),
					queuedPR("queued-two", "myrepo"),
					queuedPR("other-repo-run", "otherrepo"),
					startedPR,
				},
			})

			impl := controller.NewContext(ctx, &fakeReconciler{}, controller.ControllerOptions{
				WorkQueueName: "Test",
				Logger:        logger.Named("Test"),
			})

			enqueueQueuedPipelineRuns(impl, informers.PipelineRun.Lister(), logger)(tt.repo)

			for _, want := range tt.wantLog {
				assert.Equal(t, catcher.FilterMessageSnippet(want).Len(), 1, "expected %q to be enqueued", want)
			}
			for _, notWant := range tt.notLog {
				assert.Equal(t, catcher.FilterMessageSnippet(notWant).Len(), 0, "did not expect %q to be enqueued", notWant)
			}
		})
	}
}
