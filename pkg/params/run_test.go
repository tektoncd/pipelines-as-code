package params

import (
	"context"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/consoleui"
	"github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
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

func TestUpdatePacConfigBranches(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T) (context.Context, *Run)
		wantErr  string
		wantName string
		wantURL  string
	}{
		{
			name: "errors without namespace",
			setup: func(t *testing.T) (context.Context, *Run) {
				t.Helper()
				ctx, _ := rtesting.SetupFakeContext(t)
				return ctx, paramsRunForConfig(t, nil)
			},
			wantErr: "failed to find namespace",
		},
		{
			name: "returns configmap get error",
			setup: func(t *testing.T) (context.Context, *Run) {
				t.Helper()
				ctx, _ := rtesting.SetupFakeContext(t)
				ctx = info.StoreNS(ctx, "pac")
				return ctx, paramsRunForConfig(t, nil)
			},
			wantErr: "not found",
		},
		{
			name: "returns invalid config error",
			setup: func(t *testing.T) (context.Context, *Run) {
				t.Helper()
				ctx, _ := rtesting.SetupFakeContext(t)
				ctx = info.StoreNS(ctx, "pac")
				return ctx, paramsRunForConfig(t, map[string]string{
					"custom-console-url-pr-tasklog": "invalid-url",
				})
			},
			wantErr: "failed to validate and assign values",
		},
		{
			name: "updates tekton dashboard from config",
			setup: func(t *testing.T) (context.Context, *Run) {
				t.Helper()
				t.Setenv("PAC_TEKTON_DASHBOARD_URL", "")
				ctx, _ := rtesting.SetupFakeContext(t)
				ctx = info.StoreNS(ctx, "pac")
				return ctx, paramsRunForConfig(t, map[string]string{
					"tekton-dashboard-url": "https://dashboard.example.test",
				})
			},
			wantName: "Tekton Dashboard",
			wantURL:  "https://dashboard.example.test",
		},
		{
			name: "environment tekton dashboard overrides config",
			setup: func(t *testing.T) (context.Context, *Run) {
				t.Helper()
				t.Setenv("PAC_TEKTON_DASHBOARD_URL", "https://env-dashboard.example.test")
				ctx, _ := rtesting.SetupFakeContext(t)
				ctx = info.StoreNS(ctx, "pac")
				return ctx, paramsRunForConfig(t, map[string]string{
					"tekton-dashboard-url": "https://dashboard.example.test",
				})
			},
			wantName: "Tekton Dashboard",
			wantURL:  "https://env-dashboard.example.test",
		},
		{
			name: "updates custom console from config",
			setup: func(t *testing.T) (context.Context, *Run) {
				t.Helper()
				t.Setenv("PAC_TEKTON_DASHBOARD_URL", "")
				ctx, _ := rtesting.SetupFakeContext(t)
				ctx = info.StoreNS(ctx, "pac")
				return ctx, paramsRunForConfig(t, map[string]string{
					"custom-console-name": "Custom Console",
					"custom-console-url":  "https://custom-console.example.test",
				})
			},
			wantName: "Custom Console",
			wantURL:  "https://custom-console.example.test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, run := tt.setup(t)

			err := run.UpdatePacConfig(ctx)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantName, run.Clients.ConsoleUI().GetName())
			assert.Equal(t, tt.wantURL, run.Clients.ConsoleUI().URL())
		})
	}
}

func paramsRunForConfig(t *testing.T, configData map[string]string) *Run {
	t.Helper()

	var objects []runtime.Object
	if configData != nil {
		objects = append(objects, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pac-config",
				Namespace: "pac",
			},
			Data: configData,
		})
	}

	run := &Run{
		Clients: clients.Clients{
			Kube:    kubefake.NewSimpleClientset(objects...),
			Dynamic: dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
			Log:     zap.NewNop().Sugar(),
		},
		Info: info.Info{
			Pac: info.NewPacOpts(),
			Controller: &info.ControllerInfo{
				Configmap: "pac-config",
			},
		},
	}
	run.Clients.SetConsoleUI(&consoleui.TektonDashboard{BaseURL: "https://old-dashboard.example.test"})

	return run
}

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
