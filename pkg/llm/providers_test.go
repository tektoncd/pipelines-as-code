package llm

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	"gotest.tools/v3/assert"
)

func TestMain(m *testing.M) {
	// Register stub providers for tests (since we can't blank-import provider packages
	// from the same package due to import cycles).
	if _, ok := registry[ProviderOpenAI]; !ok {
		RegisterProvider(ProviderOpenAI, func(cfg *ProviderConfig) (Client, error) {
			if cfg == nil {
				return nil, fmt.Errorf("config is required")
			}
			if cfg.APIKey == "" {
				return nil, fmt.Errorf("API key is required")
			}
			return &stubClient{provider: string(ProviderOpenAI)}, nil
		})
	}
	if _, ok := registry[ProviderGemini]; !ok {
		RegisterProvider(ProviderGemini, func(cfg *ProviderConfig) (Client, error) {
			if cfg == nil {
				return nil, fmt.Errorf("config is required")
			}
			if cfg.APIKey == "" {
				return nil, fmt.Errorf("API key is required")
			}
			return &stubClient{provider: string(ProviderGemini)}, nil
		})
	}
	os.Exit(m.Run())
}

type stubClient struct {
	provider string
}

func (s *stubClient) Analyze(_ context.Context, _ *AnalysisRequest) (*AnalysisResponse, error) {
	return &AnalysisResponse{Content: "stub"}, nil
}

func (s *stubClient) GetProviderName() string {
	return s.provider
}

func (s *stubClient) ValidateConfig() error {
	return nil
}

func TestValidateClientConfig(t *testing.T) {
	tests := []struct {
		name      string
		provider  AIProvider
		secretRef *v1alpha1.Secret
		apiURL    string
		timeout   int
		maxTokens int
		wantError bool
	}{
		{
			name:     "valid openai config",
			provider: ProviderOpenAI,
			secretRef: &v1alpha1.Secret{
				Name: "test-secret",
				Key:  "token",
			},
			timeout:   30,
			maxTokens: 1000,
			wantError: false,
		},
		{
			name:     "valid gemini config",
			provider: ProviderGemini,
			secretRef: &v1alpha1.Secret{
				Name: "test-secret",
				Key:  "api_key",
			},
			timeout:   45,
			maxTokens: 2000,
			wantError: false,
		},
		{
			name:      "missing provider",
			provider:  "",
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			wantError: true,
		},
		{
			name:      "missing token secret ref",
			provider:  ProviderOpenAI,
			secretRef: nil,
			wantError: true,
		},
		{
			name:      "invalid provider",
			provider:  "invalid-provider",
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			wantError: true,
		},
		{
			name:      "negative timeout",
			provider:  ProviderOpenAI,
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			timeout:   -1,
			wantError: true,
		},
		{
			name:      "negative max tokens",
			provider:  ProviderOpenAI,
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			maxTokens: -1,
			wantError: true,
		},
		{
			name:     "valid config with custom api_url",
			provider: ProviderOpenAI,
			apiURL:   "https://custom-openai.example.com/v1",
			secretRef: &v1alpha1.Secret{
				Name: "test-secret",
				Key:  "token",
			},
			wantError: false,
		},
		{
			name:     "valid config with http api_url",
			provider: ProviderGemini,
			apiURL:   "http://localhost:8080/v1",
			secretRef: &v1alpha1.Secret{
				Name: "test-secret",
				Key:  "token",
			},
			wantError: false,
		},
		{
			name:      "invalid api_url - wrong scheme",
			provider:  ProviderOpenAI,
			apiURL:    "ftp://example.com",
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			wantError: true,
		},
		{
			name:      "invalid api_url - missing scheme",
			provider:  ProviderOpenAI,
			apiURL:    "example.com/v1",
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			wantError: true,
		},
		{
			name:      "invalid api_url - malformed",
			provider:  ProviderOpenAI,
			apiURL:    "://invalid",
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			wantError: true,
		},
		{
			name:      "zero timeout is valid",
			provider:  ProviderOpenAI,
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			timeout:   0,
			maxTokens: 1000,
			wantError: false,
		},
		{
			name:      "zero max tokens is valid",
			provider:  ProviderOpenAI,
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			timeout:   30,
			maxTokens: 0,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateClientConfig(tt.provider, tt.secretRef, tt.apiURL, tt.timeout, tt.maxTokens)
			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		name         string
		provider     AIProvider
		secretResult map[string]string
		secretRef    *v1alpha1.Secret
		namespace    string
		wantError    bool
	}{
		{
			name:     "create openai client",
			provider: ProviderOpenAI,
			secretResult: map[string]string{
				"test-secret": "test-api",
			},
			secretRef: &v1alpha1.Secret{
				Name: "test-secret",
				Key:  "token",
			},
			namespace: "default",
			wantError: false,
		},
		{
			name:     "create gemini client",
			provider: ProviderGemini,
			secretResult: map[string]string{
				"test-secret": "test-api",
			},
			secretRef: &v1alpha1.Secret{
				Name: "test-secret",
				Key:  "token",
			},
			namespace: "default",
			wantError: false,
		},
		{
			name:     "missing secret",
			provider: ProviderOpenAI,
			secretRef: &v1alpha1.Secret{
				Name: "missing-secret",
				Key:  "token",
			},
			namespace: "default",
			wantError: true,
		},
		{
			name:     "unsupported provider",
			provider: "unsupported",
			secretRef: &v1alpha1.Secret{
				Name: "test-secret",
				Key:  "token",
			},
			namespace: "default",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ktesthelper := &kitesthelper.KinterfaceTest{
				GetSecretResult: tt.secretResult,
			}
			client, err := NewClient(ctx, tt.provider, tt.secretRef, tt.namespace, ktesthelper,
				"", "", 30, 1000)

			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
				assert.Assert(t, client == nil, "expected nil client on error")
			} else {
				assert.NilError(t, err)
				assert.Assert(t, client != nil, "expected non-nil client")

				switch tt.provider {
				case ProviderOpenAI:
					assert.Equal(t, client.GetProviderName(), string(ProviderOpenAI))
				case ProviderGemini:
					assert.Equal(t, client.GetProviderName(), string(ProviderGemini))
				}
			}
		})
	}
}

