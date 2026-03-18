package llm

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	paramclients "github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	tprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	"k8s.io/client-go/kubernetes/fake"
)

func TestAnalyzer_Analyze(t *testing.T) {
	logger, _ := logger.GetLogger()

	// Create fake Kubernetes client
	fakeClient := fake.NewClientset()

	run := &params.Run{
		Clients: paramclients.Clients{
			Kube: fakeClient,
		},
	}

	// Create mock kubeinteraction
	kinteract := &kubeinteraction.Interaction{}

	analyzer := NewAnalyzer(run, kinteract, logger)

	tests := []struct {
		name        string
		request     *AnalyzeRequest
		wantResults int
		wantError   bool
		setupRepo   func() *v1alpha1.Repository
	}{
		{
			name: "no ai analysis config",
			request: &AnalyzeRequest{
				PipelineRun: &tektonv1.PipelineRun{},
				Event:       &info.Event{},
				Repository:  &v1alpha1.Repository{},
				Provider:    &tprovider.TestProviderImp{},
			},
			wantResults: 0,
			wantError:   false,
		},
		{
			name: "ai analysis disabled",
			request: &AnalyzeRequest{
				PipelineRun: &tektonv1.PipelineRun{},
				Event:       &info.Event{},
				Repository: &v1alpha1.Repository{
					Spec: v1alpha1.RepositorySpec{
						Settings: &v1alpha1.Settings{
							AIAnalysis: &v1alpha1.AIAnalysisConfig{
								Enabled: false,
							},
						},
					},
				},
				Provider: &tprovider.TestProviderImp{},
			},
			wantResults: 0,
			wantError:   false,
		},
		{
			name: "invalid config",
			request: &AnalyzeRequest{
				PipelineRun: &tektonv1.PipelineRun{},
				Event:       &info.Event{},
				Repository: &v1alpha1.Repository{
					Spec: v1alpha1.RepositorySpec{
						Settings: &v1alpha1.Settings{
							AIAnalysis: &v1alpha1.AIAnalysisConfig{
								Enabled:  true,
								Provider: "openai",
								// Missing required fields
							},
						},
					},
				},
				Provider: &tprovider.TestProviderImp{},
			},
			wantResults: 0,
			wantError:   true,
		},
		{
			name:      "nil request",
			request:   nil,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			results, err := analyzer.Analyze(ctx, tt.request)

			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
				assert.Equal(t, len(results), tt.wantResults)
			}
		})
	}
}

func TestAnalyzer_ValidateConfig(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	kinteract := &kubeinteraction.Interaction{}
	analyzer := NewAnalyzer(run, kinteract, logger)

	tests := []struct {
		name      string
		config    *v1alpha1.AIAnalysisConfig
		wantError bool
	}{
		{
			name: "valid config",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "test-role",
						Prompt: "test prompt",
						Output: "pr-comment",
					},
				},
			},
			wantError: false,
		},
		{
			name: "missing provider",
			config: &v1alpha1.AIAnalysisConfig{
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "test-role",
						Prompt: "test prompt",
						Output: "pr-comment",
					},
				},
			},
			wantError: true,
		},
		{
			name: "missing token secret ref",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "test-role",
						Prompt: "test prompt",
						Output: "pr-comment",
					},
				},
			},
			wantError: true,
		},
		{
			name: "no roles",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{},
			},
			wantError: true,
		},
		{
			name: "invalid role - missing name",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Prompt: "test prompt",
						Output: "pr-comment",
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid role - missing prompt",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "test-role",
						Output: "pr-comment",
					},
				},
			},
			wantError: true,
		},
		{
			name: "invalid role - invalid output",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "test-role",
						Prompt: "test prompt",
						Output: "invalid-output",
					},
				},
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := analyzer.validateConfig(tt.config)

			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestAnalyzer_ValidateConfig_WithModels(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	kinteract := &kubeinteraction.Interaction{}
	analyzer := NewAnalyzer(run, kinteract, logger)

	tests := []struct {
		name      string
		config    *v1alpha1.AIAnalysisConfig
		wantError bool
	}{
		{
			name: "roles with different models",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "security-role",
						Prompt: "analyze security",
						Model:  "gpt-5",
						Output: "pr-comment",
					},
					{
						Name:   "quick-role",
						Prompt: "quick analysis",
						Model:  "gpt-5-nano",
						Output: "pr-comment",
					},
				},
			},
			wantError: false,
		},
		{
			name: "role with custom model",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "gemini",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "custom-model-role",
						Prompt: "test prompt",
						Model:  "gemini-2.5-pro",
						Output: "pr-comment",
					},
				},
			},
			wantError: false,
		},
		{
			name: "role without model uses default",
			config: &v1alpha1.AIAnalysisConfig{
				Provider: "openai",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "test-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:   "default-model-role",
						Prompt: "test prompt",
						Output: "pr-comment",
					},
				},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := analyzer.validateConfig(tt.config)

			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestGetContextCacheKey(t *testing.T) {
	tests := []struct {
		name     string
		config   *v1alpha1.ContextConfig
		expected string
	}{
		{
			name:     "nil config returns default key",
			config:   nil,
			expected: "default",
		},
		{
			name:     "config without container logs",
			config:   &v1alpha1.ContextConfig{},
			expected: "commit:false-pr:false-error:false-logs:false-0",
		},
		{
			name: "container logs enabled with explicit max lines",
			config: &v1alpha1.ContextConfig{
				CommitContent: true,
				PRContent:     true,
				ErrorContent:  true,
				ContainerLogs: &v1alpha1.ContainerLogsConfig{
					Enabled:  true,
					MaxLines: 25,
				},
			},
			expected: "commit:true-pr:true-error:true-logs:true-25",
		},
		{
			name: "container logs enabled with default max lines",
			config: &v1alpha1.ContextConfig{
				ContainerLogs: &v1alpha1.ContainerLogsConfig{
					Enabled: true,
				},
			},
			expected: "commit:false-pr:false-error:false-logs:true-50",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, getContextCacheKey(tt.config), tt.expected)
		})
	}
}

