package kubestuff

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"gotest.tools/v3/assert"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DenyPipelineRunWrites makes every write to a PipelineRun in one namespace
// fail, and returns a function that lifts the block again.
//
// The webhook points at a service that does not exist and has a Fail policy, so
// the API server rejects the request with a "failed calling webhook" error.
// That is a faithful stand-in for a cluster having a bad minute, and it needs no
// extra workload to be deployed.
//
// Only the PipelineRun resource is matched, not its status subresource, so
// Tekton can still report on the runs that are already going.
func DenyPipelineRunWrites(ctx context.Context, t *testing.T, runcnx *params.Run, ns string) func() {
	t.Helper()
	name := "deny-pipelinerun-writes-" + ns
	fail := admissionv1.Fail
	none := admissionv1.SideEffectClassNone
	scope := admissionv1.NamespacedScope
	timeout := int32(5)
	path := "/deny"

	cfg := &admissionv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Webhooks: []admissionv1.ValidatingWebhook{{
			Name: "deny.pipelinerun.e2e.pipelinesascode.tekton.dev",
			ClientConfig: admissionv1.WebhookClientConfig{
				Service: &admissionv1.ServiceReference{
					Name:      "there-is-no-such-service",
					Namespace: ns,
					Path:      &path,
				},
			},
			Rules: []admissionv1.RuleWithOperations{{
				Operations: []admissionv1.OperationType{admissionv1.Update},
				Rule: admissionv1.Rule{
					APIGroups:   []string{"tekton.dev"},
					APIVersions: []string{"*"},
					Resources:   []string{"pipelineruns"},
					Scope:       &scope,
				},
			}},
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kubernetes.io/metadata.name": ns},
			},
			FailurePolicy:           &fail,
			SideEffects:             &none,
			TimeoutSeconds:          &timeout,
			AdmissionReviewVersions: []string{"v1"},
		}},
	}

	_, err := runcnx.Clients.Kube.AdmissionregistrationV1().
		ValidatingWebhookConfigurations().Create(ctx, cfg, metav1.CreateOptions{})
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("writes to pipelineruns in %s will now fail", ns)

	return func() {
		err := runcnx.Clients.Kube.AdmissionregistrationV1().
			ValidatingWebhookConfigurations().Delete(context.WithoutCancel(ctx), name, metav1.DeleteOptions{})
		if err != nil {
			t.Logf("could not remove the deny webhook %s: %v", name, err)
			return
		}
		runcnx.Clients.Log.Infof("writes to pipelineruns in %s are allowed again", ns)
	}
}
