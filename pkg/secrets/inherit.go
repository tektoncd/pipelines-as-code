package secrets

import (
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/vcshost"
)

// ResolveInheritedSecret decides which namespace holds the git_provider secret
// of repo, and refuses the one combination that is not safe.
//
// A Repository that omits git_provider.secret inherits the one of the global
// Repository, which lives in the controller namespace and is shared by every
// tenant. If that Repository is also allowed to choose git_provider.url, then
// whoever can create a Repository in any namespace can have an administrator's
// token sent to a host, or over a transport, of their choosing. Inheriting the
// credential and overriding the endpoint it is sent to are only safe apart.
//
// Every path that resolves a provider secret must go through here: the adapter
// and the watcher each used to carry their own copy of this decision, and a
// guard that only one of them applies is not a guard.
func ResolveInheritedSecret(repo, globalRepo *v1alpha1.Repository) (string, bool, error) {
	namespace := repo.GetNamespace()
	if globalRepo == nil || repo.Spec.GitProvider == nil || repo.Spec.GitProvider.Secret != nil ||
		globalRepo.Spec.GitProvider == nil || globalRepo.Spec.GitProvider.Secret == nil {
		return namespace, false, nil
	}
	if err := checkInheritedEndpoint(repo, globalRepo); err != nil {
		return namespace, false, err
	}
	return globalRepo.GetNamespace(), true, nil
}

func checkInheritedEndpoint(repo, globalRepo *v1alpha1.Repository) error {
	localURL := repo.Spec.GitProvider.URL
	if localURL == "" {
		return nil
	}
	globalURL := globalRepo.Spec.GitProvider.URL
	if sameProviderEndpoint(localURL, globalURL) {
		return nil
	}
	return fmt.Errorf(
		"repository %s/%s sets git_provider.url to %q but inherits its git_provider.secret from the global repository %s/%s: "+
			"a credential owned by the %s namespace must not be sent to an endpoint chosen by another namespace. "+
			"Set git_provider.secret on the repository, or drop its git_provider.url",
		repo.GetNamespace(), repo.GetName(), localURL,
		globalRepo.GetNamespace(), globalRepo.GetName(), globalRepo.GetNamespace(),
	)
}

// sameProviderEndpoint compares two provider URLs by the endpoint they actually
// reach: the transport, the host, and the path prefix.
//
// Downgrading someone else's credential to cleartext hands it to anyone on the
// path just as effectively as redirecting it would, and GitLab, Gitea and
// Bitbucket Data Center are routinely served under a path prefix on a shared
// front end, where another path on the same host is another owner.
func sameProviderEndpoint(local, global string) bool {
	localEP := splitProviderURL(local)
	globalEP := splitProviderURL(global)
	if localEP.scheme != globalEP.scheme || localEP.host != globalEP.host {
		return false
	}
	// The global endpoint carries the prefix the credential belongs to. Naming no
	// prefix is only the same endpoint when the global one has none either:
	// otherwise the token would go to the root of a front end that serves the
	// deployment under a path, which may well belong to someone else.
	return localEP.path == globalEP.path
}

type providerEndpoint struct {
	scheme string
	host   string
	path   string
}

func splitProviderURL(raw string) providerEndpoint {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return providerEndpoint{}
	}
	parsed, err := vcshost.SplitURL(raw)
	if err != nil {
		// Unparsable: fall back to comparing the raw strings, which can only
		// ever be stricter than comparing endpoints.
		return providerEndpoint{host: raw}
	}
	host, err := vcshost.Parse(parsed.Host)
	if err != nil {
		return providerEndpoint{host: raw}
	}
	// /api/v3 and /api/v4 are the API entry point of a provider served at the
	// root, not a prefix that tells two deployments apart.
	path := strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), "/api/v3"), "/api/v4")
	return providerEndpoint{
		scheme: strings.ToLower(parsed.Scheme),
		host:   vcshost.Canonical(host),
		path:   path,
	}
}
