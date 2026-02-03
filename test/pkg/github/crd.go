package github

import (
	"context"
	"os"
	"testing"

	"github.com/google/go-github/v81/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	ghprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/secret"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateCRD(ctx context.Context, t *testing.T, repoinfo *github.Repository, run *params.Run, opts options.E2E, provider *ghprovider.Provider, targetNS string) error {
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: targetNS,
		},
		Spec: v1alpha1.RepositorySpec{
			URL:      repoinfo.GetHTMLURL(),
			Settings: opts.Settings,
		},
	}

	if opts.Concurrency != 0 {
		repo.Spec.ConcurrencyLimit = intPtr(opts.Concurrency)
	}

	err := repository.CreateNS(ctx, targetNS, run)
	assert.NilError(t, err)

	if opts.DirectWebhook {
		token, apiURL := getTokenAndURL(provider)
		webhookSecret, _ := os.LookupEnv("TEST_EL_WEBHOOK_SECRET")

		err := secret.Create(ctx, run,
			map[string]string{
				"webhook-secret": webhookSecret,
				"token":          token,
			},
			targetNS,
			"webhook-token")
		assert.NilError(t, err)
		repo.Spec.GitProvider = &v1alpha1.GitProvider{
			URL: apiURL,
			Secret: &v1alpha1.Secret{
				Name: "webhook-token",
				Key:  "token",
			},
			WebhookSecret: &v1alpha1.Secret{
				Name: "webhook-token",
				Key:  "webhook-secret",
			},
		}
	}

	err = repository.CreateRepo(ctx, targetNS, run, repo)
	assert.NilError(t, err)
	return err
}

var intPtr = func(val int) *int { return &val }

func CreateCRDIncoming(ctx context.Context, t *testing.T, repoinfo *github.Repository, run *params.Run, incomings *[]v1alpha1.Incoming, opts options.E2E, provider *ghprovider.Provider, targetNS string) error {
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: targetNS,
		},
		Spec: v1alpha1.RepositorySpec{
			URL:       repoinfo.GetHTMLURL(),
			Incomings: incomings,
		},
	}

	if opts.Concurrency != 0 {
		repo.Spec.ConcurrencyLimit = intPtr(opts.Concurrency)
	}

	err := repository.CreateNS(ctx, targetNS, run)
	assert.NilError(t, err)

	if opts.DirectWebhook {
		token, apiURL := getTokenAndURL(provider)
		webhookSecret, _ := os.LookupEnv("TEST_EL_WEBHOOK_SECRET")

		err := secret.Create(ctx, run,
			map[string]string{
				"webhook-secret": webhookSecret,
				"token":          token,
			},
			targetNS,
			"webhook-token")
		assert.NilError(t, err)
		repo.Spec.GitProvider = &v1alpha1.GitProvider{
			// Type must be set explicitly so the incoming webhook handler
			// (adapter.detectIncoming) knows this is a direct webhook setup.
			// Without it, GitProvider.Type == "" causes the controller to
			// assume GitHub App mode and attempt to fetch the global
			// pipelines-as-code-secret, which doesn't exist for direct webhooks.
			Type: "github",
			URL:  apiURL,
			Secret: &v1alpha1.Secret{
				Name: "webhook-token",
				Key:  "token",
			},
			WebhookSecret: &v1alpha1.Secret{
				Name: "webhook-token",
				Key:  "webhook-secret",
			},
		}
	}

	err = repository.CreateRepo(ctx, targetNS, run, repo)
	assert.NilError(t, err)
	return err
}

func getTokenAndURL(provider *ghprovider.Provider) (string, string) {
	token := ""
	if provider.Token != nil {
		token = *provider.Token
	}
	if token == "" {
		token, _ = os.LookupEnv("TEST_GITHUB_TOKEN")
	}

	apiURL := ""
	if provider.APIURL != nil {
		apiURL = *provider.APIURL
	}
	if apiURL == "" {
		apiURL, _ = os.LookupEnv("TEST_GITHUB_API_URL")
	}

	return token, apiURL
}
