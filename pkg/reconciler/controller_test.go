package reconciler

import (
	"context"
	"path"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/controller"
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
