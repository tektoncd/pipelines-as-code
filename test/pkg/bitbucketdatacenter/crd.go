package bitbucketdatacenter

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	pacrepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/secret"

	"github.com/jenkins-x/go-scm/scm"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateCRD(ctx context.Context, t *testing.T, client *scm.Client, run *params.Run, orgAndRepo, targetNS string) *scm.Repository {
	repo, resp, err := client.Repositories.Find(ctx, orgAndRepo)
	assert.NilError(t, err, "error getting repository: http status code: %d: %v", resp.Status, err)

	url := strings.ReplaceAll(repo.Link, "/browse", "")
	repository := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: targetNS,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: url,
		},
	}

	err = pacrepo.CreateNS(ctx, targetNS, run)
	assert.NilError(t, err)
	run.Clients.Log.Infof("Namespace %s is created", targetNS)

	token, _ := os.LookupEnv("TEST_BITBUCKET_DATA_CENTER_TOKEN")
	apiURL, _ := os.LookupEnv("TEST_BITBUCKET_DATA_CENTER_API_URL")
	apiUser, _ := os.LookupEnv("TEST_BITBUCKET_DATA_CENTER_USER")
	webhookSecret := os.Getenv("TEST_BITBUCKET_DATA_CENTER_WEBHOOK_SECRET")
	secretName := "bitbucket-datacenter-webhook-config" // #nosec G101
	err = secret.Create(ctx, run, map[string]string{
		"provider.token": token,
		"webhook.secret": webhookSecret,
	}, targetNS, secretName)
	assert.NilError(t, err)
	run.Clients.Log.Infof("PipelinesAsCode Secret %s is created", secretName)

	repository.Spec.GitProvider = &v1alpha1.GitProvider{
		URL:           apiURL,
		User:          apiUser,
		Secret:        &v1alpha1.Secret{Name: secretName},
		WebhookSecret: &v1alpha1.Secret{Name: secretName},
	}

	err = pacrepo.CreateRepo(ctx, targetNS, run, repository)
	assert.NilError(t, err, "error creating PipelinesAsCode Repository CR: %v", err)

	return repo
}