func TestAnalyzer_ShouldTriggerRoleEvaluations(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	kinteract := &kubeinteraction.Interaction{}
	analyzer := NewAnalyzer(run, kinteract, logger)

	celContext := map[string]any{
		"body": map[string]any{
			"event": map[string]any{
				"event_type": "pull_request",
			},
			"pipelineRun": map[string]any{
				"status": map[string]any{
					"conditions": []map[string]any{
						{
							"reason": "Failed",
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name      string
		role      v1alpha1.AnalysisRole
		want      bool
		wantError bool
	}{
		{
			name: "no expression returns true",
			role: v1alpha1.AnalysisRole{},
			want: true,
		},
		{
			name: "expression evaluates true",
			role: v1alpha1.AnalysisRole{OnCEL: "body.event.event_type == \"pull_request\""},
			want: true,
		},
		{
			name: "expression evaluates false",
			role: v1alpha1.AnalysisRole{OnCEL: "body.event.event_type == \"push\""},
			want: false,
		},
		{
			name:      "invalid expression",
			role:      v1alpha1.AnalysisRole{OnCEL: "body.event.event_type ="},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := analyzer.shouldTriggerRole(tt.role, celContext)

			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
				return
			}

			assert.NilError(t, err)
			assert.Equal(t, got, tt.want)
		})
	}
}

func TestAnalyzer_ShouldTriggerRole(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	kinteract := &kubeinteraction.Interaction{}
	analyzer := NewAnalyzer(run, kinteract, logger)

	tests := []struct {
		name        string
		role        v1alpha1.AnalysisRole
		celContext  map[string]any
		wantTrigger bool
		wantError   bool
	}{
		{
			name: "no cel expression - always trigger",
			role: v1alpha1.AnalysisRole{
				Name: "test-role",
			},
			celContext:  map[string]any{},
			wantTrigger: true,
			wantError:   false,
		},
		{
			name: "simple true expression",
			role: v1alpha1.AnalysisRole{
				Name:  "test-role",
				OnCEL: "true",
			},
			celContext:  map[string]any{},
			wantTrigger: true,
			wantError:   false,
		},
		{
			name: "simple false expression",
			role: v1alpha1.AnalysisRole{
				Name:  "test-role",
				OnCEL: "false",
			},
			celContext:  map[string]any{},
			wantTrigger: false,
			wantError:   false,
		},
		{
			name: "invalid cel expression",
			role: v1alpha1.AnalysisRole{
				Name:  "test-role",
				OnCEL: "invalid syntax (",
			},
			celContext:  map[string]any{},
			wantTrigger: false,
			wantError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shouldTrigger, err := analyzer.shouldTriggerRole(tt.role, tt.celContext)

			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
				assert.Equal(t, shouldTrigger, tt.wantTrigger)
			}
		})
	}
}
