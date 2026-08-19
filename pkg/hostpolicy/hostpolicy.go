// Package hostpolicy decides which hosted VCS instances a controller is allowed
// to send credentials to.
//
// Pipelines-as-Code derives the API host it talks to from data that originates,
// directly or indirectly, from a webhook payload. Without a check, whoever can
// reach the controller endpoint could make it send its Git provider credentials
// to a host they control. This package is the single gate that prevents it.
//
// The policy lives in the controller ConfigMap and works in two states:
//
//   - Administrator configured: the list is authoritative for every host, public
//     ones included. The controller never writes to the ConfigMap, and an
//     administrator can express "this controller must never talk to a public
//     SaaS instance". Administrators own the
//     settings.TrustedProviderHostnamesKey value exclusively.
//   - Controller managed: the controller has no configured policy. The public
//     SaaS hostnames stay trusted, any other host is refused, and each request
//     the provider itself authenticated appends its host to the
//     keys.AutoTrustedProviderHostnames annotation (trust on first use), so that
//     a default install needs no configuration and a controller serving several
//     instances learns all of them.
//
// A non-empty administrator allowlist selects administrator configured mode. An
// empty allowlist selects controller managed mode. Keeping the two lists under
// separate keys gives each value a single owner and avoids inferring ownership
// by comparing their contents.
//
// Trust on first use appends rather than replaces, so a controller serving
// several instances, or several providers, learns all of them instead of being
// pinned to whichever one happened to send the first webhook.
//
// The policy only covers hosts the controller did not choose. A caller that
// picked the host itself, from its own configuration rather than from a payload
// or a Repository CR, installs its client through
// github.Provider.UsePreauthenticatedClient and never reaches this package: the
// end to end harness is the one caller that does.
package hostpolicy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/vcshost"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// Trusted validates rawHost against the allowlist and returns its normalised
// hostname.
//
// It never writes, so it is the function to use on every path that the provider
// has not authenticated, and on every path authenticated by a secret a tenant
// controls, such as a per Repository webhook secret: letting those record a host
// would hand any namespace user a write primitive on the controller wide policy.
func Trusted(ctx context.Context, run *params.Run, rawHost string) (string, error) {
	host, err := vcshost.Parse(rawHost)
	if err != nil {
		return "", err
	}

	namespace := info.GetNS(ctx)
	configMapName := ControllerConfigMap(run)
	policy, err := readPolicy(ctx, run.Clients.Kube, namespace, configMapName)
	if err != nil {
		return "", err
	}
	if err := policy.check(host, namespace, configMapName); err != nil {
		return "", err
	}
	return vcshost.Canonical(host), nil
}

// TrustOnFirstUse validates rawHost against the policy and, while the policy is
// controller managed, appends rawHost to the controller-owned annotation.
//
// Only call it once the provider itself has authenticated the request with a
// credential the controller owns, for instance after verifying a webhook
// signature against the GitHub App webhook secret: rawHost joins the policy from
// that point on. Anything else must use Trusted.
func TrustOnFirstUse(ctx context.Context, run *params.Run, rawHost string) (string, error) {
	host, err := vcshost.Parse(rawHost)
	if err != nil {
		return "", err
	}
	canonical := vcshost.Canonical(host)

	namespace := info.GetNS(ctx)
	configMapName := ControllerConfigMap(run)
	if run.Clients.Kube == nil {
		return "", noKubeClientError(namespace, configMapName)
	}
	configMaps := run.Clients.Kube.CoreV1().ConfigMaps(namespace)

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		configMap, err := configMaps.Get(ctx, configMapName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		policy, err := policyFrom(configMap, namespace, configMapName)
		if err != nil {
			return err
		}

		// A non-empty administrator list is authoritative. The learned hosts are
		// kept separately and ignored until the administrator empties the list
		// and deliberately hands the policy back to the controller.
		if policy.administratorConfigured() {
			return policy.check(host, namespace, configMapName)
		}

		// A public instance is trusted for as long as the policy is controller
		// managed, so recording it would buy nothing and would make every stock
		// install fight its GitOps reconciler over a value it does not need.
		if vcshost.IsPublic(host) || slices.Contains(policy.autoTrusted, canonical) {
			return nil
		}

		// A payload must never be able to point the controller at an address
		// that is only reachable from inside the cluster or the cloud instance,
		// such as the metadata endpoint. An administrator may still list one
		// explicitly, which is how a private instance is trusted on purpose.
		if vcshost.IsPrivate(host) {
			return NotTrustedError(namespace, configMapName, host, reasonPrivate)
		}

		if configMap.Annotations == nil {
			configMap.Annotations = map[string]string{}
		}
		autoTrusted := append(slices.Clone(policy.autoTrusted), canonical)
		configMap.Annotations[keys.AutoTrustedProviderHostnames] = vcshost.Join(autoTrusted)
		if _, err := configMaps.Update(ctx, configMap, metav1.UpdateOptions{}); err != nil {
			return err
		}
		logPolicyChange(run, namespace, configMapName, canonical, autoTrusted)
		return nil
	})
	if err != nil {
		return "", err
	}
	return canonical, nil
}

