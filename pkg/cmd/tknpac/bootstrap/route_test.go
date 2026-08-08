package bootstrap

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-github/v90/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestIsTLSError(t *testing.T) {
	assert.Equal(t, isTLSError(nil), false)
	assert.Equal(t, isTLSError(context.DeadlineExceeded), false)
}

func TestDetectSelfSignedCertificate(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)

	srv := httptest.NewServer(nil)
	defer srv.Close()
	msg := detectSelfSignedCertificate(ctx, srv.URL)
	assert.Equal(t, msg, "")

	tlsSrv := httptest.NewTLSServer(nil)
	defer tlsSrv.Close()
	msg = detectSelfSignedCertificate(ctx, tlsSrv.URL)
	assert.Assert(t, strings.Contains(msg, "self signed certificate"))

	msg = detectSelfSignedCertificate(ctx, "http://this.host.does.not.exist.invalid")
	assert.Assert(t, msg != "")

	msg = detectSelfSignedCertificate(ctx, "://invalid-url")
	assert.Assert(t, msg != "")
}

func TestDetectOpenShiftRoute(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)

	fakeroute := &unstructured.Unstructured{}
	fakeroute.SetUnstructuredContent(map[string]any{
		"apiVersion": "route.openshift.io/v1",
		"kind":       "Route",
		"metadata": map[string]any{
			"name":      "controller",
			"namespace": "ns",
			"labels": map[string]any{
				"pipelines-as-code/route": "controller",
			},
		},
		"spec": map[string]any{
			"host": "pac.example.com",
		},
	})

	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), fakeroute)
	run := &params.Run{
		Clients: clients.Clients{
			Dynamic: dynClient,
		},
	}
	url, err := DetectOpenShiftRoute(ctx, run, "ns")
	assert.NilError(t, err)
	assert.Equal(t, url, "https://pac.example.com")
}

func TestDeleteSecret(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	log, _ := logger.GetLogger()
	run := &params.Run{
		Clients: clients.Clients{
			Kube: cs.Kube,
			Log:  log,
		},
	}
	opts := &bootstrapOpts{targetNamespace: "ns"}
	// deleting a non-existent secret should return an error (not found)
	err := deleteSecret(ctx, run, opts)
	assert.Assert(t, err != nil)
}

func TestCheckOpenshiftRoute(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	log, _ := logger.GetLogger()
	run := &params.Run{
		Clients: clients.Clients{
			Kube: cs.Kube,
			Log:  log,
		},
		Info: info.Info{},
	}
	found, err := checkOpenshiftRoute(run)
	assert.NilError(t, err)
	assert.Equal(t, found, false)
}

func TestCreatePacSecret(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	log, _ := logger.GetLogger()
	run := &params.Run{
		Clients: clients.Clients{
			Kube: cs.Kube,
			Log:  log,
		},
	}
	io, out := newIOStream()
	opts := &bootstrapOpts{targetNamespace: "ns", ioStreams: io}

	appID := int64(123)
	pem := "fake-pem"
	webhookSecret := "fake-secret"
	manifest := &github.AppConfig{
		ID:            &appID,
		PEM:           &pem,
		WebhookSecret: &webhookSecret,
	}

	err := createPacSecret(ctx, run, opts, manifest)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(out.String(), "Secret pipelines-as-code-secret has been created in the ns namespace"))

	secret, err := run.Clients.Kube.CoreV1().Secrets("ns").Get(ctx, secretName, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, string(secret.Data["github-application-id"]), "123")
	assert.Equal(t, string(secret.Data["github-private-key"]), pem)
	assert.Equal(t, string(secret.Data["webhook.secret"]), webhookSecret)
}
