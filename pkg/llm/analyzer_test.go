package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/kubeinteraction"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	paramclients "github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	kitesthelper "github.com/tektoncd/pipelines-as-code/pkg/test/kubernetestint"
	"github.com/tektoncd/pipelines-as-code/pkg/test/logger"
	tprovider "github.com/tektoncd/pipelines-as-code/pkg/test/provider"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

type staticAnalysisClient struct {
	response      *AnalysisResponse
	err           error
	beforeAnalyze func()
	requests      []*AnalysisRequest
}

func (s *staticAnalysisClient) Analyze(_ context.Context, request *AnalysisRequest) (*AnalysisResponse, error) {
	s.requests = append(s.requests, request)
	if s.beforeAnalyze != nil {
		s.beforeAnalyze()
	}
	return s.response, s.err
}

func (s *staticAnalysisClient) GetProviderName() string {
	return string(ProviderOpenAI)
}

func (s *staticAnalysisClient) ValidateConfig() error {
	return nil
}

type recordingCommentProvider struct {
	tprovider.TestProviderImp
	err      error
	calls    int
	comments []string
	markers  []string
}

func (r *recordingCommentProvider) CreateComment(_ context.Context, _ *info.Event, comment, marker string) error {
	r.calls++
	r.comments = append(r.comments, comment)
	r.markers = append(r.markers, marker)
	return r.err
}

func registerOpenAITestClient(t *testing.T, client Client) {
	t.Helper()
	originalFactory, hadOriginal := registry[ProviderOpenAI]
	registry[ProviderOpenAI] = func(_ *ProviderConfig) (Client, error) {
		return client, nil
	}
	t.Cleanup(func() {
		if hadOriginal && originalFactory != nil {
			registry[ProviderOpenAI] = originalFactory
			return
		}
		delete(registry, ProviderOpenAI)
	})
}

func makeAnalysisRepository(roles ...v1alpha1.AnalysisRole) *v1alpha1.Repository {
	return &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-ns",
		},
		Spec: v1alpha1.RepositorySpec{
			Settings: &v1alpha1.Settings{
				AIAnalysis: &v1alpha1.AIAnalysisConfig{
					Enabled:  true,
					Provider: string(ProviderOpenAI),
					TokenSecretRef: &v1alpha1.Secret{
						Name: "llm-token",
					},
					Roles: roles,
				},
			},
		},
	}
}

func makeCompletedPipelineRun() *tektonv1.PipelineRun {
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
		},
	}
	pr.Status.Conditions = append(pr.Status.Conditions, apis.Condition{
		Type:   apis.ConditionSucceeded,
		Status: "False",
		Reason: "Failed",
	})
	return pr
}

