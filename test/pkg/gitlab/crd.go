package gitlab

import (
	"context"
	"os"

	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pacrepo "github.com/tektoncd/pipelines-as-code/test/pkg/repository"
	"github.com/tektoncd/pipelines-as-code/test/pkg/secret"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateCRD(ctx context.Context, topts *TestOpts) error {
	if err := pacrepo.CreateNS(ctx, topts.TargetNS, topts.ParamsRun); err != nil {
		return err
	}

	token, _ := os.LookupEnv("TEST_GITLAB_TOKEN")
	webhookSecret, _ := os.LookupEnv("TEST_EL_WEBHOOK_SECRET")
	apiURL, _ := os.LookupEnv("TEST_GITLAB_API_URL")
	if err := secret.Create(ctx, topts.ParamsRun, map[string]string{"token": token}, topts.TargetNS, "webhook-token"); err != nil {
		return err
	}
	if err := secret.Create(ctx, topts.ParamsRun, map[string]string{"secret": webhookSecret}, topts.TargetNS, "webhook-secret"); err != nil {
		return err
	}
	repository := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: topts.TargetNS,
		},
		Spec: v1alpha1.RepositorySpec{
			Settings: topts.Settings,
			URL:      topts.GitHTMLURL,
			GitProvider: &v1alpha1.GitProvider{
				Type:          "gitlab",
				URL:           apiURL,
				Secret:        &v1alpha1.Secret{Name: "webhook-token", Key: "token"},
				WebhookSecret: &v1alpha1.Secret{Name: "webhook-secret", Key: "secret"},
			},
			Incomings: topts.Incomings,
		},
	}

	return pacrepo.CreateRepo(ctx, topts.TargetNS, topts.ParamsRun, repository)
}
