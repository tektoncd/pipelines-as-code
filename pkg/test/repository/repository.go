package repository

import (
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
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
	RepoStatus        []v1alpha1.RepositoryRunStatus
	ConcurrencyLimit  int
	Settings          *v1alpha1.Settings
	Params            *[]v1alpha1.Params
}

func NewRepo(opts RepoTestcreationOpts) *v1alpha1.Repository {
	cw := clockwork.NewFakeClock()

	if opts.RepoStatus == nil {
		opts.RepoStatus = []v1alpha1.RepositoryRunStatus{
			{
				Status:          knativeduckv1.Status{},
				PipelineRunName: "pipelinerun5",
				StartTime:       &metav1.Time{Time: cw.Now().Add(-56 * time.Minute)},
				CompletionTime:  &metav1.Time{Time: cw.Now().Add(-55 * time.Minute)},
			},
			{
				Status:          knativeduckv1.Status{},
				PipelineRunName: "pipelinerun4",
				StartTime:       &metav1.Time{Time: cw.Now().Add(-46 * time.Minute)},
				CompletionTime:  &metav1.Time{Time: cw.Now().Add(-45 * time.Minute)},
			},
			{
				Status:          knativeduckv1.Status{},
				PipelineRunName: "pipelinerun3",
				StartTime:       &metav1.Time{Time: cw.Now().Add(-36 * time.Minute)},
				CompletionTime:  &metav1.Time{Time: cw.Now().Add(-35 * time.Minute)},
			},
			{
				Status:          knativeduckv1.Status{},
				PipelineRunName: "pipelinerun2",
				StartTime:       &metav1.Time{Time: cw.Now().Add(-26 * time.Minute)},
				CompletionTime:  &metav1.Time{Time: cw.Now().Add(-25 * time.Minute)},
			},
			{
				Status:          knativeduckv1.Status{},
				PipelineRunName: "pipelinerun1",
				StartTime:       &metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
				CompletionTime:  &metav1.Time{Time: cw.Now().Add(-15 * time.Minute)},
			},
		}
	}

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
		Status: opts.RepoStatus,
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
