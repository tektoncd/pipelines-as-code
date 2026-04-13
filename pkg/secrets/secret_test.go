package secrets

import (
	"fmt"
	"regexp"
	"testing"

	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
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
					DefaultGitProviderSecretKey)),
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
				k8int, tt.providerconfig, event, tt.repo, tt.providerType, "namespace", logger,
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
