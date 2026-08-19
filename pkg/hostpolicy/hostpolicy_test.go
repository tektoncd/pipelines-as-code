package hostpolicy

import (
	"errors"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"
	rtesting "knative.dev/pkg/reconciler/testing"
)

const (
	testNamespace     = "pipelines-as-code"
	testConfigMapName = "pipelines-as-code"
)

// newRun returns a Run backed by a fake client holding a controller ConfigMap
// with the given allowlist. When withConfigMap is false no ConfigMap is seeded.
func newRun(t *testing.T, allowlist string, withConfigMap bool) (*params.Run, *testclient.Clients) {
	t.Helper()
	ctx, _ := rtesting.SetupFakeContext(t)
	data := testclient.Data{}
	if withConfigMap {
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testConfigMapName,
				Namespace: testNamespace,
			},
			Data: map[string]string{},
		}
		if allowlist != "" {
			configMap.Data[settings.TrustedProviderHostnamesKey] = allowlist
		}
		data.ConfigMap = []*corev1.ConfigMap{configMap}
	}
	seedData, _ := testclient.SeedTestData(t, ctx, data)
	run := &params.Run{
		Clients: clients.Clients{Kube: seedData.Kube},
		Info: info.Info{
			Controller: &info.ControllerInfo{Configmap: testConfigMapName},
		},
	}
	return run, &seedData
}

func TestTrusted(t *testing.T) {
	tests := []struct {
		name        string
		allowlist   string
		autoTrusted string
		host        string
		want        string
		wantErrSub  string
	}{
		{
			name: "public github is trusted without any configuration",
			host: "github.com",
			want: "github.com",
		},
		{
			name: "api.github.com folds into github.com",
			host: "api.github.com",
			want: "github.com",
		},
		{
			name: "public gitlab is trusted without any configuration",
			host: "gitlab.com",
			want: "gitlab.com",
		},
		{
			name:       "a configured allowlist is authoritative for the public instances too",
			allowlist:  "ghe.example.com",
			host:       "github.com",
			wantErrSub: "is not listed in",
		},
		{
			name:      "a public instance listed explicitly stays trusted",
			allowlist: "ghe.example.com,github.com",
			host:      "github.com",
			want:      "github.com",
		},
		{
			name:       "self hosted host is refused when the allowlist is empty",
			host:       "ghe.example.com",
			wantErrSub: "no authenticated request has made it known yet",
		},
		{
			name:        "controller learned self hosted host is trusted",
			autoTrusted: "ghe.example.com",
			host:        "ghe.example.com",
			want:        "ghe.example.com",
		},
		{
			name:      "listed self hosted host is trusted",
			allowlist: "ghe.example.com",
			host:      "ghe.example.com",
			want:      "ghe.example.com",
		},
		{
			name:      "one of several listed hosts is trusted",
			allowlist: "ghe.example.com,gitlab.example.com",
			host:      "gitlab.example.com",
			want:      "gitlab.example.com",
		},
		{
			name:       "unlisted host is refused",
			allowlist:  "ghe.example.com",
			host:       "attacker.example.com",
			wantErrSub: "is not listed in",
		},
		{
			name:       "lookalike host is refused",
			allowlist:  "ghe.example.com",
			host:       "ghe.example.com.evil.example",
			wantErrSub: "is not listed in",
		},
		{
			name:       "invalid host is refused",
			host:       "http://ghe.example.com",
			wantErrSub: "scheme must be https",
		},
		{
			name:       "invalid allowlist is reported",
			allowlist:  "https://ghe.example.com/owner",
			host:       "ghe.example.com",
			wantErrSub: "is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, testNamespace)
			run, _ := newRunWithPolicy(t, tt.allowlist, tt.autoTrusted)

			got, err := Trusted(ctx, run, tt.host)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tt.want)
		})
	}
}

// Trusted must never write: it is used on paths carrying no provider signature.
func TestTrustedNeverWrites(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, testNamespace)
	run, seedData := newRun(t, "", true)

	_, err := Trusted(ctx, run, "ghe.example.com")
	assert.ErrorContains(t, err, "refusing to use credentials")

	configMap, err := seedData.Kube.CoreV1().ConfigMaps(testNamespace).Get(ctx, testConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, configMap.Data[settings.TrustedProviderHostnamesKey], "",
		"an unauthenticated request must never populate the allowlist")
	_, recorded := configMap.Annotations[keys.AutoTrustedProviderHostnames]
	assert.Assert(t, !recorded, "an unauthenticated request must never record a learned host")
}

