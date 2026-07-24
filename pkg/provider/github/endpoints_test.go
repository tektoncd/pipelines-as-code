package github

import (
	"errors"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakekubeclientset "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestResolveAPIEndpoint(t *testing.T) {
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
			wantErrSub: "invalid GitHub host",
		},
		{
			name:       "insecure scheme",
			rawHost:    "http://github.example.com",
			wantErrSub: "scheme must be https",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveAPIEndpoint(tt.rawHost)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestParseGitHubHost(t *testing.T) {
	tests := []struct {
		name       string
		rawHost    string
		want       string
		wantErrSub string
	}{
		{
			name:       "empty host",
			wantErrSub: "GitHub host is empty",
		},
		{
			name:       "unparsable URL",
			rawHost:    "https://github.example.com/%zz",
			wantErrSub: "invalid GitHub host",
		},
		{
			name:       "missing host",
			rawHost:    "https://",
			wantErrSub: "invalid GitHub host",
		},
		{
			name:       "http scheme",
			rawHost:    "http://github.example.com",
			wantErrSub: "scheme must be https",
		},
		{
			name:       "userinfo is rejected",
			rawHost:    "https://token@github.example.com",
			wantErrSub: "invalid GitHub host",
		},
		{
			name:       "query is rejected",
			rawHost:    "https://github.example.com?token=secret",
			wantErrSub: "invalid GitHub host",
		},
		{
			name:       "fragment is rejected",
			rawHost:    "https://github.example.com#token",
			wantErrSub: "invalid GitHub host",
		},
		{
			name:       "path is rejected",
			rawHost:    "https://github.example.com/owner",
			wantErrSub: "invalid GitHub host",
		},
		{
			name:    "normalizes case",
			rawHost: "https://GitHub.Example.COM/",
			want:    "github.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGitHubHost(tt.rawHost)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tt.want)
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

func TestPinGitHubHost(t *testing.T) {
	const (
		namespace  = "pipelines-as-code"
		secretName = "pipelines-as-code-secret"
	)

	tests := []struct {
		name       string
		pinnedHost string
		eventHost  string
		wantHost   string
		wantErrSub string
	}{
		{
			name:      "first authenticated host is pinned",
			eventHost: "github.example.com",
			wantHost:  "github.example.com",
		},
		{
			name:       "same pinned host is accepted",
			pinnedHost: "github.example.com",
			eventHost:  "github.example.com",
			wantHost:   "github.example.com",
		},
		{
			name:       "different host is rejected",
			pinnedHost: "github.example.com",
			eventHost:  "attacker.example.com",
			wantErrSub: "conflicts with controller-pinned host",
		},
		{
			name:       "invalid pinned host is rejected",
			pinnedHost: "https://github.example.com/owner",
			eventHost:  "github.example.com",
			wantErrSub: "has an invalid github-host value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, namespace)
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				Data: map[string][]byte{},
			}
			if tt.pinnedHost != "" {
				secret.Data[keys.GithubHost] = []byte(tt.pinnedHost)
			}
			seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				Secret: []*corev1.Secret{secret},
			})
			run := &params.Run{
				Clients: clients.Clients{Kube: seedData.Kube},
				Info: info.Info{
					Controller: &info.ControllerInfo{Secret: secretName},
				},
			}
			endpoint, err := ResolveAPIEndpoint(tt.eventHost)
			assert.NilError(t, err)

			got, err := pinGitHubHost(ctx, run, endpoint)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got.RepositoryHost, tt.wantHost)
			updated, err := seedData.Kube.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
			assert.NilError(t, err)
			assert.Equal(t, string(updated.Data[keys.GithubHost]), tt.wantHost)
		})
	}
}

func TestPinGitHubHostSecretGetError(t *testing.T) {
	const (
		namespace  = "pipelines-as-code"
		secretName = "pipelines-as-code-secret"
	)
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, namespace)
	seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	run := &params.Run{
		Clients: clients.Clients{Kube: seedData.Kube},
		Info: info.Info{
			Controller: &info.ControllerInfo{Secret: secretName},
		},
	}
	endpoint, err := ResolveAPIEndpoint("github.example.com")
	assert.NilError(t, err)

	_, err = pinGitHubHost(ctx, run, endpoint)
	assert.ErrorContains(t, err, "failed to pin authenticated GitHub host")
	assert.ErrorContains(t, err, `secrets "pipelines-as-code-secret" not found`)
}

func simulateSecretUpdateConflict(t *testing.T, kube *fakekubeclientset.Clientset, namespace, secretName, pinnedHost, message string, conflicts *int) {
	t.Helper()
	kube.PrependReactor("update", "secrets", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		if *conflicts == 0 {
			*conflicts++
			secret, err := kube.Tracker().Get(corev1.SchemeGroupVersion.WithResource("secrets"), namespace, secretName)
			assert.NilError(t, err)
			concurrentSecret, ok := secret.(*corev1.Secret)
			assert.Assert(t, ok)
			concurrentSecret = concurrentSecret.DeepCopy()
			if concurrentSecret.Data == nil {
				concurrentSecret.Data = map[string][]byte{}
			}
			concurrentSecret.Data[keys.GithubHost] = []byte(pinnedHost)
			assert.NilError(t, kube.Tracker().Update(corev1.SchemeGroupVersion.WithResource("secrets"), concurrentSecret, namespace))
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Resource: "secrets"},
				secretName,
				errors.New(message),
			)
		}
		return false, nil, nil
	})
}

