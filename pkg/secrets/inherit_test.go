package secrets

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func inheritTestRepo(namespace, name, providerURL string, secret bool) *v1alpha1.Repository {
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: &v1alpha1.GitProvider{
				URL: providerURL,
			},
		},
	}
	if secret {
		repo.Spec.GitProvider.Secret = &v1alpha1.Secret{Name: "provider-secret"}
	}
	return repo
}

func TestResolveInheritedSecret(t *testing.T) {
	tests := []struct {
		name          string
		repo          *v1alpha1.Repository
		globalRepo    *v1alpha1.Repository
		wantNamespace string
		wantInherited bool
		wantErr       string
	}{
		{
			name:          "no global repository keeps the local namespace",
			repo:          inheritTestRepo("tenant", "repo", "", false),
			globalRepo:    nil,
			wantNamespace: "tenant",
		},
		{
			name:          "own secret is never inherited",
			repo:          inheritTestRepo("tenant", "repo", "https://ghe.example.com", true),
			globalRepo:    inheritTestRepo("pipelines-as-code", "global", "https://other.example.com", true),
			wantNamespace: "tenant",
		},
		{
			name:          "global repository without a secret grants nothing",
			repo:          inheritTestRepo("tenant", "repo", "", false),
			globalRepo:    inheritTestRepo("pipelines-as-code", "global", "", false),
			wantNamespace: "tenant",
		},
		{
			name: "repository without a git provider keeps the local namespace",
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "tenant"},
			},
			globalRepo:    inheritTestRepo("pipelines-as-code", "global", "", true),
			wantNamespace: "tenant",
		},
		{
			name:          "inheriting without an own url is allowed",
			repo:          inheritTestRepo("tenant", "repo", "", false),
			globalRepo:    inheritTestRepo("pipelines-as-code", "global", "https://ghe.example.com", true),
			wantNamespace: "pipelines-as-code",
			wantInherited: true,
		},
		{
			name:          "inheriting for the same endpoint is allowed",
			repo:          inheritTestRepo("tenant", "repo", "https://ghe.example.com", false),
			globalRepo:    inheritTestRepo("pipelines-as-code", "global", "https://ghe.example.com", true),
			wantNamespace: "pipelines-as-code",
			wantInherited: true,
		},
		{
			name:          "pointing the inherited credential at another host is refused",
			repo:          inheritTestRepo("tenant", "repo", "https://attacker.example.com", false),
			globalRepo:    inheritTestRepo("pipelines-as-code", "global", "https://ghe.example.com", true),
			wantNamespace: "tenant",
			wantErr:       "must not be sent to an endpoint chosen by another namespace",
		},
		{
			name:          "downgrading the inherited credential to cleartext is refused",
			repo:          inheritTestRepo("tenant", "repo", "http://ghe.example.com", false),
			globalRepo:    inheritTestRepo("pipelines-as-code", "global", "https://ghe.example.com", true),
			wantNamespace: "tenant",
			wantErr:       "must not be sent to an endpoint chosen by another namespace",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespace, inherited, err := ResolveInheritedSecret(tt.repo, tt.globalRepo)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Equal(t, inherited, false)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, namespace, tt.wantNamespace)
			assert.Equal(t, inherited, tt.wantInherited)
		})
	}
}

func TestSameProviderEndpoint(t *testing.T) {
	tests := []struct {
		name   string
		local  string
		global string
		want   bool
	}{
		{
			name:   "identical https urls",
			local:  "https://ghe.example.com",
			global: "https://ghe.example.com",
			want:   true,
		},
		{
			name:   "different hosts",
			local:  "https://attacker.example.com",
			global: "https://ghe.example.com",
			want:   false,
		},
		{
			name:   "http downgrade of an https endpoint",
			local:  "http://ghe.example.com",
			global: "https://ghe.example.com",
			want:   false,
		},
		{
			name:   "trailing slash names the same endpoint",
			local:  "https://ghe.example.com/",
			global: "https://ghe.example.com",
			want:   true,
		},
		{
			name:   "host case does not tell endpoints apart",
			local:  "https://GHE.Example.COM",
			global: "https://ghe.example.com",
			want:   true,
		},
		{
			name:   "unicode lookalike host is not the ascii one",
			local:  "https://ghé.example.com",
			global: "https://ghe.example.com",
			want:   false,
		},
		{
			name:   "idna spelling matches its punycode form",
			local:  "https://ghé.example.com",
			global: "https://xn--gh-cja.example.com",
			want:   true,
		},
		{
			name:   "explicit port is another endpoint",
			local:  "https://ghe.example.com:8443",
			global: "https://ghe.example.com",
			want:   false,
		},
		{
			name:   "github api v3 suffix names the root deployment",
			local:  "https://ghe.example.com/api/v3",
			global: "https://ghe.example.com",
			want:   true,
		},
		{
			name:   "gitlab api v4 suffix names the root deployment",
			local:  "https://gitlab.example.com/api/v4",
			global: "https://gitlab.example.com",
			want:   true,
		},
		{
			name:   "same path prefix on a shared front end",
			local:  "https://front.example.com/gitlab",
			global: "https://front.example.com/gitlab",
			want:   true,
		},
		{
			name:   "another path prefix on the same host is another owner",
			local:  "https://front.example.com/other",
			global: "https://front.example.com/gitlab",
			want:   false,
		},
		{
			name:   "naming no prefix when the credential belongs under one",
			local:  "https://front.example.com",
			global: "https://front.example.com/gitlab",
			want:   false,
		},
		{
			name:   "api suffix under a path prefix keeps the prefix",
			local:  "https://front.example.com/gitlab/api/v4",
			global: "https://front.example.com/gitlab",
			want:   true,
		},
		{
			name:   "bare hostname reads as https",
			local:  "ghe.example.com",
			global: "https://ghe.example.com",
			want:   true,
		},
		{
			name:   "unparsable urls only match themselves",
			local:  "https://user@ghe.example.com",
			global: "https://ghe.example.com",
			want:   false,
		},
		{
			name:   "empty urls are the same endpoint",
			local:  "",
			global: "",
			want:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, sameProviderEndpoint(tt.local, tt.global), tt.want)
		})
	}
}