func TestTrustedConfigMapGetError(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, testNamespace)
	run, _ := newRun(t, "", false)

	_, err := Trusted(ctx, run, "ghe.example.com")
	assert.ErrorContains(t, err, `configmaps "pipelines-as-code" not found`)
}

func TestTrustOnFirstUse(t *testing.T) {
	tests := []struct {
		name            string
		allowlist       string
		host            string
		want            string
		wantAllowlist   string
		wantAutoTrusted string
		wantErrSub      string
	}{
		{
			name:            "first authenticated host is recorded",
			host:            "ghe.example.com",
			want:            "ghe.example.com",
			wantAutoTrusted: "ghe.example.com",
		},
		{
			name:          "administrator listed host is accepted without being learned",
			allowlist:     "ghe.example.com",
			host:          "ghe.example.com",
			want:          "ghe.example.com",
			wantAllowlist: "ghe.example.com",
		},
		{
			name:            "host is normalized before being recorded",
			host:            "https://GHE.Example.COM",
			want:            "ghe.example.com",
			wantAutoTrusted: "ghe.example.com",
		},
		{
			name:          "a configured allowlist is authoritative for the public instances too",
			allowlist:     "ghe.example.com",
			host:          "github.com",
			wantAllowlist: "ghe.example.com",
			wantErrSub:    "is not listed in",
		},
		{
			name:          "public github stays trusted without being recorded",
			host:          "github.com",
			want:          "github.com",
			wantAllowlist: "",
		},
		{
			name:          "a private address is never recorded automatically",
			host:          "gitea.gitea.svc",
			wantAllowlist: "",
			wantErrSub:    "not routable on the public internet",
		},
		{
			name:          "a private address listed explicitly is trusted",
			allowlist:     "gitea.gitea.svc",
			host:          "gitea.gitea.svc",
			want:          "gitea.gitea.svc",
			wantAllowlist: "gitea.gitea.svc",
		},
		{
			name:          "a configured allowlist refuses an unlisted host",
			allowlist:     "ghe.example.com",
			host:          "attacker.example.com",
			wantAllowlist: "ghe.example.com",
			wantErrSub:    "is not listed in",
		},
		{
			name:          "invalid allowlist is reported and left untouched",
			allowlist:     "https://ghe.example.com/owner",
			host:          "ghe.example.com",
			wantAllowlist: "https://ghe.example.com/owner",
			wantErrSub:    "is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, testNamespace)
			run, seedData := newRun(t, tt.allowlist, true)

			got, err := TrustOnFirstUse(ctx, run, tt.host)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, got, tt.want)
			}

			configMap, err := seedData.Kube.CoreV1().ConfigMaps(testNamespace).Get(ctx, testConfigMapName, metav1.GetOptions{})
			assert.NilError(t, err)
			assert.Equal(t, configMap.Data[settings.TrustedProviderHostnamesKey], tt.wantAllowlist)
			assert.Equal(t, configMap.Annotations[keys.AutoTrustedProviderHostnames], tt.wantAutoTrusted)
		})
	}
}

// An administrator configured allowlist is authoritative: the controller must
// not write to the ConfigMap at all, so that GitOps tooling sees no drift.
func TestTrustOnFirstUseDoesNotWriteWhenConfigured(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, testNamespace)
	run, seedData := newRun(t, "ghe.example.com", true)

	updated := false
	seedData.Kube.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		updated = true
		return false, nil, nil
	})

	got, err := TrustOnFirstUse(ctx, run, "ghe.example.com")
	assert.NilError(t, err)
	assert.Equal(t, got, "ghe.example.com")
	assert.Assert(t, !updated, "the ConfigMap must not be written when the allowlist is configured")
}

func TestTrustOnFirstUseRetriesConflict(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, testNamespace)
	run, seedData := newRun(t, "", true)

	conflicts := 1
	seedData.Kube.PrependReactor("update", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		if conflicts > 0 {
			conflicts--
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Resource: "configmaps"}, testConfigMapName, errors.New("conflict"),
			)
		}
		return false, nil, nil
	})

	got, err := TrustOnFirstUse(ctx, run, "ghe.example.com")
	assert.NilError(t, err)
	assert.Equal(t, got, "ghe.example.com")

	configMap, err := seedData.Kube.CoreV1().ConfigMaps(testNamespace).Get(ctx, testConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, configMap.Data[settings.TrustedProviderHostnamesKey], "")
	assert.Equal(t, configMap.Annotations[keys.AutoTrustedProviderHostnames], "ghe.example.com")
}