func TestAnalyze(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	originalRetryDelay := analysisRetryDelay
	analysisRetryDelay = 0
	t.Cleanup(func() {
		analysisRetryDelay = originalRetryDelay
	})

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
	registerOpenAITestClient(t, &nilResponseClient{})

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

func TestAnalyzeRoleOutcomes(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	pr := makeCompletedPipelineRun()
	event := &info.Event{EventType: "pull_request"}

	tests := []struct {
		name          string
		roles         []v1alpha1.AnalysisRole
		kinteract     kubeinteraction.Interface
		client        *staticAnalysisClient
		cancelContext bool
		wantResults   int
		wantResultErr string
		wantContent   string
		setup         func(t *testing.T, client *staticAnalysisClient)
	}{
		{
			name: "cel evaluation failure appends role error",
			roles: []v1alpha1.AnalysisRole{
				{Name: "broken-cel", Prompt: "analyze", OnCEL: "body.event.event_type ="},
			},
			kinteract:     &kitesthelper.KinterfaceTest{GetSecretResult: map[string]string{"llm-token": "token"}},
			wantResults:   1,
			wantResultErr: "CEL evaluation failed",
		},
		{
			name: "false cel skips role",
			roles: []v1alpha1.AnalysisRole{
				{Name: "skipped", Prompt: "analyze", OnCEL: "false"},
			},
			kinteract:   &kitesthelper.KinterfaceTest{GetSecretResult: map[string]string{"llm-token": "token"}},
			wantResults: 0,
		},
		{
			name: "missing secret appends client creation error",
			roles: []v1alpha1.AnalysisRole{
				{Name: "review", Prompt: "analyze", OnCEL: "true"},
			},
			kinteract:     &kitesthelper.KinterfaceTest{GetSecretResult: map[string]string{}},
			client:        &staticAnalysisClient{response: &AnalysisResponse{Content: "unused"}},
			wantResults:   1,
			wantResultErr: "client creation failed",
			setup: func(t *testing.T, client *staticAnalysisClient) {
				registerOpenAITestClient(t, client)
			},
		},
		{
			name: "analysis error appends role error after cancelled retry backoff",
			roles: []v1alpha1.AnalysisRole{
				{Name: "review", Prompt: "analyze", OnCEL: "true"},
			},
			kinteract:     &kitesthelper.KinterfaceTest{GetSecretResult: map[string]string{"llm-token": "token"}},
			client:        &staticAnalysisClient{err: errors.New("temporary failure")},
			cancelContext: true,
			wantResults:   1,
			wantResultErr: "context cancelled",
			setup: func(t *testing.T, client *staticAnalysisClient) {
				registerOpenAITestClient(t, client)
			},
		},
		{
			name: "analysis error retries before returning role error",
			roles: []v1alpha1.AnalysisRole{
				{Name: "review", Prompt: "analyze", OnCEL: "true"},
			},
			kinteract:     &kitesthelper.KinterfaceTest{GetSecretResult: map[string]string{"llm-token": "token"}},
			client:        &staticAnalysisClient{err: errors.New("temporary failure")},
			wantResults:   1,
			wantResultErr: "temporary failure",
			setup: func(t *testing.T, client *staticAnalysisClient) {
				registerOpenAITestClient(t, client)
			},
		},
		{
			name: "successful roles return responses",
			roles: []v1alpha1.AnalysisRole{
				{Name: "review", Prompt: "analyze", OnCEL: "true"},
				{Name: "summary", Prompt: "summarize", OnCEL: "true"},
			},
			kinteract:   &kitesthelper.KinterfaceTest{GetSecretResult: map[string]string{"llm-token": "token"}},
			client:      &staticAnalysisClient{response: &AnalysisResponse{Content: "analysis complete", TokensUsed: 7}},
			wantResults: 2,
			wantContent: "analysis complete",
			setup: func(t *testing.T, client *staticAnalysisClient) {
				registerOpenAITestClient(t, client)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(t, tt.client)
			}
			ctx := context.Background()
			if tt.cancelContext {
				cancelledCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelledCtx
			}

			results, err := analyze(ctx, &params.Run{}, tt.kinteract, testLogger,
				makeAnalysisRepository(tt.roles...), pr, event, &tprovider.TestProviderImp{})
			assert.NilError(t, err)
			assert.Equal(t, len(results), tt.wantResults)

			if tt.wantResultErr != "" {
				assert.ErrorContains(t, results[0].Error, tt.wantResultErr)
			}
			if tt.wantContent != "" {
				for _, result := range results {
					assert.Assert(t, result.Response != nil, "expected response for role %s", result.Role)
					assert.Equal(t, result.Response.Content, tt.wantContent)
				}
				assert.Equal(t, len(tt.client.requests), tt.wantResults)
				assert.Equal(t, tt.client.requests[0].MaxTokens, DefaultMaxTokens)
				assert.Equal(t, tt.client.requests[0].TimeoutSeconds, DefaultTimeoutSeconds)
			}
		})
	}
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

func TestExecuteAnalysisProcessesResults(t *testing.T) {
	testLogger, _ := logger.GetLogger()
	pr := makeCompletedPipelineRun()

	tests := []struct {
		name             string
		repo             *v1alpha1.Repository
		event            *info.Event
		client           *staticAnalysisClient
		provider         *recordingCommentProvider
		wantErr          string
		wantCommentCalls int
		setup            func(t *testing.T, repo *v1alpha1.Repository, client *staticAnalysisClient)
	}{
		{
			name: "no matching roles returns no results",
			repo: makeAnalysisRepository(v1alpha1.AnalysisRole{
				Name: "skipped", Prompt: "analyze", OnCEL: "false",
			}),
			event:    &info.Event{PullRequestNumber: 42},
			provider: &recordingCommentProvider{},
		},
		{
			name: "role error result is logged and skipped",
			repo: makeAnalysisRepository(v1alpha1.AnalysisRole{
				Name: "broken-cel", Prompt: "analyze", OnCEL: "body.event.pull_request_number =",
			}),
			event:    &info.Event{PullRequestNumber: 42},
			provider: &recordingCommentProvider{},
		},
		{
			name: "successful result posts pull request comment",
			repo: makeAnalysisRepository(v1alpha1.AnalysisRole{
				Name: "review", Prompt: "analyze", OnCEL: "true",
			}),
			event:            &info.Event{PullRequestNumber: 42},
			client:           &staticAnalysisClient{response: &AnalysisResponse{Content: "looks good", TokensUsed: 12}},
			provider:         &recordingCommentProvider{},
			wantCommentCalls: 1,
			setup: func(t *testing.T, _ *v1alpha1.Repository, client *staticAnalysisClient) {
				registerOpenAITestClient(t, client)
			},
		},
		{
			name: "pull request comment error is swallowed",
			repo: makeAnalysisRepository(v1alpha1.AnalysisRole{
				Name: "review", Prompt: "analyze", OnCEL: "true",
			}),
			event:            &info.Event{PullRequestNumber: 42},
			client:           &staticAnalysisClient{response: &AnalysisResponse{Content: "needs work"}},
			provider:         &recordingCommentProvider{err: errors.New("comment failed")},
			wantCommentCalls: 1,
			setup: func(t *testing.T, _ *v1alpha1.Repository, client *staticAnalysisClient) {
				registerOpenAITestClient(t, client)
			},
		},
		{
			name: "unsupported output after analysis skips comment",
			repo: makeAnalysisRepository(v1alpha1.AnalysisRole{
				Name: "review", Prompt: "analyze", OnCEL: "true",
			}),
			event:    &info.Event{PullRequestNumber: 42},
			client:   &staticAnalysisClient{response: &AnalysisResponse{Content: "skip output"}},
			provider: &recordingCommentProvider{},
			setup: func(t *testing.T, repo *v1alpha1.Repository, client *staticAnalysisClient) {
				client.beforeAnalyze = func() {
					repo.Spec.Settings.AIAnalysis.Roles[0].Output = "check-run"
				}
				registerOpenAITestClient(t, client)
			},
		},
		{
			name: "invalid analysis config returns wrapped error",
			repo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						AIAnalysis: &v1alpha1.AIAnalysisConfig{
							Enabled:  true,
							Provider: string(ProviderOpenAI),
						},
					},
				},
			},
			event:    &info.Event{PullRequestNumber: 42},
			provider: &recordingCommentProvider{},
			wantErr:  "LLM analysis failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(t, tt.repo, tt.client)
			}
			err := ExecuteAnalysis(context.Background(), &params.Run{},
				&kitesthelper.KinterfaceTest{GetSecretResult: map[string]string{"llm-token": "token"}},
				testLogger, tt.repo, pr, tt.event, tt.provider)

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}

			assert.NilError(t, err)
			assert.Equal(t, tt.provider.calls, tt.wantCommentCalls)
			if tt.wantCommentCalls > 0 {
				assert.Assert(t, len(tt.provider.comments) > 0)
				assert.Assert(t, len(tt.provider.markers) > 0)
			}
		})
	}
}

