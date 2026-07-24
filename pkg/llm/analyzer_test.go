package llm

import (
	"context"
	"fmt"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	paramclients "github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	tprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	"k8s.io/client-go/kubernetes/fake"
	"knative.dev/pkg/apis"
)

type nilResponseClient struct{}

func (n *nilResponseClient) Analyze(_ context.Context, _ *AnalysisRequest) (*AnalysisResponse, error) {
	return nil, nil
}

func (n *nilResponseClient) GetProviderName() string {
	return string(ProviderOpenAI)
}

func (n *nilResponseClient) ValidateConfig() error {
	return nil
}

func TestAnalyze(t *testing.T) {
	testLogger, _ := logger.GetLogger()

	fakeClient := fake.NewClientset()
	run := &params.Run{
		Clients: paramclients.Clients{
			Kube: fakeClient,
		},
	}
	kinteract := &kubeinteraction.Interaction{}

	tests := []struct {
		name        string
		repo        *v1alpha1.Repository
		wantResults int
		wantError   bool
	}{
		{
			name:        "no ai analysis config",
			repo:        &v1alpha1.Repository{},
			wantResults: 0,
			wantError:   false,
		},
		{
			name: "ai analysis disabled",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						AIAnalysis: &v1alpha1.AIAnalysisConfig{
							Enabled: false,
						},
					},
				},
			},
			wantResults: 0,
			wantError:   false,
		},
		{
			name: "invalid config",
			repo: &v1alpha1.Repository{
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
			wantResults: 0,
			wantError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			results, err := analyze(ctx, run, kinteract, testLogger,
				tt.repo, &tektonv1.PipelineRun{}, &info.Event{}, &tprovider.TestProviderImp{})

			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
				assert.Equal(t, len(results), tt.wantResults)
			}
		})
	}
}

func TestAnalyzeNilResponse(t *testing.T) {
	originalFactory := registry[ProviderOpenAI]
	registry[ProviderOpenAI] = func(_ *ProviderConfig) (Client, error) {
		return &nilResponseClient{}, nil
	}
	t.Cleanup(func() {
		registry[ProviderOpenAI] = originalFactory
	})

	testLogger, _ := logger.GetLogger()
	kinteract := &kitesthelper.KinterfaceTest{
		GetSecretResult: map[string]string{"llm-token": "token"},
	}
	repo := &v1alpha1.Repository{
		Spec: v1alpha1.RepositorySpec{
			Settings: &v1alpha1.Settings{
				AIAnalysis: &v1alpha1.AIAnalysisConfig{
					Enabled:  true,
					Provider: string(ProviderOpenAI),
					TokenSecretRef: &v1alpha1.Secret{
						Name: "llm-token",
					},
					Roles: []v1alpha1.AnalysisRole{
						{
							Name:   "review",
							Prompt: "review this run",
							OnCEL:  "true",
						},
					},
				},
			},
		},
	}

	results, err := analyze(context.Background(), &params.Run{}, kinteract, testLogger,
		repo, &tektonv1.PipelineRun{}, &info.Event{}, &tprovider.TestProviderImp{})
	assert.NilError(t, err)
	assert.Equal(t, len(results), 1)
	assert.ErrorContains(t, results[0].Error, "LLM client returned no response")
}

func TestExecuteAnalysis(t *testing.T) {
	testLogger, _ := logger.GetLogger()

	fakeClient := fake.NewClientset()
	run := &params.Run{
		Clients: paramclients.Clients{
			Kube: fakeClient,
		},
	}
	kinteract := &kubeinteraction.Interaction{}
	pr := &tektonv1.PipelineRun{}

	tests := []struct {
		name           string
		repo           *v1alpha1.Repository
		nilPipelineRun bool
		wantErr        string
	}{
		{
			name: "no settings",
			repo: &v1alpha1.Repository{},
		},
		{
			name: "ai analysis nil",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{},
				},
			},
		},
		{
			name: "ai analysis disabled",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						AIAnalysis: &v1alpha1.AIAnalysisConfig{Enabled: false},
					},
				},
			},
		},
		{
			name: "invalid config returns error wrapped",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						AIAnalysis: &v1alpha1.AIAnalysisConfig{
							Enabled:  true,
							Provider: "openai",
						},
					},
				},
			},
			wantErr: "LLM analysis failed",
		},
		{
			name: "nil pipelinerun",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						AIAnalysis: &v1alpha1.AIAnalysisConfig{Enabled: true},
					},
				},
			},
			nilPipelineRun: true,
			wantErr:        "no pipelinerun provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipelineRun := pr
			if tt.nilPipelineRun {
				pipelineRun = nil
			}
			err := ExecuteAnalysis(context.Background(), run, kinteract, testLogger,
				tt.repo, pipelineRun, &info.Event{}, &tprovider.TestProviderImp{})
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
		})
	}
}

