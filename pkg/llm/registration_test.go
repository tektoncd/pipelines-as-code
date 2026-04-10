package llm_test

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	"gotest.tools/v3/assert"

	// These blank imports exercise the real init() registration path.
	_ "github.com/openshift-pipelines/pipelines-as-code/pkg/llm/providers/gemini"
	_ "github.com/openshift-pipelines/pipelines-as-code/pkg/llm/providers/openai"
)

func TestProviderRegistration(t *testing.T) {
	providers := llm.SupportedProviders()

	var hasOpenAI, hasGemini bool
	for _, p := range providers {
		switch p {
		case llm.ProviderOpenAI:
			hasOpenAI = true
		case llm.ProviderGemini:
			hasGemini = true
		}
	}

	assert.Assert(t, hasOpenAI, "OpenAI provider should be registered via init()")
	assert.Assert(t, hasGemini, "Gemini provider should be registered via init()")
}

func TestNewClientWithRealProviders(t *testing.T) {
	ktesthelper := &kitesthelper.KinterfaceTest{
		GetSecretResult: map[string]string{
			"test-secret": "test-api-key",
		},
	}

	tests := []struct {
		name         string
		provider     llm.AIProvider
		wantProvider string
	}{
		{
			name:         "real openai registration",
			provider:     llm.ProviderOpenAI,
			wantProvider: "openai",
		},
		{
			name:         "real gemini registration",
			provider:     llm.ProviderGemini,
			wantProvider: "gemini",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := llm.NewClient(
				context.Background(),
				tt.provider,
				&v1alpha1.Secret{Name: "test-secret", Key: "token"},
				"default",
				ktesthelper,
				"", "", 30, 1000,
			)

			assert.NilError(t, err)
			assert.Assert(t, client != nil)
			assert.Equal(t, client.GetProviderName(), tt.wantProvider)
		})
	}
}

func TestNewClientValidation(t *testing.T) {
	ktesthelper := &kitesthelper.KinterfaceTest{
		GetSecretResult: map[string]string{
			"test-secret": "test-api-key",
		},
	}

	tests := []struct {
		name      string
		provider  llm.AIProvider
		secretRef *v1alpha1.Secret
		apiURL    string
		timeout   int
		maxTokens int
		wantErr   string
	}{
		{
			name:      "malformed api_url is rejected",
			provider:  llm.ProviderOpenAI,
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			apiURL:    "ftp://bad-scheme.example.com",
			wantErr:   "invalid api_url",
		},
		{
			name:      "negative timeout is rejected",
			provider:  llm.ProviderOpenAI,
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			timeout:   -5,
			wantErr:   "timeout seconds must be non-negative",
		},
		{
			name:      "negative max tokens is rejected",
			provider:  llm.ProviderOpenAI,
			secretRef: &v1alpha1.Secret{Name: "test-secret", Key: "token"},
			maxTokens: -100,
			wantErr:   "max tokens must be non-negative",
		},
		{
			name:      "nil secret ref is rejected",
			provider:  llm.ProviderOpenAI,
			secretRef: nil,
			wantErr:   "token secret reference is required",
		},
		{
			name:      "empty secret name is rejected",
			provider:  llm.ProviderOpenAI,
			secretRef: &v1alpha1.Secret{Name: "", Key: "token"},
			wantErr:   "token secret name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := llm.NewClient(
				context.Background(),
				tt.provider,
				tt.secretRef,
				"default",
				ktesthelper,
				tt.apiURL, "", tt.timeout, tt.maxTokens,
			)

			assert.Assert(t, err != nil, "expected error but got none")
			assert.Assert(t, client == nil)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}
