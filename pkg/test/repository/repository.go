package repository

import (
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RepoTestcreationOpts struct {
	Name              string
	URL               string
	InstallNamespace  string
	SecretName        string
	WebhookSecretName string
	ProviderURL       string
	GitProviderType   string
	CreateTime        metav1.Time
	ConcurrencyLimit  int
	Settings          *v1alpha1.Settings
	Params            *[]v1alpha1.Params
}

func NewRepo(opts RepoTestcreationOpts) *v1alpha1.Repository {
	repo := &v1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:              opts.Name,
			Namespace:         opts.InstallNamespace,
			CreationTimestamp: opts.CreateTime,
		},
		Spec: v1alpha1.RepositorySpec{
			URL:      opts.URL,
			Settings: opts.Settings,
		},
	}
	if opts.ConcurrencyLimit > 0 {
		repo.Spec.ConcurrencyLimit = &opts.ConcurrencyLimit
	}

	if opts.SecretName != "" || opts.ProviderURL != "" || opts.WebhookSecretName != "" || opts.GitProviderType != "" {
		repo.Spec.GitProvider = &v1alpha1.GitProvider{
			Secret: &v1alpha1.Secret{},
		}
	}

	if opts.SecretName != "" {
		repo.Spec.GitProvider.Secret = &v1alpha1.Secret{
			Name: opts.SecretName,
		}
	}
	if opts.ProviderURL != "" {
		repo.Spec.GitProvider.URL = opts.ProviderURL
	}

	if opts.WebhookSecretName != "" {
		repo.Spec.GitProvider.WebhookSecret = &v1alpha1.Secret{
			Name: opts.WebhookSecretName,
		}
	}

	if opts.GitProviderType != "" {
		repo.Spec.GitProvider.Type = opts.GitProviderType
	}

	if opts.Params != nil {
		repo.Spec.Params = opts.Params
	}

	return repo
}
