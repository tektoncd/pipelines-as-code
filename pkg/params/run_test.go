package params

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	rtesting "knative.dev/pkg/reconciler/testing"

	"go.uber.org/zap"
	"gotest.tools/v3/assert"
)

func TestUpdatePacConfigResetConsoleUI(t *testing.T) {
	tests := []struct {
		name        string
		dynamicObjs []runtime.Object
		wantName    string
		wantURL     string
	}{
		{
			name: "reset to openshift console when route is available",
			dynamicObjs: []runtime.Object{
				func() *unstructured.Unstructured {
					route := &unstructured.Unstructured{}
					route.SetUnstructuredContent(map[string]any{
						"apiVersion": "route.openshift.io/v1",
						"kind":       "Route",
						"metadata": map[string]any{
							"name":      "console",
							"namespace": "openshift-console",
						},
						"spec": map[string]any{
							"host": "console.example.test",
						},
					})
					return route
				}(),
			},
			wantName: "OpenShift Console",
			wantURL:  "https://console.example.test",
		},
		{
			name:     "reset to fallback console when route lookup fails",
			wantName: "Not configured",
			wantURL:  consoleui.FallBackConsole{}.URL(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, "pac")

			kubeClient := kubefake.NewSimpleClientset(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pac-config",
					Namespace: "pac",
				},
				Data: map[string]string{},
			})

			run := &Run{
				Clients: clients.Clients{
					Kube:    kubeClient,
					Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), tt.dynamicObjs...),
					Log:     zap.NewNop().Sugar(),
				},
				Info: info.Info{
					Pac: info.NewPacOpts(),
					Controller: &info.ControllerInfo{
						Configmap: "pac-config",
					},
				},
			}
			run.Clients.SetConsoleUI(&consoleui.TektonDashboard{BaseURL: "https://old.example.test"})

			err := run.UpdatePacConfig(ctx)
			assert.NilError(t, err)
			assert.Equal(t, run.Clients.ConsoleUI().GetName(), tt.wantName)
			assert.Equal(t, run.Clients.ConsoleUI().URL(), tt.wantURL)
		})
	}
}
