package secrets

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

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
		expectedTokenExpiry     string
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
		{
			name: "bitbucket cloud access token git user",
			event: info.Event{
				URL: "https://bitbucket.org/workspace/repo",
				Provider: &info.Provider{
					User:  "user@example.com",
					Token: "ATCT-access-token",
				},
			},
			expectedGitConfigURL:    "https://bitbucket.org",
			expectedGitCredentials:  "https://x-token-auth:ATCT-access-token@bitbucket.org/workspace/repo",
			expectedStartSecretName: "pac-gitauth-access-token",
		},
		{
			name:     "github app sets UTC RFC3339 token expiry annotation",
			targetNS: nsthere,
			event: info.Event{
				Organization:   "owner",
				Repository:     "repo",
				URL:            "https://github.com/owner/repo",
				InstallationID: 42,
				Provider: &info.Provider{
					Token:                secrete,
					GitHubTokenExpiresAt: time.Date(2026, 11, 20, 14, 30, 0, 0, time.FixedZone("UTC+2", 2*3600)),
				},
			},
			expectedGitConfigURL:    "https://github.com",
			expectedGitCredentials:  "https://git:verysecrete@github.com/owner/repo",
			expectedStartSecretName: "pac-gitauth-owner-repo",
			expectedTokenExpiry:     "2026-11-20T12:30:00Z",
		},
		{
			name:     "github app with zero token expiry omits expiry annotation",
			targetNS: nsthere,
			event: info.Event{
				Organization:   "owner",
				Repository:     "repo",
				URL:            "https://github.com/owner/repo",
				InstallationID: 42,
				Provider: &info.Provider{
					Token: secrete,
				},
			},
			expectedGitConfigURL:    "https://github.com",
			expectedGitCredentials:  "https://git:verysecrete@github.com/owner/repo",
			expectedStartSecretName: "pac-gitauth-owner-repo",
		},
		{
			name:     "non-app credential with token expiry omits expiry annotation",
			targetNS: nsthere,
			event: info.Event{
				Organization: "owner",
				Repository:   "repo",
				URL:          "https://github.com/owner/repo",
				Provider: &info.Provider{
					Token:                secrete,
					GitHubTokenExpiresAt: time.Date(2026, 11, 20, 12, 30, 0, 0, time.UTC),
				},
			},
			expectedGitConfigURL:    "https://github.com",
			expectedGitCredentials:  "https://git:verysecrete@github.com/owner/repo",
			expectedStartSecretName: "pac-gitauth-owner-repo",
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
			gotExpiry, hasExpiry := secret.GetAnnotations()[keys.GitHubTokenExpiry]
			if tt.expectedTokenExpiry != "" {
				assert.Assert(t, hasExpiry, "expected %s annotation to be set", keys.GitHubTokenExpiry)
				assert.Equal(t, gotExpiry, tt.expectedTokenExpiry)
			} else {
				assert.Assert(t, !hasExpiry, "expected %s annotation to be absent, got %q", keys.GitHubTokenExpiry, gotExpiry)
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

// TestMakeBasicAuthSecretCloneURLHostValidation checks that MakeBasicAuthSecret
// only reuses the provider credential for a clone URL on the same host as the
// repository URL.
func TestMakeBasicAuthSecretCloneURLHostValidation(t *testing.T) {
	tests := []struct {
		name          string
		event         info.Event
		wantErr       string
		wantCredsHost string
	}{
		{
			name: "clone URL on a different host is rejected",
			event: info.Event{
				URL:      "https://bbdc.example.com/scm/PROJ/repo",
				CloneURL: "https://other.example.com/scm/PROJ/repo.git",
				Provider: &info.Provider{Token: "provider-token", User: "pac"},
			},
			wantErr: "does not match the host of repository URL",
		},
		{
			name: "clone URL with an empty repository URL is rejected",
			event: info.Event{
				URL:      "",
				CloneURL: "https://bbdc.example.com/scm/PROJ/repo.git",
				Provider: &info.Provider{Token: "provider-token", User: "pac"},
			},
			wantErr: "does not match the host of repository URL",
		},
		{
			name: "no clone URL falls back to the repository URL",
			event: info.Event{
				URL:      "https://bbdc.example.com/scm/PROJ/repo",
				CloneURL: "",
				Provider: &info.Provider{Token: "provider-token", User: "pac"},
			},
			wantCredsHost: "bbdc.example.com",
		},
		{
			name: "clone URL on the same host with a different path is allowed",
			event: info.Event{
				URL:      "https://bbdc.example.com/scm/PROJ/repo",
				CloneURL: "https://bbdc.example.com/scm/PROJ/repo.git",
				Provider: &info.Provider{Token: "provider-token", User: "pac"},
			},
			wantCredsHost: "bbdc.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, err := MakeBasicAuthSecret(&tt.event, "test-secret")
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}

			assert.NilError(t, err)
			gitCredentials := secret.StringData[".git-credentials"]
			gitConfig := secret.StringData[".gitconfig"]
			assert.Assert(t, strings.Contains(gitCredentials, tt.wantCredsHost),
				"git-credentials should be bound to host %s, got: %s", tt.wantCredsHost, gitCredentials)
			assert.Assert(t, strings.Contains(gitConfig, tt.wantCredsHost),
				"gitconfig should reference host %s, got: %s", tt.wantCredsHost, gitConfig)
		})
	}
}
