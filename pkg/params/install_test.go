package params

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	rtesting "knative.dev/pkg/reconciler/testing"

	"gotest.tools/v3/assert"
)

func TestGetInstallLocation(t *testing.T) {
	const testNamespace = "pac-namespace"
	const versionLabel = "v1.2.3"
	originalInstallNamespaces := info.InstallNamespaces
	defer func() {
		info.InstallNamespaces = originalInstallNamespaces
	}()

	tests := []struct {
		name              string
		installNamespaces []string
		deployments       []runtime.Object
		expectedNS        string
		expectedVersion   string
		wantErr           bool
	}{
		{
			name:              "deployment with version label",
			installNamespaces: []string{testNamespace},
			deployments: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pipelines-as-code-controller",
						Namespace: testNamespace,
						Labels: map[string]string{
							"app.kubernetes.io/version": versionLabel,
						},
					},
				},
			},
			expectedNS:      testNamespace,
			expectedVersion: versionLabel,
			wantErr:         false,
		},
		{
			name:              "deployment without version label",
			installNamespaces: []string{testNamespace},
			deployments: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pipelines-as-code-controller",
						Namespace: testNamespace,
					},
				},
			},
			expectedNS:      testNamespace,
			expectedVersion: "unknown",
			wantErr:         false,
		},
		{
			name:              "no deployments found",
			installNamespaces: []string{"ns1", "ns2"},
			deployments:       []runtime.Object{},
			expectedNS:        "",
			expectedVersion:   "",
			wantErr:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			info.InstallNamespaces = tt.installNamespaces
			kubeClient := fake.NewClientset(tt.deployments...)

			run := &Run{
				Clients: clients.Clients{
					Kube: kubeClient,
				},
			}

			ns, version, err := GetInstallLocation(ctx, run)
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
			} else {
				assert.NilError(t, err)
				assert.Equal(t, ns, tt.expectedNS)
				assert.Equal(t, version, tt.expectedVersion)
			}
		})
	}
}
