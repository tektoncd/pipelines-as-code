package consoleui

import (
	"testing"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestFallbackConsole(t *testing.T) {
	fbc := &FallBackConsole{}
	ctx, _ := rtesting.SetupFakeContext(t)
	unsf := &unstructured.Unstructured{}
	unsf.SetUnstructuredContent(map[string]any{
		"apiVersion": "foo.io/v1",
		"kind":       "Random",
	})
	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), unsf)

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "pr",
		},
	}
	trStatus := &tektonv1.PipelineRunTaskRunStatus{
		PipelineTaskName: "task",
	}
	assert.NilError(t, fbc.UI(ctx, dynClient))
	assert.Assert(t, fbc.URL() != "")
	assert.Assert(t, fbc.DetailURL(pr) != "")
	assert.Assert(t, fbc.TaskLogURL(pr, trStatus) != "")
	assert.Assert(t, fbc.StepLogURL(pr, trStatus, "step") != "")
}
