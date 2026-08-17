package github

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestResolveUntrustedAPIEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		rawHost    string
		want       APIEndpoint
		wantErrSub string
	}{
		{
			name:    "public API host",
			rawHost: "api.github.com",
			want: APIEndpoint{
				APIURL:         "https://api.github.com",
				RepositoryHost: "github.com",
			},
		},
		{
			name:    "public repository host",
			rawHost: "https://github.com",
			want: APIEndpoint{
				APIURL:         "https://api.github.com",
				RepositoryHost: "github.com",
			},
		},
		{
			name:    "enterprise host",
			rawHost: "github.example.com",
			want: APIEndpoint{
				APIURL:         "https://github.example.com/api/v3",
				BaseURL:        "https://github.example.com",
				RepositoryHost: "github.example.com",
			},
		},
		{
			name:       "invalid path",
			rawHost:    "https://github.example.com/attacker",
			wantErrSub: "must not contain a path",
		},
		{
			name:       "insecure scheme",
			rawHost:    "http://github.example.com",
			wantErrSub: "scheme must be https",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveUntrustedAPIEndpoint(tt.rawHost)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestAppTokenTestAPIURL(t *testing.T) {
	tests := []struct {
		name       string
		rawURL     string
		want       string
		wantErrSub string
	}{
		{
			name: "unset",
		},
		{
			name:   "loopback test server",
			rawURL: "http://127.0.0.1:1234/api/v3",
			want:   "http://127.0.0.1:1234/api/v3",
		},
		{
			name:       "remote host",
			rawURL:     "https://attacker.example/api/v3",
			wantErrSub: "must target a loopback IP address",
		},
		{
			name:       "unexpected path",
			rawURL:     "http://127.0.0.1:1234/credentials",
			wantErrSub: "must not contain a path other than /api/v3",
		},
		{
			name:       "malformed URL",
			rawURL:     "http://127.0.0.1:1234/%zz",
			wantErrSub: "must be a loopback HTTP(S) URL",
		},
		{
			name:       "userinfo is rejected",
			rawURL:     "http://user@127.0.0.1:1234/api/v3",
			wantErrSub: "must be a loopback HTTP(S) URL",
		},
		{
			name:       "query is rejected",
			rawURL:     "http://127.0.0.1:1234/api/v3?token=secret",
			wantErrSub: "must be a loopback HTTP(S) URL",
		},
		{
			name:       "fragment is rejected",
			rawURL:     "http://127.0.0.1:1234/api/v3#token",
			wantErrSub: "must be a loopback HTTP(S) URL",
		},
		{
			name:       "scheme is rejected",
			rawURL:     "ftp://127.0.0.1:1234/api/v3",
			wantErrSub: "must be a loopback HTTP(S) URL",
		},
		{
			name:   "trailing slash is trimmed",
			rawURL: "http://127.0.0.1:1234/api/v3/",
			want:   "http://127.0.0.1:1234/api/v3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", tt.rawURL)
			got, err := AppTokenTestAPIURL()
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// emptyAllowlistConfigMap returns the controller ConfigMap of a fresh install,
// where no trusted hostname has been recorded yet.
func emptyAllowlistConfigMap() []*corev1.ConfigMap {
	return []*corev1.ConfigMap{{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipelines-as-code",
			Namespace: "pipelinesascode",
		},
	}}
}

// newTestRun returns a Run whose controller ConfigMap holds the given allowlist.
func newTestRun(t *testing.T, allowlist string) *params.Run {
	t.Helper()
	ctx, _ := rtesting.SetupFakeContext(t)
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipelines-as-code",
			Namespace: "pipelines-as-code",
		},
		Data: map[string]string{},
	}
	if allowlist != "" {
		configMap.Data[settings.TrustedProviderHostnamesKey] = allowlist
	}
	seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		ConfigMap: []*corev1.ConfigMap{configMap},
	})
	return &params.Run{
		Clients: clients.Clients{Kube: seedData.Kube},
		Info: info.Info{
			Controller: &info.ControllerInfo{Configmap: "pipelines-as-code"},
		},
	}
}

