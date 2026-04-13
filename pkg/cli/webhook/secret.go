package webhook

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (w *Options) createWebhookSecret(ctx context.Context, response *response) error {
	_, err := w.Run.Clients.Kube.CoreV1().Secrets(w.RepositoryNamespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: w.RepositoryName,
		},
		Data: map[string][]byte{
			secrets.DefaultGitProviderSecretKey:        []byte(response.PersonalAccessToken),
			secrets.DefaultGitProviderWebhookSecretKey: []byte(response.WebhookSecret),
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	fmt.Fprintf(w.IOStreams.Out, "🔑 Webhook Secret %s has been created in the %s namespace.\n", w.RepositoryName, w.RepositoryNamespace)
	return nil
}

func (w *Options) updateWebhookSecret(ctx context.Context, response *response) error {
	secretInfo, err := w.Run.Clients.Kube.CoreV1().Secrets(w.RepositoryNamespace).Get(ctx, w.SecretName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	secretInfo.Data[secrets.DefaultGitProviderWebhookSecretKey] = []byte(response.WebhookSecret)

	_, err = w.Run.Clients.Kube.CoreV1().Secrets(w.RepositoryNamespace).Update(ctx, secretInfo, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	fmt.Fprintf(w.IOStreams.Out, "🔑 Secret %s has been updated with webhook secret in the %s namespace.\n", w.SecretName, w.RepositoryNamespace)
	return nil
}

func (w *Options) updateRepositoryCR(ctx context.Context, res *response) error {
	repo, err := w.Run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(w.RepositoryNamespace).
		Get(ctx, w.RepositoryName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if repo.Spec.GitProvider == nil {
		repo.Spec.GitProvider = &v1alpha1.GitProvider{}
	}

	repo.Spec.GitProvider.Secret = &v1alpha1.Secret{
		Name: w.RepositoryName,
		Key:  secrets.DefaultGitProviderSecretKey,
	}
	repo.Spec.GitProvider.WebhookSecret = &v1alpha1.Secret{
		Name: w.RepositoryName,
		Key:  secrets.DefaultGitProviderWebhookSecretKey,
	}

	if res.UserName != "" {
		repo.Spec.GitProvider.User = res.UserName
	}

	if res.APIURL != "" {
		repo.Spec.GitProvider.URL = res.APIURL
	}

	_, err = w.Run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(w.RepositoryNamespace).
		Update(ctx, repo, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	fmt.Fprintf(w.IOStreams.Out, "🔑 Repository CR %s has been updated with webhook secret in the %s namespace\n", w.RepositoryName, w.RepositoryNamespace)
	return nil
}
