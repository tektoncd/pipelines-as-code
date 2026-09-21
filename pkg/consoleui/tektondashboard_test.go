package consoleui

import (
	"strings"
	"testing"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestTektonDashboard(t *testing.T) {
	tr := &TektonDashboard{
		BaseURL: "https://test",
	}
	ctx, _ := rtesting.SetupFakeContext(t)

	unsf := &unstructured.Unstructured{}
	unsf.SetUnstructuredContent(map[string]any{
		"apiVersion": "foo.io/v1",
		"kind":       "Random",
	})

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "pr",
		},
	}
	trStatus := &tektonv1.PipelineRunTaskRunStatus{
		PipelineTaskName: "task",
	}
	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), unsf)
	assert.NilError(t, tr.UI(ctx, dynClient))
	assert.Assert(t, strings.Contains(tr.DetailURL(pr), "namespaces/ns"))
	assert.Assert(t, strings.Contains(tr.TaskLogURL(pr, trStatus), "pipelineTask=task"))
	assert.Assert(t, strings.Contains(tr.URL(), "test"))
	assert.Assert(t, strings.Contains(tr.NamespaceURL(pr), "namespaces/ns"))
	assert.Assert(t, strings.Contains(tr.GetName(), tektonDashboardName))
	assert.Equal(t, tr.WithParams(map[string]string{}), Interface(tr))
}