// ControllerConfigMap returns the ConfigMap holding this controller policy. Each
// controller has its own, so a second controller can never widen the policy of
// the first one.
func ControllerConfigMap(run *params.Run) string {
	if run.Info.Controller != nil && run.Info.Controller.Configmap != "" {
		return run.Info.Controller.Configmap
	}
	return info.DefaultPipelinesAscodeConfigmapName
}

// logPolicyChange makes a trust on first use write visible: it changes a
// security policy, so an administrator must be able to find out when, and to
// what, without diffing the ConfigMap.
func logPolicyChange(run *params.Run, namespace, configMapName, host string, autoTrusted []string) {
	if run.Clients.Log == nil {
		return
	}
	run.Clients.Log.Warnf(
		"trusted provider hostnames: learnt %q from an authenticated request and added it to the %q annotation of the %s/%s ConfigMap, "+
			"which now trusts %s on top of the public instances. Set the %q key explicitly to take the policy over.",
		host, keys.AutoTrustedProviderHostnames, namespace, configMapName, vcshost.Join(autoTrusted), settings.TrustedProviderHostnamesKey,
	)
}

// policy is the trust policy of one controller, as stored in its ConfigMap.
type policy struct {
	// allowlist is the administrator configured list of trusted hostnames.
	allowlist []string
	// autoTrusted is the list of hostnames the controller recorded itself.
	autoTrusted []string
}

// administratorConfigured reports whether the policy belongs to an
// administrator rather than to the controller.
//
// Any non-empty administrator list is exhaustive. Learned hosts live under a
// different key and therefore never make administrator intent ambiguous.
func (p policy) administratorConfigured() bool {
	return len(p.allowlist) > 0
}

// check applies the policy to host.
//
// An administrator configured list is exhaustive: a public SaaS instance that is
// not on it is refused, which is how "this controller must never talk to
// github.com" is expressed. While the policy is still controller managed the
// public instances stay trusted on top of whatever was learnt, so that a
// controller serving several providers keeps working.
func (p policy) check(host, namespace, configMapName string) error {
	if p.administratorConfigured() {
		if !vcshost.Allowed(p.allowlist, host) {
			return NotTrustedError(namespace, configMapName, host, reasonNotListed)
		}
		return nil
	}
	if vcshost.IsPublic(host) || vcshost.Allowed(p.autoTrusted, host) {
		return nil
	}
	return NotTrustedError(namespace, configMapName, host, reasonUnconfigured)
}

func readPolicy(ctx context.Context, kube kubernetes.Interface, namespace, configMapName string) (policy, error) {
	// Without a Kubernetes client the policy cannot be read, and a policy that
	// cannot be read must never be assumed to be permissive.
	if kube == nil {
		return policy{}, noKubeClientError(namespace, configMapName)
	}
	configMap, err := kube.CoreV1().ConfigMaps(namespace).Get(ctx, configMapName, metav1.GetOptions{})
	if err != nil {
		return policy{}, err
	}
	return policyFrom(configMap, namespace, configMapName)
}