func TestPostPRComment(t *testing.T) {
	testLogger, _ := logger.GetLogger()

	tests := []struct {
		name        string
		event       *info.Event
		providerErr error
		wantErr     string
		wantCalls   int
	}{
		{
			name:  "no pull request number, skipped",
			event: &info.Event{PullRequestNumber: 0},
		},
		{
			name:      "with pull request number",
			event:     &info.Event{PullRequestNumber: 42},
			wantCalls: 1,
		},
		{
			name:        "create comment error",
			event:       &info.Event{PullRequestNumber: 42},
			providerErr: errors.New("provider rejected comment"),
			wantErr:     "failed to create PR comment",
			wantCalls:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov := &recordingCommentProvider{err: tt.providerErr}
			result := AnalysisResult{
				Role:     "test-role",
				Response: &AnalysisResponse{Content: "analysis content"},
			}
			err := postPRComment(context.Background(), result, tt.event, prov, testLogger)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Equal(t, prov.calls, tt.wantCalls)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, prov.calls, tt.wantCalls)
			if tt.wantCalls > 0 {
				assert.Assert(t, len(prov.comments) == 1)
				assert.Assert(t, len(prov.markers) == 1)
			}
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
		{
			name:       "non boolean cel expression errors",
			role:       v1alpha1.AnalysisRole{Name: "test-role", OnCEL: `"not-a-bool"`},
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