// An administrator may have configured a list while a controller request was
// starting. The configured value wins and the request is refused.
func TestTrustOnFirstUseRejectsConcurrentDifferentHost(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, testNamespace)
	run, seedData := newRun(t, "", true)

	raced := false
	seedData.Kube.PrependReactor("get", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		if raced {
			return false, nil, nil
		}
		raced = true
		return true, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: testConfigMapName, Namespace: testNamespace},
			Data: map[string]string{
				settings.TrustedProviderHostnamesKey: "other.example.com",
			},
		}, nil
	})

	_, err := TrustOnFirstUse(ctx, run, "ghe.example.com")
	assert.ErrorContains(t, err, "is not listed in")
}

func TestTrustOnFirstUseInvalidHost(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, testNamespace)
	run, _ := newRun(t, "", true)

	_, err := TrustOnFirstUse(ctx, run, "http://ghe.example.com")
	assert.ErrorContains(t, err, "scheme must be https")
}

// The controller must always read the ConfigMap named in its own
// ControllerInfo, so that a second controller keeps its own allowlist.
func TestTrustedUsesControllerConfigMap(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, testNamespace)
	seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		ConfigMap: []*corev1.ConfigMap{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pipelines-as-code", Namespace: testNamespace},
				Data:       map[string]string{settings.TrustedProviderHostnamesKey: "first.example.com"},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "second-configmap", Namespace: testNamespace},
				Data:       map[string]string{settings.TrustedProviderHostnamesKey: "second.example.com"},
			},
		},
	})
	run := &params.Run{
		Clients: clients.Clients{Kube: seedData.Kube},
		Info: info.Info{
			Controller: &info.ControllerInfo{Configmap: "second-configmap"},
		},
	}

	got, err := Trusted(ctx, run, "second.example.com")
	assert.NilError(t, err)
	assert.Equal(t, got, "second.example.com")

	_, err = Trusted(ctx, run, "first.example.com")
	assert.ErrorContains(t, err, "is not listed in")
}

func TestControllerConfigMap(t *testing.T) {
	tests := []struct {
		name       string
		controller *info.ControllerInfo
		want       string
	}{
		{
			name:       "nil controller falls back to default",
			controller: nil,
			want:       info.DefaultPipelinesAscodeConfigmapName,
		},
		{
			name:       "empty configmap field falls back to default",
			controller: &info.ControllerInfo{Configmap: ""},
			want:       info.DefaultPipelinesAscodeConfigmapName,
		},
		{
			name:       "custom configmap is returned",
			controller: &info.ControllerInfo{Configmap: "my-configmap"},
			want:       "my-configmap",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := &params.Run{Info: info.Info{Controller: tt.controller}}
			assert.Equal(t, ControllerConfigMap(run), tt.want)
		})
	}
}

// The error must tell the administrator exactly how to fix the situation.
func TestNotTrustedErrorIsActionable(t *testing.T) {
	err := NotTrustedError(testNamespace, testConfigMapName, "ghe.example.com", reasonUnconfigured)
	assert.ErrorContains(t, err, "ghe.example.com")
	assert.ErrorContains(t, err, settings.TrustedProviderHostnamesKey)
	assert.ErrorContains(t, err, "kubectl -n pipelines-as-code patch configmap pipelines-as-code")
}

// newRunWithPolicy seeds a controller ConfigMap whose administrator list and
// controller-learned annotation are set independently.
func newRunWithPolicy(t *testing.T, allowlist, autoTrusted string) (*params.Run, *testclient.Clients) {
	t.Helper()
	ctx, _ := rtesting.SetupFakeContext(t)
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testConfigMapName,
			Namespace:   testNamespace,
			Annotations: map[string]string{},
		},
		Data: map[string]string{},
	}
	if allowlist != "" {
		configMap.Data[settings.TrustedProviderHostnamesKey] = allowlist
	}
	if autoTrusted != "" {
		configMap.Annotations[keys.AutoTrustedProviderHostnames] = autoTrusted
	}
	seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{ConfigMap: []*corev1.ConfigMap{configMap}})
	run := &params.Run{
		Clients: clients.Clients{Kube: seedData.Kube},
		Info:    info.Info{Controller: &info.ControllerInfo{Configmap: testConfigMapName}},
	}
	return run, &seedData
}