func TestPinGitHubHostRetriesConflict(t *testing.T) {
	const (
		namespace  = "pipelines-as-code"
		secretName = "pipelines-as-code-secret"
	)
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, namespace)
	seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Secret: []*corev1.Secret{{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
		}},
	})
	run := &params.Run{
		Clients: clients.Clients{Kube: seedData.Kube},
		Info: info.Info{
			Controller: &info.ControllerInfo{Secret: secretName},
		},
	}
	conflicts := 0
	simulateSecretUpdateConflict(t, seedData.Kube, namespace, secretName, "github.example.com", "simulated update conflict", &conflicts)
	endpoint, err := ResolveAPIEndpoint("github.example.com")
	assert.NilError(t, err)

	got, err := pinGitHubHost(ctx, run, endpoint)
	assert.NilError(t, err)
	assert.Equal(t, got.RepositoryHost, "github.example.com")
	assert.Equal(t, conflicts, 1)
	updated, err := seedData.Kube.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, string(updated.Data[keys.GithubHost]), "github.example.com")
}

func TestPinGitHubHostRejectsConcurrentDifferentHost(t *testing.T) {
	const (
		namespace  = "pipelines-as-code"
		secretName = "pipelines-as-code-secret"
	)
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, namespace)
	seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Secret: []*corev1.Secret{{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
		}},
	})
	run := &params.Run{
		Clients: clients.Clients{Kube: seedData.Kube},
		Info: info.Info{
			Controller: &info.ControllerInfo{Secret: secretName},
		},
	}
	conflicts := 0
	simulateSecretUpdateConflict(t, seedData.Kube, namespace, secretName, "other.example.com", "simulated concurrent pin", &conflicts)
	endpoint, err := ResolveAPIEndpoint("github.example.com")
	assert.NilError(t, err)

	_, err = pinGitHubHost(ctx, run, endpoint)
	assert.ErrorContains(t, err, `conflicts with controller-pinned host "other.example.com"`)
	assert.Equal(t, conflicts, 1)
	updated, err := seedData.Kube.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, string(updated.Data[keys.GithubHost]), "other.example.com")
}

func TestTrustedAPIEndpointForRepository(t *testing.T) {
	const (
		namespace  = "pipelines-as-code"
		secretName = "pipelines-as-code-secret"
	)
	tests := []struct {
		name          string
		pinnedHost    string
		repositoryURL string
		wantHost      string
		wantErrSub    string
	}{
		{
			name:          "public GitHub works before pinning",
			repositoryURL: "https://github.com/owner/repo",
			wantHost:      "github.com",
		},
		{
			name:          "enterprise requires prior authenticated webhook",
			repositoryURL: "https://github.example.com/owner/repo",
			wantErrSub:    "has not been authenticated yet",
		},
		{
			name:          "matching enterprise pin",
			pinnedHost:    "github.example.com",
			repositoryURL: "https://github.example.com/owner/repo",
			wantHost:      "github.example.com",
		},
		{
			name:          "repository conflicts with pin",
			pinnedHost:    "github.example.com",
			repositoryURL: "https://attacker.example.com/owner/repo",
			wantErrSub:    "requested GitHub host",
		},
		{
			name:          "invalid repository URL",
			repositoryURL: "https://token@github.example.com/owner/repo",
			wantErrSub:    "invalid GitHub repository URL",
		},
		{
			name:          "invalid pinned host is rejected",
			pinnedHost:    "https://github.example.com/owner",
			repositoryURL: "https://github.example.com/owner/repo",
			wantErrSub:    "has an invalid github-host value",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, namespace)
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				Data: map[string][]byte{},
			}
			if tt.pinnedHost != "" {
				secret.Data[keys.GithubHost] = []byte(tt.pinnedHost)
			}
			seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				Secret: []*corev1.Secret{secret},
			})
			run := &params.Run{
				Clients: clients.Clients{Kube: seedData.Kube},
				Info: info.Info{
					Controller: &info.ControllerInfo{Secret: secretName},
				},
			}

			got, err := TrustedAPIEndpointForRepository(ctx, run, tt.repositoryURL)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got.RepositoryHost, tt.wantHost)
		})
	}
}

func TestTrustedAPIEndpointForRepositorySecretGetError(t *testing.T) {
	const (
		namespace  = "pipelines-as-code"
		secretName = "pipelines-as-code-secret"
	)
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, namespace)
	seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	run := &params.Run{
		Clients: clients.Clients{Kube: seedData.Kube},
		Info: info.Info{
			Controller: &info.ControllerInfo{Secret: secretName},
		},
	}

	_, err := TrustedAPIEndpointForRepository(ctx, run, "https://github.com/owner/repo")
	assert.ErrorContains(t, err, `secrets "pipelines-as-code-secret" not found`)
}

func TestTrustedAPIEndpointForHost(t *testing.T) {
	const (
		namespace  = "pipelines-as-code"
		secretName = "pipelines-as-code-secret"
	)
	tests := []struct {
		name       string
		pinnedHost string
		rawHost    string
		wantHost   string
		wantErrSub string
	}{
		{
			name:     "empty host defaults to public GitHub",
			wantHost: "github.com",
		},
		{
			name:       "invalid host is rejected",
			rawHost:    "http://github.example.com",
			wantErrSub: "scheme must be https",
		},
		{
			name:       "matching enterprise pin",
			pinnedHost: "github.example.com",
			rawHost:    "github.example.com",
			wantHost:   "github.example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, namespace)
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				Data: map[string][]byte{},
			}
			if tt.pinnedHost != "" {
				secret.Data[keys.GithubHost] = []byte(tt.pinnedHost)
			}
			seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				Secret: []*corev1.Secret{secret},
			})

			got, err := trustedAPIEndpointForHost(ctx, seedData.Kube, namespace, secretName, tt.rawHost)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got.RepositoryHost, tt.wantHost)
		})
	}
}