func TestSupportedProviders(t *testing.T) {
	providers := SupportedProviders()

	assert.Assert(t, len(providers) >= 2, "expected at least 2 supported providers")

	var hasOpenAI, hasGemini bool
	for _, p := range providers {
		switch p {
		case ProviderOpenAI:
			hasOpenAI = true
		case ProviderGemini:
			hasGemini = true
		}
	}

	assert.Assert(t, hasOpenAI, "expected OpenAI to be supported")
	assert.Assert(t, hasGemini, "expected Gemini to be supported")
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "empty URL is valid",
			url:     "",
			wantErr: false,
		},
		{
			name:    "valid HTTPS URL",
			url:     "https://api.example.com",
			wantErr: false,
		},
		{
			name:    "valid HTTP URL",
			url:     "http://api.example.com",
			wantErr: false,
		},
		{
			name:    "valid URL with port",
			url:     "https://api.example.com:8443",
			wantErr: false,
		},
		{
			name:    "valid URL with path",
			url:     "https://api.example.com/v1",
			wantErr: false,
		},
		{
			name:    "invalid URL - no scheme",
			url:     "api.example.com",
			wantErr: true,
		},
		{
			name:    "invalid URL - wrong scheme",
			url:     "ftp://api.example.com",
			wantErr: true,
		},
		{
			name:    "invalid URL - no host",
			url:     "https://",
			wantErr: true,
		},
		{
			name:    "invalid URL - with whitespace",
			url:     "https://api.example.com /path",
			wantErr: true,
		},
		{
			name:    "invalid URL - with tab",
			url:     "https://api.example.com\t/path",
			wantErr: true,
		},
		{
			name:    "invalid URL - with newline",
			url:     "https://api.example.com\n",
			wantErr: true,
		},
		{
			name:    "invalid URL - ws scheme",
			url:     "ws://api.example.com",
			wantErr: true,
		},
		{
			name:    "invalid URL - malformed scheme",
			url:     "ht!tp://api.example.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(tt.url)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.NilError(t, err)
			}
		})
	}
}
