package info

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

const (
	controllerURL = "https://controller.url"
	provider      = "gitprovider"
)

func TestIsGithubAppInstalled(t *testing.T) {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace1",
		},
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      info.DefaultPipelinesAscodeSecretName,
			Namespace: namespace.GetName(),
		},
		Data: map[string][]byte{
			"hub.token":      []byte(`1234==`),
			"webhook.secret": []byte(`webhooksecret`),
		},
	}
	tests := []struct {
		name       string
		namespaces []*corev1.Namespace
		secrets    []*corev1.Secret
		want       bool
	}{{
		name:       "GithubApp is installed",
		namespaces: []*corev1.Namespace{namespace},
		secrets:    []*corev1.Secret{secret},
		want:       true,
	}, {
		name: "GithubApp is not installed",
		want: false,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			tdata := testclient.Data{
				Secret:     tt.secrets,
				Namespaces: tt.namespaces,
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					Kube: stdata.Kube,
				},
			}
			out := IsGithubAppInstalled(ctx, cs, namespace.GetName())
			if res := cmp.Diff(out, tt.want); res != "" {
				t.Errorf("Diff %s:", res)
			}
		})
	}
}

func TestGetPACInfo(t *testing.T) {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace1",
		},
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      infoConfigMap,
			Namespace: namespace.GetName(),
			Labels: map[string]string{
				"app.kubernetes.io/part-of": "pipelines-as-code",
			},
		},
		Data: map[string]string{
			"provider":       provider,
			"controller-url": controllerURL,
		},
	}

	tests := []struct {
		name        string
		namespaces  []*corev1.Namespace
		configmaps  []*corev1.ConfigMap
		wantError   string
		wantOptions *Options
	}{{
		name:      "Configmap pipelines-as-code-info does not exist",
		wantError: `configmaps "pipelines-as-code-info" not found`,
	}, {
		name:       "Configmap pipelines-as-code-info exist",
		namespaces: []*corev1.Namespace{namespace},
		configmaps: []*corev1.ConfigMap{configMap},
		wantError:  "",
		wantOptions: &Options{
			ControllerURL: controllerURL,
			Provider:      provider,
		},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			tdata := testclient.Data{
				ConfigMap:  tt.configmaps,
				Namespaces: tt.namespaces,
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					Kube: stdata.Kube,
				},
			}
			gotOptions, gotError := GetPACInfo(ctx, cs, namespace.GetName())
			if gotError != nil {
				if res := cmp.Diff(gotError.Error(), tt.wantError); res != "" {
					t.Errorf("Diff %s:", res)
				}
			}
			if res := cmp.Diff(gotOptions, tt.wantOptions); res != "" {
				t.Errorf("Diff %s:", res)
			}
		})
	}
}

func TestUpdateInfoConfigMap(t *testing.T) {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace1",
		},
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      infoConfigMap,
			Namespace: namespace.GetName(),
			Labels: map[string]string{
				"app.kubernetes.io/part-of": "pipelines-as-code",
			},
		},
		Data: map[string]string{},
	}

	tests := []struct {
		name       string
		namespaces []*corev1.Namespace
		configmaps []*corev1.ConfigMap
		option     *Options
		wantError  string
	}{{
		name:      "Configmap pipelines-as-code-info does not exist",
		wantError: `configmaps "pipelines-as-code-info" not found`,
		option: &Options{
			TargetNamespace: namespace.GetName(),
		},
	}, {
		name:       "Update Configmap pipelines-as-code-info with provided options",
		namespaces: []*corev1.Namespace{namespace},
		configmaps: []*corev1.ConfigMap{configMap},
		option: &Options{
			TargetNamespace: namespace.GetName(),
			ControllerURL:   controllerURL,
			Provider:        provider,
		},
		wantError: "",
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			tdata := testclient.Data{
				ConfigMap:  tt.configmaps,
				Namespaces: tt.namespaces,
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					Kube: stdata.Kube,
				},
			}
			if gotError := UpdateInfoConfigMap(ctx, cs, tt.option); gotError != nil {
				if res := cmp.Diff(gotError.Error(), tt.wantError); res != "" {
					t.Errorf("Diff %s:", res)
				}
			}
		})
	}
}
