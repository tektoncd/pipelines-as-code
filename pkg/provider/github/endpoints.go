package github

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/hostpolicy"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/vcshost"
)

// APIEndpoint holds the API locations to use for a given GitHub host.
type APIEndpoint struct {
	APIURL         string
	BaseURL        string
	RepositoryHost string
}

func AppTokenTestAPIURL() (string, error) {
	rawURL := strings.TrimSpace(os.Getenv("PAC_GIT_PROVIDER_TOKEN_APIURL"))
	if rawURL == "" {
		return "", nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawPath != "" {
		return "", fmt.Errorf("PAC_GIT_PROVIDER_TOKEN_APIURL must be a loopback HTTP(S) URL")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("PAC_GIT_PROVIDER_TOKEN_APIURL must target a loopback IP address")
	}
	path := strings.TrimSuffix(parsed.Path, "/")
	if path != "" && path != "/api/v3" {
		return "", fmt.Errorf("PAC_GIT_PROVIDER_TOKEN_APIURL must not contain a path other than /api/v3")
	}
	parsed.Path = path
	return strings.TrimSuffix(parsed.String(), "/"), nil
}

// BaseURLForClient returns the base URL a client must be built against.
//
// It is the endpoint base URL, except when the loopback override the unit tests
// use is set: authentication and API then live on two different servers, and
// every caller has to prefer the override the same way.
func (e APIEndpoint) BaseURLForClient() (string, error) {
	testAPIURL, err := AppTokenTestAPIURL()
	if err != nil {
		return "", err
	}
	if testAPIURL != "" {
		return strings.TrimSuffix(testAPIURL, "/api/v3"), nil
	}
	return e.BaseURL, nil
}

// resolveUntrustedAPIEndpoint maps a GitHub hostname to the API endpoints to
// use. It performs NO trust check and is deliberately unexported: every caller
// must feed it a hostname returned by hostpolicy, never a hostname taken from a
// payload, a header or a Repository CR. Passing raw input here reopens the
// credential exfiltration hole this package exists to close.
func resolveUntrustedAPIEndpoint(rawHost string) (APIEndpoint, error) {
	host, err := vcshost.Parse(rawHost)
	if err != nil {
		return APIEndpoint{}, err
	}
	return apiEndpointFor("https", host), nil
}

// apiEndpointFor builds the endpoint of an already validated hostname.
//
// The public instance answers on its own API host and has no base URL, while a
// self hosted one serves the API under /api/v3 of the host itself, so the two
// shapes differ and every caller has to produce the same pair. scheme is only
// honoured for a self hosted instance: github.com is always https.
func apiEndpointFor(scheme, host string) APIEndpoint {
	if vcshost.Canonical(host) == vcshost.PublicGitHub {
		return APIEndpoint{
			APIURL:         keys.PublicGithubAPIURL,
			RepositoryHost: vcshost.PublicGitHub,
		}
	}
	baseURL := strings.ToLower(scheme) + "://" + host
	return APIEndpoint{
		APIURL:         baseURL + "/api/v3",
		BaseURL:        baseURL,
		RepositoryHost: host,
	}
}

// trustedAPIEndpointForHost resolves rawHost through the controller allowlist.
// Use it on paths that carry no provider signature.
func trustedAPIEndpointForHost(ctx context.Context, run *params.Run, rawHost string) (APIEndpoint, error) {
	if rawHost == "" {
		rawHost = vcshost.PublicGitHub
	}
	host, err := hostpolicy.Trusted(ctx, run, rawHost)
	if err != nil {
		return APIEndpoint{}, err
	}
	return resolveUntrustedAPIEndpoint(host)
}

// TrustedAPIEndpointForRepository resolves the endpoint for a Repository CR URL
// through the controller allowlist.
func TrustedAPIEndpointForRepository(ctx context.Context, run *params.Run, repositoryURL string) (APIEndpoint, error) {
	parsedURL, err := vcshost.SplitURL(repositoryURL)
	// SplitURL reads a value with no scheme as https, which is what an
	// administrator typing a hostname means. A repository URL is not typed by
	// hand though, GitHub always writes it in full, so it has to carry its own
	// https scheme and anything else is not a repository URL.
	if err != nil || !strings.Contains(repositoryURL, "://") || parsedURL.Scheme != "https" {
		return APIEndpoint{}, fmt.Errorf("invalid GitHub repository URL")
	}
	return trustedAPIEndpointForHost(ctx, run, parsedURL.Host)
}

// authenticatedAPIEndpoint resolves the endpoint of a request that has already
// been authenticated by GitHub, recording the host when the controller has no
// allowlist configured yet.
func authenticatedAPIEndpoint(ctx context.Context, run *params.Run, rawHost string) (APIEndpoint, error) {
	host, err := hostpolicy.TrustOnFirstUse(ctx, run, rawHost)
	if err != nil {
		return APIEndpoint{}, err
	}
	return resolveUntrustedAPIEndpoint(host)
}

// trustedAPIEndpointForProviderURL resolves the endpoint for a provider URL that
// came from a Repository CR through the controller allowlist.
//
// The value can be a bare hostname, a base URL or an API URL, since that is what
// administrators have always been able to put in spec.git_provider.url, so only
// its host is validated and the endpoint is rebuilt from the hostname the
// allowlist approved rather than from the string that was supplied.
//
// The scheme is carried over rather than forced to https: a self hosted instance
// reachable over plain http has always been supported here, and refusing it
// would break those installs without making the credential any harder to
// redirect, which is what the allowlist is for.
func trustedAPIEndpointForProviderURL(ctx context.Context, run *params.Run, rawURL string) (APIEndpoint, error) {
	if strings.TrimSpace(rawURL) == "" {
		return trustedAPIEndpointForHost(ctx, run, vcshost.PublicGitHub)
	}
	parsed, err := vcshost.SplitURL(rawURL)
	if err != nil {
		if errors.Is(err, vcshost.ErrURLUnsafeComponents) {
			return APIEndpoint{}, fmt.Errorf("provider URL must not contain credentials, a query or a fragment")
		}
		return APIEndpoint{}, fmt.Errorf("invalid provider URL %q", rawURL)
	}
	if !strings.EqualFold(parsed.Scheme, "https") && !strings.EqualFold(parsed.Scheme, "http") {
		return APIEndpoint{}, fmt.Errorf("invalid provider URL %q: unsupported scheme %q", rawURL, parsed.Scheme)
	}

	host, err := hostpolicy.Trusted(ctx, run, parsed.Host)
	if err != nil {
		return APIEndpoint{}, err
	}
	return apiEndpointFor(parsed.Scheme, host), nil
}
