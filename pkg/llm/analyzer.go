package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cel"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	llmcontext "github.com/openshift-pipelines/pipelines-as-code/pkg/llm/context"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm/ltypes"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apis "knative.dev/pkg/apis"
)

// AnalysisResult represents the result of an LLM analysis.
type AnalysisResult struct {
	Role     string
	Response *ltypes.AnalysisResponse
	Error    error
}

// Analyzer coordinates the LLM analysis process.
type Analyzer struct {
	run       *params.Run
	kinteract kubeinteraction.Interface
	factory   *Factory
	assembler *llmcontext.Assembler
	logger    *zap.SugaredLogger
}

// NewAnalyzer creates a new LLM analyzer.
func NewAnalyzer(run *params.Run, kinteract kubeinteraction.Interface, logger *zap.SugaredLogger) *Analyzer {
	return &Analyzer{
		run:       run,
		kinteract: kinteract,
		factory:   NewFactory(run, kinteract),
		assembler: llmcontext.NewAssembler(run, kinteract, logger),
		logger:    logger,
	}
}

// AnalyzeRequest represents a request for LLM analysis.
type AnalyzeRequest struct {
	PipelineRun *tektonv1.PipelineRun
	Event       *info.Event
	Repository  *v1alpha1.Repository
	Provider    provider.Interface
}

