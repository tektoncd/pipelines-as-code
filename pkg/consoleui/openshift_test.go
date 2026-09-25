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

func TestOpenshiftConsoleUI(t *testing.T) {
	fakeroute := &unstructured.Unstructured{}
	fakeroute.SetUnstructuredContent(map[string]any{
		"apiVersion": "route.openshift.io/v1",
		"kind":       "Route",
		"metadata": map[string]any{
			"name":      "console",
			"namespace": "openshift-console",
		},
		"spec": map[string]any{
			"host": "http://fakeconsole",
		},
	})
	emptys := &unstructured.Unstructured{}
	emptys.SetUnstructuredContent(map[string]any{
		"apiVersion": "route.openshift.io/v1",
		"kind":       "Route",
		"metadata": map[string]any{
			"name":      "not",
			"namespace": "console",
		},
	})
	nospeccontent := &unstructured.Unstructured{}
	nospeccontent.SetUnstructuredContent(map[string]any{
		"apiVersion": "route.openshift.io/v1",
		"kind":       "Route",
		"metadata": map[string]any{
			"name":      "console",
			"namespace": "openshift-console",
		},
		"spec": map[string]any{},
	})

	nohost := &unstructured.Unstructured{}
	nohost.SetUnstructuredContent(map[string]any{
		"apiVersion": "route.openshift.io/v1",
		"kind":       "Route",
		"metadata": map[string]any{
			"name":      "console",
			"namespace": "openshift-console",
		},
	})

	tests := []struct {
		name     string
		unsf     *unstructured.Unstructured
		wantErr  bool
		wantHost string
		wantName string
	}{
		{
			name:     "Get Openshift Console url",
			unsf:     fakeroute,
			wantErr:  false,
			wantHost: "http://fakeconsole",
		},
		{
			name:     "Get Openshift Console name",
			unsf:     fakeroute,
			wantErr:  false,
			wantName: "OpenShift Console",
		},
		{
			name:    "No Openshift Console",
			unsf:    emptys,
			wantErr: true,
		},
		{
			name:    "No spec in route",
			unsf:    nospeccontent,
			wantErr: true,
		},
		{
			name:    "No host in route.spec",
			unsf:    nohost,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			o := &OpenshiftConsole{}
			dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), tt.unsf)
			err := o.UI(ctx, dynClient)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			if tt.wantHost != "" {
				assert.Equal(t, o.host, tt.wantHost)
			}
			if tt.wantName != "" {
				assert.Equal(t, o.GetName(), tt.wantName)
			}
		})
	}
}

func TestOpenshiftConsoleURLs(t *testing.T) {
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "theNS",
			Name:      "pr",
		},
	}
	trStatus := &tektonv1.PipelineRunTaskRunStatus{
		PipelineTaskName: "task",
	}
	o := OpenshiftConsole{host: "fakeconsole"}
	assert.Equal(t, o.URL(), "https://fakeconsole")
	assert.Equal(t, o.DetailURL(pr), "https://fakeconsole/k8s/ns/theNS/tekton.dev~v1~PipelineRun/pr")
	assert.Equal(t, o.TaskLogURL(pr, trStatus), "https://fakeconsole/k8s/ns/theNS/tekton.dev~v1~PipelineRun/pr/logs/task")
	assert.Equal(t, o.StepLogURL(pr, trStatus, "unit-tests"),
		"https://fakeconsole/k8s/ns/theNS/tekton.dev~v1~PipelineRun/pr/logs?taskName=task&step=unit-tests")
	// no step name falls back to the task logs
	assert.Equal(t, o.StepLogURL(pr, trStatus, ""), o.TaskLogURL(pr, trStatus))
	assert.Equal(t, o.NamespaceURL(pr), "https://fakeconsole/pipelines/ns/theNS/pipeline-runs")

	emptyHost := OpenshiftConsole{host: ""}
	assert.Equal(t, emptyHost.URL(), "https://openshift.url.is.not.configured")
	assert.Equal(t, emptyHost.DetailURL(pr), "https://openshift.url.is.not.configured/k8s/ns/theNS/tekton.dev~v1~PipelineRun/pr")
	assert.Equal(t, emptyHost.TaskLogURL(pr, trStatus), "https://openshift.url.is.not.configured/k8s/ns/theNS/tekton.dev~v1~PipelineRun/pr/logs/task")
	assert.Equal(t, emptyHost.NamespaceURL(pr), "https://openshift.url.is.not.configured/pipelines/ns/theNS/pipeline-runs")
}

func TestOpenshiftConsoleStepLogURLEscaping(t *testing.T) {
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Namespace: "theNS", Name: "pr"},
	}
	trStatus := &tektonv1.PipelineRunTaskRunStatus{PipelineTaskName: "my task"}
	o := OpenshiftConsole{host: "fakeconsole"}
	assert.Equal(t, o.StepLogURL(pr, trStatus, "a step"),
		"https://fakeconsole/k8s/ns/theNS/tekton.dev~v1~PipelineRun/pr/logs?taskName=my+task&step=a+step")
}
