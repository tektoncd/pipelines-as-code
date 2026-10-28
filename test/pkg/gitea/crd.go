package gitea

import (
	"context"
	"fmt"
	"os"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	pacrepo "github.com/tektoncd/pipelines-as-code/test/pkg/repository"
	"github.com/tektoncd/pipelines-as-code/test/pkg/secret"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const webhookSecretName = "webhook-secret"

// CreateToken creates gitea token with all scopes.
// It creates the token for the authenticated admin user rather than the org,
// because Forgejo 13+ doesn't allow org tokens to list org teams.
func CreateToken(topts *TestOpts) (string, error) {
	userInfo, _, err := topts.GiteaCNX.Client().GetMyUserInfo()
	if err != nil {
		return "", fmt.Errorf("failed to get current user info: %w", err)
	}
	token, _, err := topts.GiteaCNX.Client().CreateAccessToken(userInfo.UserName, forgejo.CreateAccessTokenOption{
		Name:   topts.TargetNS,
		Scopes: []forgejo.AccessTokenScope{forgejo.AccessTokenScopeAll},
	})
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

func CreateCRD(ctx context.Context, topts *TestOpts, spec v1alpha1.RepositorySpec, isGlobal bool) error {
	var ns string
	if isGlobal {
		ns = info.GetNS(ctx)
	} else {
		ns = topts.TargetNS
	}

	if spec.GitProvider != nil && spec.GitProvider.Secret != nil {
		secretName := spec.GitProvider.Secret.Name
		if err := topts.ParamsRun.Clients.Kube.CoreV1().Secrets(ns).Delete(ctx, secretName, metav1.DeleteOptions{}); err == nil {
			if isGlobal {
				topts.ParamsRun.Clients.Log.Infof("Secret global %s has been deleted in %s", secretName, ns)
			} else {
				topts.ParamsRun.Clients.Log.Infof("Secret %s has been deleted in %s", secretName, ns)
			}
		}
		if isGlobal {
			topts.ParamsRun.Clients.Log.Infof("Creating global secret %s for global repository in %s", secretName, ns)
		}
		if err := secret.Create(ctx, topts.ParamsRun, map[string]string{"token": topts.Token}, ns, secretName); err != nil {
			return err
		}
	}

	_ = topts.ParamsRun.Clients.Kube.CoreV1().Secrets(ns).Delete(ctx, webhookSecretName, metav1.DeleteOptions{})
	webhookSecret, _ := os.LookupEnv("TEST_EL_WEBHOOK_SECRET")
	if err := secret.Create(ctx, topts.ParamsRun, map[string]string{"secret": webhookSecret}, ns, webhookSecretName); err != nil {
		return err
	}

	if spec.GitProvider != nil {
		spec.GitProvider.WebhookSecret = &v1alpha1.Secret{Name: webhookSecretName, Key: "secret"}
	}
	repoName := ns
	if isGlobal {
		repoName = info.DefaultGlobalRepoName
	}
	repository := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: repoName,
		},
		Spec: spec,
	}

	if isGlobal {
		_ = topts.ParamsRun.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(ns).Delete(ctx, repository.GetName(), metav1.DeleteOptions{})
	}

	return pacrepo.CreateRepo(ctx, ns, topts.ParamsRun, repository)
}
