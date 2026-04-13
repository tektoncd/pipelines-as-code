package webhook

import (
	"fmt"
	"strings"
	"testing"

	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestWebHookSecret(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	anURL := "https://hello.world/"
	repoName := "test-repo"
	repoNS := "test-ns"
	secretName := "test-secret"
	data := testclient.Data{
		Secret: []*corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: repoNS,
				},
				Data: map[string][]byte{
					secrets.DefaultGitProviderSecretKey: []byte("somethingsomething"),
				},
			},
		},
		Repositories: []*pacv1alpha1.Repository{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      repoName,
					Namespace: repoNS,
				},
				Spec: pacv1alpha1.RepositorySpec{
					URL: anURL,
				},
			},
		},
		Namespaces: []*corev1.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: repoNS,
				},
			},
		},
	}

	cs, _ := testclient.SeedTestData(t, ctx, data)
	logger, _ := logger.GetLogger()
	io, _, out, _ := cli.IOTest()
	w := &Options{
		Run: &params.Run{
			Clients: clients.Clients{
				PipelineAsCode: cs.PipelineAsCode,
				Log:            logger,
				Kube:           cs.Kube,
			},
			Info: info.Info{
				Pac: &info.PacOpts{
					Settings: settings.Settings{
						AutoConfigureNewGitHubRepo: false,
					},
				},
			},
		},
		IOStreams:           io,
		RepositoryName:      repoName,
		RepositoryNamespace: repoNS,
		SecretName:          secretName,
	}
	response := &response{PersonalAccessToken: "pattest", WebhookSecret: "webtest", UserName: "testuser", APIURL: "https://api.github.com/"}
	assert.NilError(t, w.createWebhookSecret(ctx, response))
	assert.NilError(t, w.updateRepositoryCR(ctx, response))
	assert.NilError(t, w.updateWebhookSecret(ctx, response))
	golden.Assert(t, out.String(), strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
}