func TestTrustedAPIEndpointForRepository(t *testing.T) {
	tests := []struct {
		name          string
		allowlist     string
		repositoryURL string
		want          APIEndpoint
		wantErrSub    string
	}{
		{
			name:          "public GitHub needs no allowlist",
			repositoryURL: "https://github.com/owner/repo",
			want: APIEndpoint{
				APIURL:         "https://api.github.com",
				RepositoryHost: "github.com",
			},
		},
		{
			name:          "self hosted needs a trusted host",
			repositoryURL: "https://github.example.com/owner/repo",
			wantErrSub:    "refusing to use credentials",
		},
		{
			name:          "trusted self hosted repository",
			allowlist:     "github.example.com",
			repositoryURL: "https://github.example.com/owner/repo",
			want: APIEndpoint{
				APIURL:         "https://github.example.com/api/v3",
				BaseURL:        "https://github.example.com",
				RepositoryHost: "github.example.com",
			},
		},
		{
			name:          "untrusted repository host is refused",
			allowlist:     "github.example.com",
			repositoryURL: "https://attacker.example.com/owner/repo",
			wantErrSub:    "is not listed in",
		},
		{
			name:          "userinfo is rejected",
			repositoryURL: "https://token@github.example.com/owner/repo",
			wantErrSub:    "invalid GitHub repository URL",
		},
		{
			name:          "query string is rejected",
			repositoryURL: "https://github.com/owner/repo?x=y",
			wantErrSub:    "invalid GitHub repository URL",
		},
		{
			name:          "fragment is rejected",
			repositoryURL: "https://github.com/owner/repo#section",
			wantErrSub:    "invalid GitHub repository URL",
		},
		{
			name:          "http scheme is rejected",
			repositoryURL: "http://github.com/owner/repo",
			wantErrSub:    "invalid GitHub repository URL",
		},
		{
			name:          "invalid allowlist is reported",
			allowlist:     "https://github.example.com/owner",
			repositoryURL: "https://github.example.com/owner/repo",
			wantErrSub:    "is invalid",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, "pipelines-as-code")
			run := newTestRun(t, tt.allowlist)

			got, err := TrustedAPIEndpointForRepository(ctx, run, tt.repositoryURL)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestTrustedAPIEndpointForHost(t *testing.T) {
	tests := []struct {
		name       string
		allowlist  string
		rawHost    string
		wantHost   string
		wantErrSub string
	}{
		{
			name:     "empty host defaults to public GitHub",
			wantHost: "github.com",
		},
		{
			name:       "insecure scheme is rejected",
			rawHost:    "http://github.example.com",
			wantErrSub: "scheme must be https",
		},
		{
			name:      "trusted self hosted host",
			allowlist: "github.example.com",
			rawHost:   "github.example.com",
			wantHost:  "github.example.com",
		},
		{
			name:       "untrusted self hosted host",
			rawHost:    "github.example.com",
			wantErrSub: "refusing to use credentials",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, "pipelines-as-code")
			run := newTestRun(t, tt.allowlist)

			got, err := trustedAPIEndpointForHost(ctx, run, tt.rawHost)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got.RepositoryHost, tt.wantHost)
		})
	}
}

func TestAuthenticatedAPIEndpoint(t *testing.T) {
	tests := []struct {
		name            string
		allowlist       string
		rawHost         string
		wantHost        string
		wantAllowlist   string
		wantAutoTrusted string
		wantErrSub      string
	}{
		{
			name:            "first authenticated self hosted webhook is recorded",
			rawHost:         "github.example.com",
			wantHost:        "github.example.com",
			wantAutoTrusted: "github.example.com",
		},
		{
			name:          "public GitHub stays trusted without being recorded",
			rawHost:       "github.com",
			wantHost:      "github.com",
			wantAllowlist: "",
		},
		{
			name:          "an allowlist without the public host refuses public GitHub",
			allowlist:     "github.example.com",
			rawHost:       "github.com",
			wantAllowlist: "github.example.com",
			wantErrSub:    "is not listed in",
		},
		{
			name:          "a private address is never recorded automatically",
			rawHost:       "gitea.gitea.svc",
			wantAllowlist: "",
			wantErrSub:    "not routable on the public internet",
		},
		{
			name:          "a configured allowlist refuses another host",
			allowlist:     "github.example.com",
			rawHost:       "attacker.example.com",
			wantAllowlist: "github.example.com",
			wantErrSub:    "is not listed in",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, "pipelines-as-code")
			run := newTestRun(t, tt.allowlist)

			got, err := authenticatedAPIEndpoint(ctx, run, tt.rawHost)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, got.RepositoryHost, tt.wantHost)
			}

			configMap, err := run.Clients.Kube.CoreV1().ConfigMaps("pipelines-as-code").Get(
				ctx, "pipelines-as-code", metav1.GetOptions{},
			)
			assert.NilError(t, err)
			assert.Equal(t, configMap.Data[settings.TrustedProviderHostnamesKey], tt.wantAllowlist)
			assert.Equal(t, configMap.Annotations[keys.AutoTrustedProviderHostnames], tt.wantAutoTrusted)
		})
	}
}
