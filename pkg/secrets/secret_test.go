package secrets

import (
	"fmt"
	"regexp"
	"testing"

	apipac "github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	kitesthelper "github.com/tektoncd/pipelines-as-code/pkg/test/kubernetestint"

	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestSecretFromRepository(t *testing.T) {
	tests := []struct {
		name           string
		repo           *apipac.Repository
		providerconfig *info.ProviderConfig
		providerType   string
		secrets        map[string]string
		logmatch       []*regexp.Regexp
		expectedSecret string
		wantErr        string
		wantErrIs      error
	}{
		{
			name: "config default",
			providerconfig: &info.ProviderConfig{
				APIURL: "https://apiurl.default",
			},
			repo: &apipac.Repository{
				Spec: apipac.RepositorySpec{
					GitProvider: &apipac.GitProvider{
						Secret:        &apipac.Secret{Name: "repo-secret"},
						WebhookSecret: &apipac.Secret{Name: "repo-webhook-secret"},
					},
				},
			},
			providerType: "lalala",
			secrets: map[string]string{
				"repo-secret":         "configdefault",
				"repo-webhook-secret": "webhooksecret",
			},
			expectedSecret: "configdefault",
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
						URL:           "https://dowant",
						Secret:        &apipac.Secret{Name: "provider-secret"},
						WebhookSecret: &apipac.Secret{Name: "webhook-secret"},
					},
				},
			},
			secrets: map[string]string{
				"provider-secret": "setapiurl",
				"webhook-secret":  "",
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
						User:          "userfoo",
						Secret:        &apipac.Secret{Name: "provider-secret"},
						WebhookSecret: &apipac.Secret{Name: "webhook-secret"},
					},
				},
			},
			secrets: map[string]string{
				"provider-secret": "set user",
				"webhook-secret":  "",
			},
			expectedSecret: "set user",
			logmatch: []*regexp.Regexp{
				regexp.MustCompile(".*user=userfoo*"),
			},
		},
		{
			name:           "no git provider",
			providerconfig: &info.ProviderConfig{APIURL: "https://fake"},
			providerType:   "subversion",
			repo: &apipac.Repository{
				Spec: apipac.RepositorySpec{GitProvider: nil},
			},
			wantErr: "failed to find git_provider details",
		},
		{
			name:           "no git provider secret",
			providerconfig: &info.ProviderConfig{APIURL: "https://fake"},
			providerType:   "subversion",
			repo: &apipac.Repository{
				Spec: apipac.RepositorySpec{GitProvider: &apipac.GitProvider{}},
			},
			wantErr: "failed to find secret in git_provider section in repository",
		},
		{
			name:           "git provider secret doesn't exist",
			providerconfig: &info.ProviderConfig{APIURL: "https://fake"},
			providerType:   "subversion",
			repo: &apipac.Repository{
				Spec: apipac.RepositorySpec{
					GitProvider: &apipac.GitProvider{
						Secret: &apipac.Secret{Name: "bad-name"},
					},
				},
			},
			wantErr:   "error getting provider secret",
			wantErrIs: ErrSecretNotFound,
		},
		{
			// a bad key on an existing secret is not an error, it just resolves to an empty value
			name:           "git provider secret bad key",
			providerconfig: &info.ProviderConfig{APIURL: "https://fake"},
			providerType:   "subversion",
			repo: &apipac.Repository{
				Spec: apipac.RepositorySpec{
					GitProvider: &apipac.GitProvider{
						Secret: &apipac.Secret{Name: "good-name", Key: "bad-key"},
					},
				},
			},
			secrets: map[string]string{
				"good-name": "keep it secret, keep it safe",
			},
			expectedSecret: "keep it secret, keep it safe",
		},
		{
			// webhook secret being unspecified is OK, but if it is specified it must exist
			name:           "webhook secret missing",
			providerconfig: &info.ProviderConfig{APIURL: "https://fake"},
			providerType:   "subversion",
			repo: &apipac.Repository{
				Spec: apipac.RepositorySpec{
					GitProvider: &apipac.GitProvider{
						Secret:        &apipac.Secret{Name: "good-name", Key: "good-key"},
						WebhookSecret: &apipac.Secret{Name: "bad-name"},
					},
				},
			},
			secrets: map[string]string{
				"good-name": "keep it secret, keep it safe",
			},
			wantErr:   "error getting webhook secret",
			wantErrIs: ErrSecretNotFound,
		},
		{
			name:           "webhook secret bad key",
			providerconfig: &info.ProviderConfig{APIURL: "https://fake"},
			providerType:   "subversion",
			repo: &apipac.Repository{
				Spec: apipac.RepositorySpec{
					GitProvider: &apipac.GitProvider{
						Secret:        &apipac.Secret{Name: "good-name", Key: "good-key"},
						WebhookSecret: &apipac.Secret{Name: "good-name", Key: "bad-key"},
					},
				},
			},
			secrets: map[string]string{
				"good-name": "keep it secret, keep it safe",
			},
			expectedSecret: "keep it secret, keep it safe",
			logmatch: []*regexp.Regexp{
				regexp.MustCompile("^Using git provider subversion:.*"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, log := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			k8int := &kitesthelper.KinterfaceTest{
				GetSecretResult: tt.secrets,
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
			if tt.wantErr != "" {
				assert.Assert(t, err != nil, "expected error: "+tt.wantErr)
				assert.ErrorContains(t, err, tt.wantErr)
				if tt.wantErrIs != nil {
					assert.ErrorIs(t, err, tt.wantErrIs)
				}
				return
			}
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
