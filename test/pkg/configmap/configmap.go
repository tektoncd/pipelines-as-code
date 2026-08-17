package configmap

import (
	"context"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/vcshost"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	typedv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/util/retry"
)

// update applies mutate to the ConfigMap under a conflict retry.
//
// Every helper here reads, mutates and updates rather than replacing the object
// wholesale: the controller writes to this ConfigMap on its own when it learns a
// provider hostname, and neither that value nor the labels and annotations it
// carries may be lost by a test tuning an unrelated key. mutate runs again on
// each retry, so it must derive everything it writes from the object it is
// given.
func update(ctx context.Context, t *testing.T, configMaps typedv1.ConfigMapInterface, configMapName string, mutate func(*corev1.ConfigMap)) {
	t.Helper()
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := configMaps.Get(ctx, configMapName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if current.Data == nil {
			current.Data = map[string]string{}
		}
		if current.Annotations == nil {
			current.Annotations = map[string]string{}
		}
		mutate(current)
		_, err = configMaps.Update(ctx, current, metav1.UpdateOptions{})
		return err
	})
	assert.NilError(t, err)
}

func configMapsFor(ctx context.Context, runcnx *params.Run) typedv1.ConfigMapInterface {
	return runcnx.Clients.Kube.CoreV1().ConfigMaps(info.GetNS(ctx))
}

// ChangeGlobalConfig patches keys into the controller ConfigMap and returns a
// function restoring the previous values.
func ChangeGlobalConfig(ctx context.Context, t *testing.T, runcnx *params.Run, configMapName string, data map[string]string) func() {
	t.Helper()
	configMaps := configMapsFor(ctx, runcnx)

	original := map[string]string{}
	update(ctx, t, configMaps, configMapName, func(current *corev1.ConfigMap) {
		clear(original)
		for key := range data {
			original[key] = current.Data[key]
		}
		maps.Copy(current.Data, data)
	})

	return func() {
		update(ctx, t, configMaps, configMapName, func(current *corev1.ConfigMap) {
			maps.Copy(current.Data, original)
		})
	}
}

// TrustProviderHostname adds the hostname of the provider under test to the
// controller allowlist, so that an incoming webhook, which carries no provider
// signature and therefore cannot be trusted on first use, reaches it. It returns
// a function removing that hostname again.
//
// Only the hostname it added is removed on restore; writing the whole key back
// would otherwise delete another administrator entry added during the test.
func TrustProviderHostname(ctx context.Context, t *testing.T, runcnx *params.Run, configMapName, providerURL string) func() {
	t.Helper()
	host := providerHost(t, providerURL)
	configMaps := configMapsFor(ctx, runcnx)

	added := false
	update(ctx, t, configMaps, configMapName, func(current *corev1.ConfigMap) {
		hosts := splitHosts(current.Data[settings.TrustedProviderHostnamesKey])
		if added = !slices.Contains(hosts, host); added {
			current.Data[settings.TrustedProviderHostnamesKey] = strings.Join(append(hosts, host), ",")
		}
	})

	if !added {
		return func() {}
	}

	return func() {
		update(ctx, t, configMaps, configMapName, func(current *corev1.ConfigMap) {
			hosts := slices.DeleteFunc(splitHosts(current.Data[settings.TrustedProviderHostnamesKey]),
				func(entry string) bool { return entry == host })
			setOrDelete(current.Data, settings.TrustedProviderHostnamesKey, strings.Join(hosts, ","), len(hosts) > 0)
		})
	}
}

// ProviderHost extracts the hostname of a provider URL the way the controller
// stores it in the allowlist, so that a test comparing what the controller wrote
// against the instance it talks to compares equal spellings.
func ProviderHost(t *testing.T, providerURL string) string {
	t.Helper()
	return providerHost(t, providerURL)
}

// TrustedProviderHostnames returns the administrator allowlist together with the
// hostnames the controller recorded itself.
func TrustedProviderHostnames(ctx context.Context, t *testing.T, runcnx *params.Run, configMapName string) (allowlist, autoTrusted []string) {
	t.Helper()
	current, err := configMapsFor(ctx, runcnx).Get(ctx, configMapName, metav1.GetOptions{})
	assert.NilError(t, err)
	return splitHosts(current.Data[settings.TrustedProviderHostnamesKey]),
		splitHosts(current.Annotations[keys.AutoTrustedProviderHostnames])
}

// ResetTrustedProviderHostnames hands the policy back to the controller and
// forgets what it learned, which is the state a fresh install starts from. It
// returns a function putting both values back as they were.
//
// A test that wants to watch the controller learn a hostname has to start from
// here: a configured list is authoritative, while an existing learned entry
// would make the controller skip the write.
func ResetTrustedProviderHostnames(ctx context.Context, t *testing.T, runcnx *params.Run, configMapName string) func() {
	t.Helper()
	configMaps := configMapsFor(ctx, runcnx)

	var originalData, originalAnnotation string
	var hadData, hadAnnotation bool
	update(ctx, t, configMaps, configMapName, func(current *corev1.ConfigMap) {
		originalData, hadData = current.Data[settings.TrustedProviderHostnamesKey]
		originalAnnotation, hadAnnotation = current.Annotations[keys.AutoTrustedProviderHostnames]
		delete(current.Data, settings.TrustedProviderHostnamesKey)
		delete(current.Annotations, keys.AutoTrustedProviderHostnames)
	})

	return func() {
		update(ctx, t, configMaps, configMapName, func(current *corev1.ConfigMap) {
			setOrDelete(current.Data, settings.TrustedProviderHostnamesKey, originalData, hadData)
			setOrDelete(current.Annotations, keys.AutoTrustedProviderHostnames, originalAnnotation, hadAnnotation)
		})
	}
}

// setOrDelete restores a key to the value it had, which for a key that was not
// there in the first place means removing it rather than leaving an empty value
// behind: an empty allowlist and an unset one read the same to the controller,
// but an empty annotation would not survive a round trip unchanged.
func setOrDelete(target map[string]string, key, value string, keep bool) {
	if keep {
		target[key] = value
		return
	}
	delete(target, key)
}

// providerHost extracts the hostname of a provider URL the way the controller
// stores it in the allowlist, so that adding and removing an entry compares
// equal to what the controller itself would have written.
func providerHost(t *testing.T, providerURL string) string {
	t.Helper()
	parsed, err := vcshost.SplitURL(providerURL)
	assert.NilError(t, err)
	host, err := vcshost.Parse(parsed.Host)
	assert.NilError(t, err)
	return vcshost.Canonical(host)
}

// splitHosts parses the comma separated allowlist, dropping the empty entries an
// unset or trailing separator leaves behind.
//
// It does not go through vcshost.ParseAllowlist on purpose: a restore path has
// to put back whatever it found, and an entry the controller cannot parse must
// not make a test fail in its teardown.
func splitHosts(raw string) []string {
	var hosts []string
	for entry := range strings.SplitSeq(raw, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			hosts = append(hosts, entry)
		}
	}
	return hosts
}
