package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cel"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	llmcontext "github.com/openshift-pipelines/pipelines-as-code/pkg/llm/context"
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
	Response *AnalysisResponse
	Error    error
}

// ExecuteAnalysis performs the complete LLM analysis workflow.
// This is the single entry point called by the reconciler.
func ExecuteAnalysis(
	ctx context.Context,
	run *params.Run,
	kinteract kubeinteraction.Interface,
	logger *zap.SugaredLogger,
	repo *v1alpha1.Repository,
	pr *tektonv1.PipelineRun,
	event *info.Event,
	prov provider.Interface,
) error {
	if repo.Spec.Settings == nil || repo.Spec.Settings.AIAnalysis == nil || !repo.Spec.Settings.AIAnalysis.Enabled {
		logger.Debug("AI analysis not configured or disabled, skipping")
		return nil
	}

	logger.Infof("Starting LLM analysis for pipeline %s/%s", pr.Namespace, pr.Name)

	results, err := analyze(ctx, run, kinteract, logger, repo, pr, event, prov)
	if err != nil {
		return fmt.Errorf("LLM analysis failed: %w", err)
	}

	if len(results) == 0 {
		logger.Debug("No analysis results generated")
		return nil
	}

	for _, result := range results {
		if result.Error != nil {
			logger.Warnf("Analysis failed for role %s: %v", result.Role, result.Error)
			continue
		}
		if result.Response == nil {
			logger.Warnf("No response for role %s", result.Role)
			continue
		}

		logger.Infof("Processing LLM analysis result for role %s, tokens used: %d", result.Role, result.Response.TokensUsed)

		// Find the role config and validate output destination
		var roleConfig *v1alpha1.AnalysisRole
		for i := range repo.Spec.Settings.AIAnalysis.Roles {
			if repo.Spec.Settings.AIAnalysis.Roles[i].Name == result.Role {
				roleConfig = &repo.Spec.Settings.AIAnalysis.Roles[i]
				break
			}
		}
		if roleConfig != nil {
			output := roleConfig.GetOutput()
			if output != "pr-comment" {
				logger.Warnf("Unsupported output destination %q for role %s, skipping (only 'pr-comment' is supported)", output, result.Role)
				continue
			}
		}

		if err := postPRComment(ctx, result, event, prov, logger); err != nil {
			logger.Warnf("Failed to handle output for role %s: %v", result.Role, err)
		}
	}

	return nil
}

