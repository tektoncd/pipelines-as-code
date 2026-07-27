package bitbucketcloud

import (
	"context"
	"os"
	"testing"

	"github.com/ktrysmt/go-bitbucket"
	"github.com/mitchellh/mapstructure"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	pacrepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/secret"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateCRD(ctx context.Context, t *testing.T, bprovider bitbucketcloud.Provider, run *params.Run, opts options.E2E, targetNS string) *bitbucket.Repository {
	repo, err := bprovider.Client().Workspaces.Repositories.Repository.Get(
		&bitbucket.RepositoryOptions{
			Owner:    opts.Organization,
			RepoSlug: opts.Repo,
		},
	)
	assert.NilError(t, err, "failed to get repository %s/%s: %v", opts.Organization, opts.Repo, err)

	links := &types.Links{}
	err = mapstructure.Decode(repo.Links, links)
	assert.NilError(t, err)
	repository := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: targetNS,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: links.HTML.HRef,
		},
	}
	err = pacrepo.CreateNS(ctx, targetNS, run)
	assert.NilError(t, err)

	token, _ := os.LookupEnv("TEST_BITBUCKET_CLOUD_TOKEN")
	apiURL, _ := os.LookupEnv("TEST_BITBUCKET_CLOUD_API_URL")
	apiUser, _ := os.LookupEnv("TEST_BITBUCKET_CLOUD_USER")
	webhookSecret, _ := os.LookupEnv("TEST_BITBUCKET_CLOUD_WEBHOOK_SECRET")
	err = secret.Create(ctx, run, map[string]string{"token": token, "webhook-secret": webhookSecret}, targetNS, "webhook-token")
	assert.NilError(t, err)
	repository.Spec.GitProvider = &v1alpha1.GitProvider{
		URL:           apiURL,
		User:          apiUser,
		Secret:        &v1alpha1.Secret{Name: "webhook-token", Key: "token"},
		WebhookSecret: &v1alpha1.Secret{Name: "webhook-token", Key: "webhook-secret"},
	}

	err = pacrepo.CreateRepo(ctx, targetNS, run, repository)
	assert.NilError(t, err)

	return repo
}