func policyFrom(configMap *corev1.ConfigMap, namespace, configMapName string) (policy, error) {
	allowlist, err := vcshost.ParseAllowlist(configMap.Data[settings.TrustedProviderHostnamesKey])
	if err != nil {
		return policy{}, InvalidAllowlistError(namespace, configMapName, err)
	}
	// A corrupted annotation must never widen the policy: forget the learned
	// hosts and retain only the public defaults until a valid authenticated
	// request records them again.
	autoTrusted, err := vcshost.ParseAllowlist(configMap.Annotations[keys.AutoTrustedProviderHostnames])
	if err != nil {
		autoTrusted = nil
	}
	return policy{allowlist: allowlist, autoTrusted: autoTrusted}, nil
}

// Reasons a host was refused, used to build an error an administrator can act on.
const (
	reasonNotListed = iota
	reasonUnconfigured
	reasonPrivate
)

// NotTrustedError explains, with a ready to run command, how to trust a host.
func NotTrustedError(namespace, configMapName, host string, reason int) error {
	var why string
	switch reason {
	case reasonUnconfigured:
		why = fmt.Sprintf("it is not a public instance and no authenticated request has made it known yet, "+
			"so it must be listed in the %q key of the %s/%s ConfigMap",
			settings.TrustedProviderHostnamesKey, namespace, configMapName)
	case reasonPrivate:
		why = fmt.Sprintf("it is not routable on the public internet, so it cannot be trusted automatically "+
			"and must be listed in the %q key of the %s/%s ConfigMap",
			settings.TrustedProviderHostnamesKey, namespace, configMapName)
	default:
		why = fmt.Sprintf("it is not listed in the %q key of the %s/%s ConfigMap",
			settings.TrustedProviderHostnamesKey, namespace, configMapName)
	}
	return fmt.Errorf(
		"refusing to use credentials with the %q host: %s. "+
			"Add it with: kubectl -n %s patch configmap %s --type merge -p '{\"data\":{\"%s\":\"%s\"}}'",
		host, why, namespace, configMapName, settings.TrustedProviderHostnamesKey, host,
	)
}

// noKubeClientError reports that the policy cannot be read at all, which is
// never allowed to read as permissive.
func noKubeClientError(namespace, configMapName string) error {
	return fmt.Errorf(
		"cannot read the %q key of the %s/%s ConfigMap: no Kubernetes client is configured",
		settings.TrustedProviderHostnamesKey, namespace, configMapName,
	)
}

// InvalidAllowlistError is returned when the configured allowlist cannot be parsed.
func InvalidAllowlistError(namespace, configMapName string, err error) error {
	return fmt.Errorf("the %q key of the %s/%s ConfigMap is invalid: %w",
		settings.TrustedProviderHostnamesKey, namespace, configMapName, err)
}

// TrustedURL validates the host component of rawURL against the allowlist and
// returns the URL rebuilt on the canonical hostname, keeping the path and query.
//
// GitHub derives its API endpoint from a bare hostname, but GitLab, Gitea and
// Bitbucket Data Center are routinely served under a path prefix
// (https://example.com/gitlab), which rebuilding a URL from the hostname alone
// would silently drop. Those providers keep their own URL and use this function
// as their gate.
//
// The returned URL always carries the hostname the allowlist actually approved,
// never the one the caller passed in: the two can differ, and a caller dialling
// the raw value would defeat the check. See the vcshost package documentation.
func TrustedURL(ctx context.Context, run *params.Run, rawURL string) (string, error) {
	parsed, err := vcshost.SplitURL(rawURL)
	if err != nil {
		if errors.Is(err, vcshost.ErrURLUnsafeComponents) {
			return "", fmt.Errorf("provider URL must not contain credentials, a query or a fragment")
		}
		return "", fmt.Errorf("invalid provider URL")
	}
	if !strings.EqualFold(parsed.Scheme, "https") {
		return "", fmt.Errorf("provider URL scheme must be https, got %q", parsed.Scheme)
	}
	host, err := Trusted(ctx, run, parsed.Host)
	if err != nil {
		return "", err
	}
	parsed.Scheme = "https"
	parsed.Host = host
	return parsed.String(), nil
}