// analyze performs LLM analysis based on the repository configuration.
func analyze(
	ctx context.Context,
	run *params.Run,
	kinteract kubeinteraction.Interface,
	logger *zap.SugaredLogger,
	repo *v1alpha1.Repository,
	pr *tektonv1.PipelineRun,
	event *info.Event,
	prov provider.Interface,
) ([]AnalysisResult, error) {
	if repo == nil || repo.Spec.Settings == nil || repo.Spec.Settings.AIAnalysis == nil {
		return nil, nil
	}

	config := repo.Spec.Settings.AIAnalysis
	if !config.Enabled {
		return nil, nil
	}

	analysisLogger := logger.With(
		"provider", config.Provider,
		"pipeline_run", pr.Name,
		"namespace", pr.Namespace,
		"repository", repo.Name,
		"roles_count", len(config.Roles),
	)

	analysisLogger.Info("Starting LLM analysis")

	if err := validateAnalysisConfig(config); err != nil {
		analysisLogger.With("error", err).Error("Invalid AI analysis configuration")
		return nil, fmt.Errorf("invalid AI analysis configuration: %w", err)
	}

	namespace := repo.Namespace
	assembler := llmcontext.NewAssembler(run, kinteract, logger)

	celContext, err := assembler.BuildCELContext(pr, event, repo)
	if err != nil {
		analysisLogger.With("error", err).Error("Failed to build CEL context")
		return nil, fmt.Errorf("failed to build CEL context: %w", err)
	}

	results := []AnalysisResult{}
	contextCache := make(map[string]map[string]any)

	for _, role := range config.Roles {
		roleLogger := analysisLogger.With("role", role.Name)

		shouldTrigger, err := shouldTriggerRole(role, celContext, pr)
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
			roleContext, err = assembler.BuildContext(ctx, pr, event, role.ContextItems, prov)
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

		client, err := NewClient(
			ctx,
			AIProvider(config.Provider),
			config.TokenSecretRef,
			namespace,
			kinteract,
			config.GetAPIURL(),
			role.GetModel(),
			config.TimeoutSeconds,
			config.MaxTokens,
		)
		if err != nil {
			roleLogger.With("error", err).Warn("Failed to create LLM client for role")
			results = append(results, AnalysisResult{
				Role:  role.Name,
				Error: fmt.Errorf("client creation failed: %w", err),
			})
			continue
		}

		analysisRequest := &AnalysisRequest{
			Prompt:         role.Prompt,
			Context:        roleContext,
			MaxTokens:      config.MaxTokens,
			TimeoutSeconds: config.TimeoutSeconds,
		}

		if analysisRequest.MaxTokens == 0 {
			analysisRequest.MaxTokens = DefaultMaxTokens
		}
		if analysisRequest.TimeoutSeconds == 0 {
			analysisRequest.TimeoutSeconds = DefaultTimeoutSeconds
		}

		roleLogger.With(
			"max_tokens", analysisRequest.MaxTokens,
			"timeout_seconds", analysisRequest.TimeoutSeconds,
			"context_items", len(roleContext),
		).Debug("Sending analysis request to LLM")

		var response *AnalysisResponse
		var analysisErr error
		analysisStart := time.Now()

		const maxRetries = 3
		const retryDelay = 2 * time.Second

		for attempt := 1; attempt <= maxRetries; attempt++ {
			response, analysisErr = client.Analyze(ctx, analysisRequest)
			if analysisErr == nil {
				break
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

// postPRComment posts LLM analysis as a PR comment.
func postPRComment(ctx context.Context, result AnalysisResult, event *info.Event, prov provider.Interface, logger *zap.SugaredLogger) error {
	if event.PullRequestNumber == 0 {
		logger.Debug("No pull request associated with this event, skipping PR comment")
		return nil
	}

	comment := fmt.Sprintf("## 🤖 AI Analysis - %s\n\n%s\n\n---\n*Generated by Pipelines-as-Code LLM Analysis*",
		result.Role, result.Response.Content)

	updateMarker := fmt.Sprintf("llm-analysis-%s", result.Role)

	if err := prov.CreateComment(ctx, event, comment, updateMarker); err != nil {
		return fmt.Errorf("failed to create PR comment: %w", err)
	}

	logger.Infof("Posted LLM analysis as PR comment for role %s", result.Role)
	return nil
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

func countSuccessfulResults(results []AnalysisResult) int {
	count := 0
	for _, result := range results {
		if result.Error == nil && result.Response != nil {
			count++
		}
	}
	return count
}

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
func shouldTriggerRole(role v1alpha1.AnalysisRole, celContext map[string]any, pr *tektonv1.PipelineRun) (bool, error) {
	if role.OnCEL == "" {
		succeededCondition := pr.Status.GetCondition(apis.ConditionSucceeded)
		return succeededCondition != nil && succeededCondition.Status == corev1.ConditionFalse, nil
	}

	result, err := cel.Value(role.OnCEL, celContext["body"],
		make(map[string]string),
		make(map[string]string),
		make(map[string]any))
	if err != nil {
		return false, fmt.Errorf("failed to evaluate CEL expression '%s': %w", role.OnCEL, err)
	}

	if boolVal, ok := result.Value().(bool); ok {
		return boolVal, nil
	}

	return false, fmt.Errorf("CEL expression '%s' did not return boolean value", role.OnCEL)
}

// validateAnalysisConfig validates the AI analysis configuration.
func validateAnalysisConfig(config *v1alpha1.AIAnalysisConfig) error {
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
