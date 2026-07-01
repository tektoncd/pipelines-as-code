package secrets

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"gotest.tools/v3/assert"
)

func TestCreateBasicAuthSecret(t *testing.T) {
	nsNotThere := "not_there"
	nsthere := "there"
	secrete := "verysecrete"

	event := info.Event{
		Organization: "owner",
		Repository:   "repo",
		URL:          "https://forge/owner/repo",
	}

	tests := []struct {
		name                    string
		targetNS                string
		event                   info.Event
		expectedGitConfigURL    string
		expectedGitCredentials  string
		expectedStartSecretName string
		expectedError           bool
		expectedLabels          map[string]string
	}{
		{
			name:                    "Target secret not there",
			targetNS:                nsNotThere,
			event:                   event,
			expectedGitConfigURL:    "https://forge",
			expectedGitCredentials:  "https://git:verysecrete@forge/owner/repo",
			expectedStartSecretName: "pac-gitauth-owner-repo",
			expectedLabels: map[string]string{
				"app.kubernetes.io/managed-by": "pipelinesascode.tekton.dev",
				keys.URLOrg:                    "owner",
				keys.URLRepository:             "repo",
			},
		},
		{
			name:     "Cleaned up gitlab style long repo and organisation name",
			targetNS: nsthere,
			event: info.Event{
				Organization: "owner/foo/bar/linux/kernel",
				Repository:   "yoyo",
				URL:          "https://forge/owner/yoyo/foo/bar/linux/kernel",
			},
			expectedGitConfigURL:    "https://forge",
			expectedGitCredentials:  "https://git:verysecrete@forge/owner/yoyo/foo/bar/linux/kernel",
			expectedStartSecretName: "pac-gitauth-owner-repo",
			expectedLabels: map[string]string{
				"app.kubernetes.io/managed-by": "pipelinesascode.tekton.dev",
				keys.URLOrg:                    "owner-foo-bar-linux-kernel",
				keys.URLRepository:             "yoyo",
			},
		},
		{
			name:                    "Use clone URL",
			targetNS:                nsNotThere,
			event:                   event,
			expectedGitConfigURL:    "https://forge",
			expectedGitCredentials:  "https://git:verysecrete@forge/owner/repo",
			expectedStartSecretName: "pac-gitauth-owner-repo",
		},
		{
			name:                    "Target secret already there",
			targetNS:                nsthere,
			event:                   event,
			expectedGitConfigURL:    "https://forge",
			expectedGitCredentials:  "https://git:verysecrete@forge/owner/repo",
			expectedStartSecretName: "pac-gitauth-owner-repo",
		},
		{
			name:     "Lowercase secrets",
			targetNS: nsthere,
			event: info.Event{
				Organization: "UPPER",
				Repository:   "CASE",
				URL:          "https://forge/UPPER/CASE",
			},
			expectedGitConfigURL:    "https://forge",
			expectedGitCredentials:  "https://git:verysecrete@forge/UPPER/CASE",
			expectedStartSecretName: "pac-gitauth-upper-case",
		},
		{
			name:     "Use clone URL",
			targetNS: nsthere,
			event: info.Event{
				Organization: "hello",
				Repository:   "moto",
				URL:          "https://forge/hello/moto",
				CloneURL:     "https://forge/miss/robinson",
			},
			expectedGitConfigURL:    "https://forge",
			expectedGitCredentials:  "https://git:verysecrete@forge/miss/robinson",
			expectedStartSecretName: "pac-gitauth-upper-case",
		},
		{
			name:     "different git user",
			targetNS: nsthere,
			event: info.Event{
				Organization: "hello",
				Repository:   "moto",
				URL:          "https://forge/bat/cave",
				Provider: &info.Provider{
					User:  "superman",
					Token: "supersecrete",
				},
			},
			expectedGitConfigURL:    "https://forge",
			expectedGitCredentials:  "https://superman:supersecrete@forge/bat/cave",
			expectedStartSecretName: "pac-gitauth-upper-case",
		},
		{
			name:     "bitbucket cloud API token git user",
			targetNS: nsthere,
			event: info.Event{
				Organization: "workspace",
				Repository:   "repo",
				URL:          "https://bitbucket.org/workspace/repo",
				Provider: &info.Provider{
					User:  "user@example.com",
					Token: "api-token",
				},
			},
			expectedGitConfigURL:    "https://bitbucket.org",
			expectedGitCredentials:  "https://x-bitbucket-api-token-auth:api-token@bitbucket.org/workspace/repo",
			expectedStartSecretName: "pac-gitauth-upper-case",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.event.Provider == nil {
				tt.event.Provider = &info.Provider{
					Token: secrete,
				}
			}
			secret, err := MakeBasicAuthSecret(&tt.event, tt.expectedStartSecretName)
			assert.NilError(t, err)
			if len(tt.expectedLabels) > 0 {
				if d := cmp.Diff(secret.GetLabels(), tt.expectedLabels); d != "" {
					t.Fatalf("-got, +want: %v", d)
				}
			}
			assert.Assert(t, strings.HasPrefix(secret.GetName(), tt.expectedStartSecretName))
			gitConfig := secret.StringData[".gitconfig"]
			regPattern := fmt.Sprintf("\\[credential\\s+\\\"%s\\\"\\]", tt.expectedGitConfigURL)
			match, err := regexp.MatchString(regPattern, gitConfig)
			assert.NilError(t, err)
			assert.Assert(t, match, ".gitconfig URL should not have path component: %s", gitConfig)
			assert.Equal(t, secret.StringData[".git-credentials"], tt.expectedGitCredentials)
		})
	}
}

func TestGetBasicAuthSecret(t *testing.T) {
	t1 := GenerateBasicAuthSecretName()
	t2 := GenerateBasicAuthSecretName()
	assert.Assert(t, t1 != t2)
}