func TestPostPRComment(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	prov := &tprovider.TestProviderImp{}

	tests := []struct {
		name  string
		event *info.Event
	}{
		{
			name:  "no pull request number, skipped",
			event: &info.Event{PullRequestNumber: 0},
		},
		{
			name:  "with pull request number",
			event: &info.Event{PullRequestNumber: 42},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := AnalysisResult{
				Role:     "test-role",
				Response: &AnalysisResponse{Content: "analysis content"},
			}
			err := postPRComment(context.Background(), result, tt.event, prov, testLogger)
			assert.NilError(t, err)
		})
	}
}

func TestCountResults(t *testing.T) {
	results := []AnalysisResult{
		{Role: "a", Response: &AnalysisResponse{}},
		{Role: "b", Error: fmt.Errorf("failed")},
		{Role: "c", Response: &AnalysisResponse{}},
		{Role: "d", Error: fmt.Errorf("failed again")},
	}

	assert.Equal(t, countSuccessfulResults(results), 2)
	assert.Equal(t, countFailedResults(results), 2)
}

func TestAnalysisErrorMessage(t *testing.T) {
	err := &AnalysisError{
		Provider: "openai",
		Type:     "timeout",
		Message:  "request timed out",
	}
	assert.Equal(t, err.Error(), "request timed out")
}

func TestValidateAnalysisConfig(t *testing.T) {
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
			err := validateAnalysisConfig(tt.config)

			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
			}
		})
	}
}

func TestValidateAnalysisConfigWithModels(t *testing.T) {
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
			err := validateAnalysisConfig(tt.config)

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

func TestShouldTriggerRoleEvaluations(t *testing.T) {
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

	failedPR := &tektonv1.PipelineRun{}
	failedPR.Status.Conditions = append(failedPR.Status.Conditions, apis.Condition{Type: apis.ConditionSucceeded, Status: "False"})

	tests := []struct {
		name      string
		role      v1alpha1.AnalysisRole
		want      bool
		wantError bool
	}{
		{
			name: "no expression defaults to completed pipelines",
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
			got, err := shouldTriggerRole(tt.role, celContext, failedPR)

			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
				return
			}

			assert.NilError(t, err)
			assert.Equal(t, got, tt.want)
		})
	}
}

func TestShouldTriggerRole(t *testing.T) {
	failedPR := &tektonv1.PipelineRun{}
	failedPR.Status.Conditions = append(failedPR.Status.Conditions, apis.Condition{Type: apis.ConditionSucceeded, Status: "False"})

	succeededPR := &tektonv1.PipelineRun{}
	succeededPR.Status.Conditions = append(succeededPR.Status.Conditions, apis.Condition{Type: apis.ConditionSucceeded, Status: "True"})

	pendingPR := &tektonv1.PipelineRun{}
	pendingPR.Status.Conditions = append(pendingPR.Status.Conditions, apis.Condition{Type: apis.ConditionSucceeded, Status: "Unknown"})

	tests := []struct {
		name        string
		role        v1alpha1.AnalysisRole
		celContext  map[string]any
		pr          *tektonv1.PipelineRun
		wantTrigger bool
		wantError   bool
	}{
		{
			name:        "no cel expression - triggers for failed pipeline",
			role:        v1alpha1.AnalysisRole{Name: "test-role"},
			celContext:  map[string]any{},
			pr:          failedPR,
			wantTrigger: true,
		},
		{
			name:        "no cel expression - triggers for succeeded pipeline",
			role:        v1alpha1.AnalysisRole{Name: "test-role"},
			celContext:  map[string]any{},
			pr:          succeededPR,
			wantTrigger: true,
		},
		{
			name:        "no cel expression - skips pending pipeline",
			role:        v1alpha1.AnalysisRole{Name: "test-role"},
			celContext:  map[string]any{},
			pr:          pendingPR,
			wantTrigger: false,
		},
		{
			name:        "no cel expression - skips nil pipelinerun",
			role:        v1alpha1.AnalysisRole{Name: "test-role"},
			celContext:  map[string]any{},
			pr:          nil,
			wantTrigger: false,
		},
		{
			name:        "no cel expression - skips pipelinerun without status",
			role:        v1alpha1.AnalysisRole{Name: "test-role"},
			celContext:  nil,
			pr:          &tektonv1.PipelineRun{},
			wantTrigger: false,
		},
		{
			name:        "simple true expression",
			role:        v1alpha1.AnalysisRole{Name: "test-role", OnCEL: "true"},
			celContext:  map[string]any{},
			pr:          succeededPR,
			wantTrigger: true,
		},
		{
			name:        "simple false expression",
			role:        v1alpha1.AnalysisRole{Name: "test-role", OnCEL: "false"},
			celContext:  map[string]any{},
			pr:          failedPR,
			wantTrigger: false,
		},
		{
			name:       "invalid cel expression",
			role:       v1alpha1.AnalysisRole{Name: "test-role", OnCEL: "invalid syntax ("},
			celContext: map[string]any{},
			pr:         failedPR,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trigger, err := shouldTriggerRole(tt.role, tt.celContext, tt.pr)

			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
			} else {
				assert.NilError(t, err)
				assert.Equal(t, trigger, tt.wantTrigger)
			}
		})
	}
}
