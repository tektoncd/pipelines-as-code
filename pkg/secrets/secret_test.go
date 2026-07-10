package secrets

import (
	"fmt"
	"regexp"
	"testing"

	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestSecretFromRepository(t *testing.T) {
	tests := []struct {
		name                  string
		repo                  *apipac.Repository
		providerconfig        *info.ProviderConfig
		logmatch              []*regexp.Regexp
		expectedSecret        string
		expectedWebhookSecret string
		providerType          string
	}{
		{
			name: "config default",
			providerconfig: &info.ProviderConfig{
				APIURL: "https://apiurl.default",
			},
			expectedSecret:        "configdefault",
			expectedWebhookSecret: "webhooksecret",
			repo: &apipac.Repository{
				Spec: apipac.RepositorySpec{
					GitProvider: &apipac.GitProvider{
						Secret: &apipac.Secret{
							Name: "repo-secret",
						},
						WebhookSecret: &apipac.Secret{
							Name: "repo-webhook-secret",
						},
					},
				},
			},
			providerType: "lalala",
			logmatch: []*regexp.Regexp{
				regexp.MustCompile(fmt.Sprintf(
					"^Using git provider lalala: apiurl=https://apiurl.default user= token-secret=repo-secret token-key=%s",
					DefaultGitProviderSecretKey,
				)),
			},
		},
		{
			name: "set api url",
			providerconfig: &info.ProviderConfig{
				APIURL: "https://donotwant",
			},
			repo: &apipac.Repository{
				Spec: apipac.RepositorySpec{
					GitProvider: &apipac.GitProvider{
						URL:    "https://dowant",
						Secret: &apipac.Secret{},
					},
				},
			},
			expectedSecret: "setapiurl",
			logmatch: []*regexp.Regexp{
				regexp.MustCompile(".*apiurl=https://dowant.*"),
			},
		},
		{
			name:           "set user",
			providerconfig: &info.ProviderConfig{},
			repo: &apipac.Repository{
				Spec: apipac.RepositorySpec{
					GitProvider: &apipac.GitProvider{
						User:   "userfoo",
						Secret: &apipac.Secret{},
					},
				},
			},
			expectedSecret: "set user",
			logmatch: []*regexp.Regexp{
				regexp.MustCompile(".*user=userfoo*"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, log := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			retsecret := map[string]string{}
			if tt.repo.Spec.GitProvider.Secret != nil {
				retsecret[tt.repo.Spec.GitProvider.Secret.Name] = tt.expectedSecret
			} else {
				tt.repo.Spec.GitProvider.Secret = &apipac.Secret{}
			}
			if tt.repo.Spec.GitProvider.WebhookSecret != nil {
				retsecret[tt.repo.Spec.GitProvider.WebhookSecret.Name] = tt.expectedWebhookSecret
			} else {
				tt.repo.Spec.GitProvider.WebhookSecret = &apipac.Secret{}
			}

			k8int := &kitesthelper.KinterfaceTest{
				GetSecretResult: retsecret,
			}
			event := info.NewEvent()
			sfr := SecretFromRepository{
				K8int:       k8int,
				Config:      tt.providerconfig,
				Event:       event,
				Repo:        tt.repo,
				WebhookType: tt.providerType,
				Namespace:   "namespace",
				Logger:      logger,
			}

			err := sfr.Get(ctx)
			assert.NilError(t, err)
			logs := log.TakeAll()
			assert.Equal(t, len(tt.logmatch), len(logs), "we didn't get the number of logging message: %+v", logs)
			for key, value := range logs {
				assert.Assert(t, tt.logmatch[key].MatchString(value.Message), "no match on logs %s => %s", tt.logmatch[key], value.Message)
			}
			assert.Equal(t, tt.expectedSecret, event.Provider.Token)
		})
	}
}

func TestSecretFromRepositoryError(t *testing.T) {
	namespace := "test"
	tdata := testclient.Data{
		Secret: []*corev1.Secret{{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "good-name",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"good-key": []byte(`keep it secret, keep it safe`),
			},
		}},
	}

	tests := []struct {
		name     string
		repoSpec apipac.RepositorySpec
		wantErr  string
	}{
		{
			name: "no git provider",
			repoSpec: apipac.RepositorySpec{
				GitProvider: nil,
			},
			wantErr: "failed to find git_provider details",
		},
		{
			name: "no git provider secret",
			repoSpec: apipac.RepositorySpec{
				GitProvider: &apipac.GitProvider{},
			},
			wantErr: "failed to find secret in git_provider section in repository",
		},
		{
			name: "git provider secret doesn't exist",
			repoSpec: apipac.RepositorySpec{
				GitProvider: &apipac.GitProvider{
					Secret: &apipac.Secret{Name: "bad-name"},
				},
			},
			wantErr: "error getting provider secret",
		},
		{
			name: "git provider secret bad key",
			repoSpec: apipac.RepositorySpec{
				GitProvider: &apipac.GitProvider{
					Secret: &apipac.Secret{Name: "good-name", Key: "bad-key"},
				},
			},
			wantErr: "",
		},
		{
			// webhook secret being unspecified is OK, but if it is specified it must exist
			name: "webhook secret missing",
			repoSpec: apipac.RepositorySpec{
				GitProvider: &apipac.GitProvider{
					Secret:        &apipac.Secret{Name: "good-name", Key: "good-key"},
					WebhookSecret: &apipac.Secret{Name: "bad-name"},
				},
			},
			wantErr: "error getting webhook secret",
		},
		{
			name: "webhook secret bad key",
			repoSpec: apipac.RepositorySpec{
				GitProvider: &apipac.GitProvider{
					Secret:        &apipac.Secret{Name: "good-name", Key: "good-key"},
					WebhookSecret: &apipac.Secret{Name: "good-name", Key: "bad-key"},
				},
			},
			wantErr: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			run := &params.Run{
				Clients: clients.Clients{
					Kube:           stdata.Kube,
					PipelineAsCode: stdata.PipelineAsCode,
					Log:            logger,
				},
			}
			kint, err := kubeinteraction.NewKubernetesInteraction(run)
			assert.NilError(t, err)

			repo := &apipac.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: tt.name, Namespace: namespace},
				Spec:       tt.repoSpec,
			}

			event := info.NewEvent()
			sfr := SecretFromRepository{
				K8int:       kint,
				Config:      &info.ProviderConfig{APIURL: "https://fake"},
				Event:       event,
				Repo:        repo,
				WebhookType: "subversion",
				Namespace:   repo.Namespace,
				Logger:      logger,
			}

			err = sfr.Get(ctx)
			if tt.wantErr != "" {
				assert.Assert(t, err != nil, "expected error: "+tt.wantErr)
				assert.ErrorContains(t, err, tt.wantErr)
				assert.ErrorIs(t, err, ErrSecretNotFound)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}