// Analyze performs LLM analysis based on the repository configuration.
func (a *Analyzer) Analyze(ctx context.Context, request *AnalyzeRequest) ([]AnalysisResult, error) {
	if request == nil {
		return nil, fmt.Errorf("analysis request is required")
	}
	if request.Repository == nil {
		return nil, nil
	}

	if request.Repository.Spec.Settings == nil || request.Repository.Spec.Settings.AIAnalysis == nil {
		a.logger.With(
			"repository", request.Repository.Name,
			"namespace", request.Repository.Namespace,
		).Debug("No AI analysis configuration found, skipping analysis")
		return nil, nil
	}

	config := request.Repository.Spec.Settings.AIAnalysis
	if !config.Enabled {
		a.logger.With(
			"repository", request.Repository.Name,
			"namespace", request.Repository.Namespace,
		).Debug("AI analysis is disabled, skipping analysis")
		return nil, nil
	}

	analysisLogger := a.logger.With(
		"provider", config.Provider,
		"pipeline_run", request.PipelineRun.Name,
		"namespace", request.PipelineRun.Namespace,
		"repository", request.Repository.Name,
		"roles_count", len(config.Roles),
	)

	analysisLogger.Info("Starting LLM analysis")

	if err := a.validateConfig(config); err != nil {
		analysisLogger.With("error", err).Error("Invalid AI analysis configuration")
		return nil, fmt.Errorf("invalid AI analysis configuration: %w", err)
	}

	// Secret must be in the same namespace as the Repository CR
	namespace := request.Repository.Namespace

	// Build CEL context for role filtering
	celContext, err := a.assembler.BuildCELContext(request.PipelineRun, request.Event, request.Repository)
	if err != nil {
		analysisLogger.With("error", err).Error("Failed to build CEL context")
		return nil, fmt.Errorf("failed to build CEL context: %w", err)
	}

	// Process each role
	results := []AnalysisResult{}
	contextCache := make(map[string]map[string]any)

	for _, role := range config.Roles {
		roleLogger := analysisLogger.With("role", role.Name)

		shouldTrigger, err := a.shouldTriggerRole(role, celContext, request.PipelineRun)
		if err != nil {
			roleLogger.With("error", err, "cel_expression", role.OnCEL).Warn("Failed to evaluate CEL expression")
			results = append(results, AnalysisResult{
				Role:  role.Name,
				Error: fmt.Errorf("CEL evaluation failed: %w", err),
			})
			continue
		}

		if !shouldTrigger {
			roleLogger.With("cel_expression", role.OnCEL).Debug("Role did not match CEL condition, skipping")
			continue
		}

		roleLogger.Info("Executing analysis role")

		contextKey := getContextCacheKey(role.ContextItems)
		var roleContext map[string]any
		var cached bool
		if roleContext, cached = contextCache[contextKey]; !cached {
			roleContext, err = a.assembler.BuildContext(
				ctx,
				request.PipelineRun,
				request.Event,
				role.ContextItems,
				request.Provider,
			)
			if err != nil {
				roleLogger.With("error", err).Warn("Failed to build context for role")
				results = append(results, AnalysisResult{
					Role:  role.Name,
					Error: fmt.Errorf("context build failed: %w", err),
				})
				continue
			}
			contextCache[contextKey] = roleContext
		}

		// Create LLM client for this role
		client, err := a.createClient(ctx, config, namespace, &role)
		if err != nil {
			roleLogger.With("error", err).Warn("Failed to create LLM client for role")
			results = append(results, AnalysisResult{
				Role:  role.Name,
				Error: fmt.Errorf("client creation failed: %w", err),
			})
			continue
		}

		// Create analysis request
		analysisRequest := &ltypes.AnalysisRequest{
			Prompt:         role.Prompt,
			Context:        roleContext,
			MaxTokens:      config.MaxTokens,
			TimeoutSeconds: config.TimeoutSeconds,
		}

		// Apply defaults
		if analysisRequest.MaxTokens == 0 {
			analysisRequest.MaxTokens = ltypes.DefaultConfig.MaxTokens
		}
		if analysisRequest.TimeoutSeconds == 0 {
			analysisRequest.TimeoutSeconds = ltypes.DefaultConfig.TimeoutSeconds
		}

		roleLogger.With(
			"max_tokens", analysisRequest.MaxTokens,
			"timeout_seconds", analysisRequest.TimeoutSeconds,
			"context_items", len(roleContext),
		).Debug("Sending analysis request to LLM")

		// Perform analysis
		var response *ltypes.AnalysisResponse
		var analysisErr error
		analysisStart := time.Now()

		const maxRetries = 3
		const retryDelay = 2 * time.Second

		for attempt := 1; attempt <= maxRetries; attempt++ {
			response, analysisErr = client.Analyze(ctx, analysisRequest)
			if analysisErr == nil {
				break // Success
			}

			roleLogger.With(
				"error", analysisErr,
				"attempt", attempt,
				"max_attempts", maxRetries,
			).Warn("LLM analysis attempt failed")

			if attempt < maxRetries {
				timer := time.NewTimer(retryDelay)
				select {
				case <-timer.C:
				case <-ctx.Done():
					roleLogger.With("context_error", ctx.Err()).Warn("Context cancelled during retry backoff")
					analysisErr = fmt.Errorf("context cancelled: %w", ctx.Err())
					attempt = maxRetries
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			}
		}
		analysisDuration := time.Since(analysisStart)

		if analysisErr != nil {
			roleLogger.With(
				"error", analysisErr,
				"duration", analysisDuration,
			).Warn("LLM analysis failed for role after all retries")
			results = append(results, AnalysisResult{
				Role:  role.Name,
				Error: analysisErr,
			})
			continue
		}

		roleLogger.With(
			"tokens_used", response.TokensUsed,
			"duration", analysisDuration,
			"response_length", len(response.Content),
		).Info("LLM analysis completed successfully")

		results = append(results, AnalysisResult{
			Role:     role.Name,
			Response: response,
		})
	}

	analysisLogger.With(
		"total_results", len(results),
		"successful_analyses", countSuccessfulResults(results),
		"failed_analyses", countFailedResults(results),
	).Info("LLM analysis completed")

	return results, nil
}

// getContextCacheKey generates a unique key for a context configuration.
func getContextCacheKey(config *v1alpha1.ContextConfig) string {
	if config == nil {
		return "default"
	}
	maxLines := 0
	if config.ContainerLogs != nil {
		maxLines = config.ContainerLogs.GetMaxLines()
	}

	return fmt.Sprintf("commit:%t-pr:%t-error:%t-logs:%t-%d",
		config.CommitContent,
		config.PRContent,
		config.ErrorContent,
		config.ContainerLogs != nil && config.ContainerLogs.Enabled,
		maxLines,
	)
}

// countSuccessfulResults counts the number of successful analysis results.
func countSuccessfulResults(results []AnalysisResult) int {
	count := 0
	for _, result := range results {
		if result.Error == nil && result.Response != nil {
			count++
		}
	}
	return count
}

// countFailedResults counts the number of failed analysis results.
func countFailedResults(results []AnalysisResult) int {
	count := 0
	for _, result := range results {
		if result.Error != nil {
			count++
		}
	}
	return count
}

// shouldTriggerRole evaluates the CEL expression to determine if a role should be triggered.
// If no on_cel is provided, defaults to triggering only for failed PipelineRuns.
func (a *Analyzer) shouldTriggerRole(role v1alpha1.AnalysisRole, celContext map[string]any, pr *tektonv1.PipelineRun) (bool, error) {
	if role.OnCEL == "" {
		succeededCondition := pr.Status.GetCondition(apis.ConditionSucceeded)

		return succeededCondition != nil && succeededCondition.Status == corev1.ConditionFalse, nil
	}

	result, err := cel.Value(role.OnCEL, celContext["body"],
		make(map[string]string), // headers - empty for pipeline context
		make(map[string]string), // pac params - empty for now
		make(map[string]any))    // files - empty for pipeline context
	if err != nil {
		return false, fmt.Errorf("failed to evaluate CEL expression '%s': %w", role.OnCEL, err)
	}

	if boolVal, ok := result.Value().(bool); ok {
		return boolVal, nil
	}

	return false, fmt.Errorf("CEL expression '%s' did not return boolean value", role.OnCEL)
}

// validateConfig validates the AI analysis configuration.
func (a *Analyzer) validateConfig(config *v1alpha1.AIAnalysisConfig) error {
	if config.Provider == "" {
		return fmt.Errorf("provider is required")
	}

	if config.TokenSecretRef == nil {
		return fmt.Errorf("token secret reference is required")
	}

	if len(config.Roles) == 0 {
		return fmt.Errorf("at least one analysis role is required")
	}

	for i, role := range config.Roles {
		if role.Name == "" {
			return fmt.Errorf("role[%d]: name is required", i)
		}

		if role.Prompt == "" {
			return fmt.Errorf("role[%d]: prompt is required", i)
		}

		output := role.GetOutput()
		if output != "pr-comment" {
			return fmt.Errorf("role[%d]: invalid output destination '%s' (only 'pr-comment' is currently supported)", i, output)
		}
	}

	return nil
}

// createClient creates an LLM client based on the configuration and role.
func (a *Analyzer) createClient(ctx context.Context, config *v1alpha1.AIAnalysisConfig, namespace string, role *v1alpha1.AnalysisRole) (ltypes.Client, error) {
	clientConfig := &ClientConfig{
		Provider:       ltypes.AIProvider(config.Provider),
		APIURL:         config.GetAPIURL(),
		Model:          role.GetModel(),
		TokenSecretRef: config.TokenSecretRef,
		TimeoutSeconds: config.TimeoutSeconds,
		MaxTokens:      config.MaxTokens,
	}

	if err := a.factory.ValidateConfig(clientConfig); err != nil {
		return nil, fmt.Errorf("invalid client configuration: %w", err)
	}

	return a.factory.CreateClient(ctx, clientConfig, namespace)
}

// GetSupportedProviders returns the list of supported LLM providers.
func (a *Analyzer) GetSupportedProviders() []ltypes.AIProvider {
	return a.factory.GetSupportedProviders()
}
