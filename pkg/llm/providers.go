package llm

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets/types"
)

// ProviderConfig is the unified configuration passed to every provider constructor.
type ProviderConfig struct {
	APIKey         string
	BaseURL        string
	Model          string
	TimeoutSeconds int
	MaxTokens      int
}

// NewClientFunc is the constructor signature every provider must implement.
type NewClientFunc func(cfg *ProviderConfig) (Client, error)

// registry holds registered provider constructors, populated by provider init() functions.
var registry = map[AIProvider]NewClientFunc{}

// RegisterProvider registers a provider constructor. Called by provider init() functions.
func RegisterProvider(name AIProvider, fn NewClientFunc) {
	registry[name] = fn
}

// NewClient creates an LLM client by validating configuration, fetching the API
// token from a K8s secret, and delegating to the registered provider constructor.
func NewClient(ctx context.Context, provider AIProvider, secretRef *v1alpha1.Secret,
	namespace string, kinteract kubeinteraction.Interface,
	apiURL, model string, timeoutSeconds, maxTokens int,
) (Client, error) {
	if err := ValidateClientConfig(provider, secretRef, apiURL, timeoutSeconds, maxTokens); err != nil {
		return nil, fmt.Errorf("invalid client configuration: %w", err)
	}

	fn := registry[provider]

	token, err := getTokenFromSecret(ctx, secretRef, namespace, kinteract)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve LLM token: %w", err)
	}

	if timeoutSeconds == 0 {
		timeoutSeconds = DefaultTimeoutSeconds
	}
	if maxTokens == 0 {
		maxTokens = DefaultMaxTokens
	}

	return fn(&ProviderConfig{
		APIKey:         token,
		BaseURL:        apiURL,
		Model:          model,
		TimeoutSeconds: timeoutSeconds,
		MaxTokens:      maxTokens,
	})
}

// ValidateClientConfig validates the client configuration before creating a client.
func ValidateClientConfig(provider AIProvider, secretRef *v1alpha1.Secret, apiURL string, timeoutSeconds, maxTokens int) error {
	if provider == "" {
		return fmt.Errorf("LLM provider is required")
	}

	if _, ok := registry[provider]; !ok {
		return fmt.Errorf("unsupported LLM provider: %s", provider)
	}

	if secretRef == nil {
		return fmt.Errorf("token secret reference is required")
	}
	if secretRef.Name == "" {
		return fmt.Errorf("token secret name is required")
	}

	if apiURL != "" {
		if err := ValidateURL(apiURL); err != nil {
			return fmt.Errorf("invalid api_url: %w", err)
		}
	}

	if timeoutSeconds < 0 {
		return fmt.Errorf("timeout seconds must be non-negative")
	}

	if maxTokens < 0 {
		return fmt.Errorf("max tokens must be non-negative")
	}

	return nil
}

// SupportedProviders returns the names of all registered providers.
func SupportedProviders() []AIProvider {
	keys := make([]AIProvider, 0, len(registry))
	for k := range registry {
		keys = append(keys, k)
	}
	return keys
}

// getTokenFromSecret retrieves the API token from a Kubernetes secret.
func getTokenFromSecret(ctx context.Context, secretRef *v1alpha1.Secret, namespace string, kinteract kubeinteraction.Interface) (string, error) {
	if secretRef == nil {
		return "", fmt.Errorf("secret reference is nil")
	}

	key := secretRef.Key
	if key == "" {
		key = "token"
	}

	opt := types.GetSecretOpt{
		Namespace: namespace,
		Name:      secretRef.Name,
		Key:       key,
	}

	secretValue, err := kinteract.GetSecret(ctx, opt)
	if err != nil {
		return "", fmt.Errorf("failed to get secret %s/%s: %w", namespace, secretRef.Name, err)
	}

	if secretValue == "" {
		return "", fmt.Errorf("secret %s/%s key %s is empty", namespace, secretRef.Name, key)
	}

	return secretValue, nil
}

// ValidateURL validates that the URL is properly formatted with http or https scheme.
func ValidateURL(urlStr string) error {
	if urlStr == "" {
		return nil
	}

	if strings.ContainsAny(urlStr, " \t\n\r") {
		return fmt.Errorf("URL contains invalid whitespace characters")
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return fmt.Errorf("failed to parse URL '%s': %w", urlStr, err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL scheme must be 'http' or 'https', got '%s'", parsedURL.Scheme)
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("URL must contain a host")
	}

	return nil
}
