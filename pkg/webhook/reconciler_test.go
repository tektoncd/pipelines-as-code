package webhook

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	pkgreconciler "knative.dev/pkg/reconciler"
	certresources "knative.dev/pkg/webhook/certificates/resources"
)

// Test_Reconcile tests the reconcile function
// TODO: make it a more complete test.
func TestReconcile(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "run reconcile",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := &reconciler{}
			if err := ac.Reconcile(context.Background(), ""); (err != nil) != tt.wantErr {
				t.Errorf("reconciler.Reconcile() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPath(t *testing.T) {
	ac := &reconciler{path: "/some/path"}
	assert.Equal(t, ac.Path(), "/some/path")
}

func TestReconcileLeaderSecretAndWebhook(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "test-ns")
	key := types.NamespacedName{Name: "test-webhook"}

	kubeClient := fake.NewSimpleClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "webhook-certs", Namespace: "test-ns"},
			Data: map[string][]byte{
				certresources.CACert: []byte("fake-ca-cert"),
			},
		},
		&admissionregistrationv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: "test-webhook"},
			Webhooks: []admissionregistrationv1.ValidatingWebhook{
				{Name: "queued-token.pipelinesascode.tekton.dev", ClientConfig: admissionregistrationv1.WebhookClientConfig{Service: &admissionregistrationv1.ServiceReference{Name: "svc", Namespace: "test-ns"}}},
				{
					Name: "test-webhook",
					ClientConfig: admissionregistrationv1.WebhookClientConfig{
						Service: &admissionregistrationv1.ServiceReference{
							Name:      "svc",
							Namespace: "test-ns",
						},
					},
				},
			},
		},
	)

	factory := informers.NewSharedInformerFactory(kubeClient, 0)
	secretInformer := factory.Core().V1().Secrets()
	vwhInformer := factory.Admissionregistration().V1().ValidatingWebhookConfigurations()
	secretLister := secretInformer.Lister()
	vwhLister := vwhInformer.Lister()
	stop := make(chan struct{})
	defer close(stop)
	factory.Start(stop)
	factory.WaitForCacheSync(stop)

	ac := &reconciler{
		key:          key,
		path:         "/validate",
		client:       kubeClient,
		secretlister: secretLister,
		vwhlister:    vwhLister,
		secretName:   "webhook-certs",
	}
	assert.NilError(t, ac.Promote(pkgreconciler.UniversalBucket(), nil))

	err := ac.Reconcile(context.Background(), "")
	assert.NilError(t, err)

	updated, err := kubeClient.AdmissionregistrationV1().ValidatingWebhookConfigurations().Get(context.Background(), "test-webhook", metav1.GetOptions{})
	assert.NilError(t, err)
	assert.DeepEqual(t, updated.Webhooks[0].ClientConfig.CABundle, []byte("fake-ca-cert"))
	assert.Equal(t, *updated.Webhooks[0].ClientConfig.Service.Path, "/validate")
	assert.Equal(t, len(updated.Webhooks), 2)
	assert.DeepEqual(t, updated.Webhooks[1].ClientConfig.CABundle, []byte("fake-ca-cert"))
	assert.Equal(t, *updated.Webhooks[1].ClientConfig.Service.Path, "/validate")
}

func TestReconcileMissingSecret(t *testing.T) {
	t.Setenv("SYSTEM_NAMESPACE", "test-ns")
	key := types.NamespacedName{Name: "test-webhook"}

	kubeClient := fake.NewSimpleClientset()
	factory := informers.NewSharedInformerFactory(kubeClient, 0)
	secretLister := factory.Core().V1().Secrets().Lister()
	stop := make(chan struct{})
	defer close(stop)
	factory.Start(stop)
	factory.WaitForCacheSync(stop)

	ac := &reconciler{
		key:          key,
		path:         "/validate",
		client:       kubeClient,
		secretlister: secretLister,
		secretName:   "webhook-certs",
	}
	assert.NilError(t, ac.Promote(pkgreconciler.UniversalBucket(), nil))

	err := ac.Reconcile(context.Background(), "")
	assert.Assert(t, err != nil)
}
