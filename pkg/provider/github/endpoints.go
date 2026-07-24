package github

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

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

func ResolveAPIEndpoint(rawHost string) (APIEndpoint, error) {
	host, err := parseGitHubHost(rawHost)
	if err != nil {
		return APIEndpoint{}, err
	}

	switch strings.ToLower(host) {
	case "api.github.com", "github.com":
		return APIEndpoint{
			APIURL:         keys.PublicGithubAPIURL,
			RepositoryHost: "github.com",
		}, nil
	default:
		baseURL := "https://" + host
		return APIEndpoint{
			APIURL:         baseURL + "/api/v3",
			BaseURL:        baseURL,
			RepositoryHost: host,
		}, nil
	}
}

func parseGitHubHost(rawURL string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("GitHub host is empty")
	}
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		rawURL = "https://" + rawURL
	}
	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Host == "" {
		return "", fmt.Errorf("invalid GitHub host")
	}
	if parsedURL.Scheme != "https" {
		return "", fmt.Errorf("GitHub host scheme must be https")
	}
	if parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return "", fmt.Errorf("invalid GitHub host")
	}
	if path := strings.TrimSuffix(parsedURL.EscapedPath(), "/"); path != "" {
		return "", fmt.Errorf("invalid GitHub host")
	}
	return strings.ToLower(parsedURL.Host), nil
}

func pinGitHubHost(ctx context.Context, run *params.Run, endpoint APIEndpoint) (APIEndpoint, error) {
	namespace := info.GetNS(ctx)
	secretName := run.Info.Controller.Secret
	secretsClient := run.Clients.Kube.CoreV1().Secrets(namespace)
	trustedEndpoint := endpoint

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, err := secretsClient.Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		pinnedHost := strings.TrimSpace(string(secret.Data[keys.GithubHost]))
		if pinnedHost != "" {
			pinnedEndpoint, err := ResolveAPIEndpoint(pinnedHost)
			if err != nil {
				return fmt.Errorf("controller secret %s/%s has an invalid %s value: %w", namespace, secretName, keys.GithubHost, err)
			}
			if pinnedEndpoint.RepositoryHost != endpoint.RepositoryHost {
				return fmt.Errorf("authenticated GitHub host %q conflicts with controller-pinned host %q", endpoint.RepositoryHost, pinnedEndpoint.RepositoryHost)
			}
			trustedEndpoint = pinnedEndpoint
			return nil
		}

		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data[keys.GithubHost] = []byte(endpoint.RepositoryHost)
		if _, err := secretsClient.Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
			return err
		}
		trustedEndpoint = endpoint
		return nil
	})
	if err != nil {
		return APIEndpoint{}, fmt.Errorf("failed to pin authenticated GitHub host: %w", err)
	}
	return trustedEndpoint, nil
}

func TrustedAPIEndpointForRepository(ctx context.Context, run *params.Run, repositoryURL string) (APIEndpoint, error) {
	parsedURL, err := url.Parse(repositoryURL)
	if err != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.User != nil {
		return APIEndpoint{}, fmt.Errorf("invalid GitHub repository URL")
	}
	repositoryEndpoint, err := ResolveAPIEndpoint(parsedURL.Host)
	if err != nil {
		return APIEndpoint{}, err
	}
	return trustedAPIEndpoint(ctx, run.Clients.Kube, info.GetNS(ctx), run.Info.Controller.Secret, repositoryEndpoint)
}

func trustedAPIEndpoint(ctx context.Context, kube kubernetes.Interface, namespace, secretName string, requestedEndpoint APIEndpoint) (APIEndpoint, error) {
	secret, err := kube.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return APIEndpoint{}, err
	}
	pinnedHost := strings.TrimSpace(string(secret.Data[keys.GithubHost]))
	if pinnedHost == "" {
		if requestedEndpoint.RepositoryHost == "github.com" {
			return requestedEndpoint, nil
		}
		return APIEndpoint{}, fmt.Errorf("GitHub Enterprise host %q has not been authenticated yet; deliver a signed GitHub webhook before using GitHub App credentials", requestedEndpoint.RepositoryHost)
	}
	pinnedEndpoint, err := ResolveAPIEndpoint(pinnedHost)
	if err != nil {
		return APIEndpoint{}, fmt.Errorf("controller secret %s/%s has an invalid %s value: %w", namespace, secretName, keys.GithubHost, err)
	}
	if pinnedEndpoint.RepositoryHost != requestedEndpoint.RepositoryHost {
		return APIEndpoint{}, fmt.Errorf("requested GitHub host %q does not match controller-pinned GitHub host %q", requestedEndpoint.RepositoryHost, pinnedEndpoint.RepositoryHost)
	}
	return pinnedEndpoint, nil
}

func trustedAPIEndpointForHost(ctx context.Context, kube kubernetes.Interface, namespace, secretName, rawHost string) (APIEndpoint, error) {
	if rawHost == "" {
		rawHost = "github.com"
	}
	requestedEndpoint, err := ResolveAPIEndpoint(rawHost)
	if err != nil {
		return APIEndpoint{}, err
	}
	return trustedAPIEndpoint(ctx, kube, namespace, secretName, requestedEndpoint)
}