// A controller serving several instances, or several providers, must learn all
// of them instead of being pinned to whichever one sent the first webhook.
func TestTrustOnFirstUseAppends(t *testing.T) {
	tests := []struct {
		name            string
		allowlist       string
		autoTrusted     string
		host            string
		wantAllowlist   string
		wantAutoTrusted string
		wantErrSub      string
	}{
		{
			name:            "a second self hosted instance is appended, not substituted",
			autoTrusted:     "ghe.example.com",
			host:            "gitlab.example.com",
			wantAutoTrusted: "ghe.example.com,gitlab.example.com",
		},
		{
			name:            "a public instance stays trusted alongside a learnt one",
			autoTrusted:     "ghe.example.com",
			host:            "github.com",
			wantAutoTrusted: "ghe.example.com",
		},
		{
			name:            "an administrator configured list is never appended to",
			allowlist:       "ghe.example.com",
			host:            "gitlab.example.com",
			wantAllowlist:   "ghe.example.com",
			wantAutoTrusted: "",
			wantErrSub:      "is not listed in",
		},
		{
			name:            "a configured list is authoritative even when it matches a learned host",
			allowlist:       "ghe.example.com",
			autoTrusted:     "ghe.example.com",
			host:            "gitlab.example.com",
			wantAllowlist:   "ghe.example.com",
			wantAutoTrusted: "ghe.example.com",
			wantErrSub:      "is not listed in",
		},
		{
			name:            "a corrupted learned annotation is replaced safely",
			autoTrusted:     "not a hostname!!",
			host:            "gitlab.example.com",
			wantAutoTrusted: "gitlab.example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, testNamespace)
			run, seedData := newRunWithPolicy(t, tt.allowlist, tt.autoTrusted)

			_, err := TrustOnFirstUse(ctx, run, tt.host)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
			} else {
				assert.NilError(t, err)
			}

			configMap, err := seedData.Kube.CoreV1().ConfigMaps(testNamespace).Get(ctx, testConfigMapName, metav1.GetOptions{})
			assert.NilError(t, err)
			assert.Equal(t, configMap.Data[settings.TrustedProviderHostnamesKey], tt.wantAllowlist)
			assert.Equal(t, configMap.Annotations[keys.AutoTrustedProviderHostnames], tt.wantAutoTrusted)
		})
	}
}

// TrustedURL must hand back a URL built on the hostname the allowlist approved,
// never the one it was given: the two can differ, and a caller dialling the raw
// value would defeat the check entirely.
func TestTrustedURL(t *testing.T) {
	tests := []struct {
		name       string
		allowlist  string
		rawURL     string
		want       string
		wantErrSub string
	}{
		{
			name:      "a path prefix is preserved",
			allowlist: "git.example.com",
			rawURL:    "https://git.example.com/gitlab",
			want:      "https://git.example.com/gitlab",
		},
		{
			name:      "the host is replaced by the canonical one",
			allowlist: "",
			rawURL:    "https://API.GitHub.com/foo",
			want:      "https://github.com/foo",
		},
		{
			// strings.ToLower folds the Turkish dotted capital I onto an ASCII
			// i, but a resolver reads it as xn--github-qyd.com. The policy must
			// see, and refuse, the name that would really be dialled.
			name:       "a unicode lookalike of github.com is refused",
			rawURL:     "https://GİTHUB.com/owner/repo",
			wantErrSub: "refusing to use credentials",
		},
		{
			name:       "cleartext is refused",
			allowlist:  "git.example.com",
			rawURL:     "http://git.example.com/gitlab",
			wantErrSub: "scheme must be https",
		},
		{
			name:       "userinfo redirection is refused",
			allowlist:  "git.example.com",
			rawURL:     "https://git.example.com@attacker.example/gitlab",
			wantErrSub: "must not contain credentials",
		},
		{
			name:       "an untrusted host is refused",
			allowlist:  "git.example.com",
			rawURL:     "https://attacker.example/gitlab",
			wantErrSub: "refusing to use credentials",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, testNamespace)
			run, _ := newRun(t, tt.allowlist, true)

			got, err := TrustedURL(ctx, run, tt.rawURL)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tt.want)
		})
	}
}

// Emptying the administrator list deliberately returns to managed mode. Hosts
// learned before the administrator took over remain available in that mode.
func TestEmptyingConfiguredListRestoresManagedPolicy(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, testNamespace)
	run, seedData := newRunWithPolicy(t, "corp.example.com", "ghe.example.com")

	_, err := Trusted(ctx, run, "ghe.example.com")
	assert.ErrorContains(t, err, "is not listed in")

	configMaps := seedData.Kube.CoreV1().ConfigMaps(testNamespace)
	configMap, err := configMaps.Get(ctx, testConfigMapName, metav1.GetOptions{})
	assert.NilError(t, err)
	delete(configMap.Data, settings.TrustedProviderHostnamesKey)
	_, err = configMaps.Update(ctx, configMap, metav1.UpdateOptions{})
	assert.NilError(t, err)

	_, err = Trusted(ctx, run, "ghe.example.com")
	assert.NilError(t, err)
	_, err = Trusted(ctx, run, "github.com")
	assert.NilError(t, err)
}
