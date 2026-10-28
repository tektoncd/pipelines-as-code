package context

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/kubeinteraction"
	kstatus "github.com/tektoncd/pipelines-as-code/pkg/kubeinteraction/status"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	"github.com/tektoncd/pipelines-as-code/pkg/provider"
	"github.com/tektoncd/pipelines-as-code/pkg/sort"
	"go.uber.org/zap"
)

const (
	// DefaultMaxLogLines is the default maximum number of log lines to include in context.
	DefaultMaxLogLines = 50
)

// Assembler builds context data for LLM analysis from pipeline and event information.
type Assembler struct {
	run       *params.Run
	kinteract kubeinteraction.Interface
	logger    *zap.SugaredLogger
}

// NewAssembler creates a new context assembler.
func NewAssembler(run *params.Run, kinteract kubeinteraction.Interface, logger *zap.SugaredLogger) *Assembler {
	return &Assembler{
		run:       run,
		kinteract: kinteract,
		logger:    logger,
	}
}

// BuildContext assembles context data based on the provided configuration.
func (a *Assembler) BuildContext(
	ctx context.Context,
	pipelineRun *tektonv1.PipelineRun,
	event *info.Event,
	contextConfig *v1alpha1.ContextConfig,
	provider provider.Interface,
) (map[string]any, error) {
	contextData := make(map[string]any)

	if contextConfig == nil {
		return a.buildBasicPipelineContext(pipelineRun, event), nil
	}

	if contextConfig.CommitContent {
		if commitData, err := a.buildCommitContent(ctx, event, provider); err != nil {
			a.logger.Warnf("we couldn't retrieve the commit details. this may limit the analysis, but we'll proceed with the available information. (error: %v)", err)
		} else {
			contextData["commit"] = commitData
		}
	}

	if contextConfig.PRContent {
		if prData, err := a.buildPRContent(ctx, event, provider); err != nil {
			a.logger.Warnf("Failed to build PR content: %v", err)
		} else {
			contextData["pull_request"] = prData
		}
	}

	if contextConfig.ErrorContent {
		if errorData := a.buildErrorContent(ctx, pipelineRun); errorData != nil {
			contextData["errors"] = errorData
		}
	}

	if contextConfig.ContainerLogs != nil && contextConfig.ContainerLogs.Enabled {
		maxLines := contextConfig.ContainerLogs.MaxLines
		if maxLines == 0 {
			maxLines = DefaultMaxLogLines
		}
		if logData := a.buildContainerLogs(ctx, pipelineRun, maxLines); logData != nil {
			contextData["logs"] = logData
		}
	}

	// Always include basic pipeline information
	contextData["pipeline"] = a.buildBasicPipelineContext(pipelineRun, event)

	return contextData, nil
}

// buildBasicPipelineContext creates basic pipeline context information.
func (a *Assembler) buildBasicPipelineContext(pipelineRun *tektonv1.PipelineRun, event *info.Event) map[string]any {
	pipelineData := map[string]any{
		"name":      pipelineRun.Name,
		"namespace": pipelineRun.Namespace,
		"status":    "unknown",
	}

	if len(pipelineRun.Status.Conditions) > 0 {
		condition := pipelineRun.Status.Conditions[0]
		pipelineData["status"] = condition.Status
		pipelineData["reason"] = condition.Reason
		pipelineData["message"] = condition.Message
	}

	if pipelineRun.Status.StartTime != nil {
		pipelineData["start_time"] = pipelineRun.Status.StartTime.Time
	}
	if pipelineRun.Status.CompletionTime != nil {
		pipelineData["completion_time"] = pipelineRun.Status.CompletionTime.Time
	}

	if event != nil {
		pipelineData["event_type"] = event.EventType
		pipelineData["sha"] = event.SHA
		pipelineData["base_branch"] = event.BaseBranch
		pipelineData["head_branch"] = event.HeadBranch
	}

	return pipelineData
}

// buildCommitContent builds commit-related context information.
func (a *Assembler) buildCommitContent(ctx context.Context, event *info.Event, provider provider.Interface) (map[string]any, error) {
	if event == nil {
		return nil, fmt.Errorf("event is nil")
	}

	commitData := map[string]any{
		"sha":     event.SHA,
		"message": event.SHATitle,
	}

	// Try to get additional commit information from the provider
	if provider != nil {
		if err := provider.GetCommitInfo(ctx, event); err != nil {
			a.logger.Warnf("Failed to get additional commit info: %v", err)
		}
	}

	// Add extended commit fields if available (after GetCommitInfo or if already populated)
	// Add URL if available
	if event.SHAURL != "" {
		commitData["url"] = event.SHAURL
	}

	// Add full commit message if available and different from title
	if event.SHAMessage != "" && event.SHAMessage != event.SHATitle {
		commitData["full_message"] = event.SHAMessage
	}

	// Add author information if available
	// Note: Email addresses are excluded for privacy/PII reasons
	if event.SHAAuthorName != "" || !event.SHAAuthorDate.IsZero() {
		author := map[string]any{}
		if event.SHAAuthorName != "" {
			author["name"] = event.SHAAuthorName
		}
		if !event.SHAAuthorDate.IsZero() {
			author["date"] = event.SHAAuthorDate
		}
		commitData["author"] = author
	}

	// Add committer information if available
	// Note: Email addresses are excluded for privacy/PII reasons
	if event.SHACommitterName != "" || !event.SHACommitterDate.IsZero() {
		committer := map[string]any{}
		if event.SHACommitterName != "" {
			committer["name"] = event.SHACommitterName
		}
		if !event.SHACommitterDate.IsZero() {
			committer["date"] = event.SHACommitterDate
		}
		commitData["committer"] = committer
	}

	return commitData, nil
}

// buildPRContent builds pull request context information.
func (a *Assembler) buildPRContent(_ context.Context, event *info.Event, _ provider.Interface) (map[string]any, error) {
	if event == nil || event.PullRequestNumber == 0 {
		return nil, fmt.Errorf("no pull request information available")
	}

	prData := map[string]any{
		"number":      event.PullRequestNumber,
		"title":       event.PullRequestTitle,
		"head_branch": event.HeadBranch,
		"base_branch": event.BaseBranch,
	}

	// Note: PR body is not available in the Event struct

	return prData, nil
}

// buildErrorContent builds error and failure context information.
func (a *Assembler) buildErrorContent(ctx context.Context, pipelineRun *tektonv1.PipelineRun) map[string]any {
	if len(pipelineRun.Status.Conditions) == 0 {
		return nil
	}

	condition := pipelineRun.Status.Conditions[0]
	if condition.Status != "False" {
		return nil
	}

	errorData := map[string]any{
		"condition_reason":  condition.Reason,
		"condition_message": condition.Message,
	}

	// Get detailed task failure information
	taskInfos := kstatus.CollectFailedTasksLogSnippet(ctx, a.run, a.kinteract, pipelineRun, 3)
	if len(taskInfos) > 0 {
		sortedTaskInfos := sort.TaskInfos(taskInfos)

		var failedTasks []map[string]any
		for _, taskInfo := range sortedTaskInfos {
			failedTask := map[string]any{
				"name":        taskInfo.Name,
				"reason":      taskInfo.Reason,
				"message":     taskInfo.Message,
				"log_snippet": taskInfo.LogSnippet,
			}

			if taskInfo.DisplayName != "" {
				failedTask["display_name"] = taskInfo.DisplayName
			}

			if taskInfo.CompletionTime != nil {
				failedTask["completion_time"] = taskInfo.CompletionTime.Time
			}

			failedTasks = append(failedTasks, failedTask)
		}

		errorData["failed_tasks"] = failedTasks
	}

	return errorData
}

// buildContainerLogs builds container logs context information.
func (a *Assembler) buildContainerLogs(ctx context.Context, pipelineRun *tektonv1.PipelineRun, maxLines int) map[string]any {
	// Get detailed task information with logs
	taskInfos := kstatus.CollectFailedTasksLogSnippet(ctx, a.run, a.kinteract, pipelineRun, int64(maxLines))
	if len(taskInfos) == 0 {
		return nil
	}

	logs := []map[string]any{}
	for _, taskInfo := range taskInfos {
		logEntry := map[string]any{
			"task_name": taskInfo.Name,
			"log_lines": strings.Split(taskInfo.LogSnippet, "\n"),
		}

		if taskInfo.DisplayName != "" {
			logEntry["display_name"] = taskInfo.DisplayName
		}

		logs = append(logs, logEntry)
	}

	return map[string]any{
		"failed_tasks_logs": logs,
		"max_lines":         maxLines,
	}
}

// BuildCELContext builds context data for CEL expression evaluation.
//
// This function exposes a carefully curated subset of event data to CEL expressions.
// The following fields are EXCLUDED for security or internal implementation reasons:
//   - event.Provider (contains API tokens and webhook secrets)
//   - event.Request (contains raw HTTP headers and payload which may include secrets)
//   - event.InstallationID, AccountID, GHEURL, CloneURL (provider-specific internal IDs/URLs)
//   - event.SourceProjectID, TargetProjectID (GitLab-specific internal IDs)
//   - event.State (internal state management fields)
//   - event.Event (raw event object, already represented in structured fields)
func (a *Assembler) BuildCELContext(
	pipelineRun *tektonv1.PipelineRun,
	event *info.Event,
	repo *v1alpha1.Repository,
) (map[string]any, error) {
	// Convert PipelineRun to map for CEL access
	prMap, err := a.pipelineRunToMap(pipelineRun)
	if err != nil {
		return nil, fmt.Errorf("failed to convert PipelineRun to map: %w", err)
	}

	// Convert Repository to map for CEL access
	repoMap, err := a.repositoryToMap(repo)
	if err != nil {
		return nil, fmt.Errorf("failed to convert Repository to map: %w", err)
	}

	celData := map[string]any{
		"body": map[string]any{
			"pipelineRun": prMap,
			"repository":  repoMap,
		},
		"pac": make(map[string]string), // PAC parameters will be populated by caller
	}

	// Add event information to CEL context
	if event != nil {
		eventMap := map[string]any{
			// Event type and trigger information
			"event_type":     event.EventType,
			"trigger_target": event.TriggerTarget.String(),

			// Branch and commit information
			"sha":            event.SHA,
			"sha_title":      event.SHATitle,
			"base_branch":    event.BaseBranch,
			"head_branch":    event.HeadBranch,
			"default_branch": event.DefaultBranch,

			// Repository information
			"organization": event.Organization,
			"repository":   event.Repository,

			// URLs (web URLs, not git URLs)
			"url":      event.URL,
			"sha_url":  event.SHAURL,
			"base_url": event.BaseURL,
			"head_url": event.HeadURL,

			// User information
			"sender": event.Sender,

			// Webhook-specific
			"target_pipelinerun": event.TargetPipelineRun,
		}

		// Pull/Merge Request specific fields (only populated for PR events)
		if event.PullRequestNumber > 0 {
			eventMap["pull_request_number"] = event.PullRequestNumber
			eventMap["pull_request_title"] = event.PullRequestTitle
			eventMap["pull_request_labels"] = event.PullRequestLabel
		}

		// Comment trigger field (only populated when triggered by comment)
		if event.TriggerComment != "" {
			eventMap["trigger_comment"] = event.TriggerComment
		}

		if bodyMap, ok := celData["body"].(map[string]any); ok {
			bodyMap["event"] = eventMap
		}
	}

	return celData, nil
}

// pipelineRunToMap converts a PipelineRun to a map for CEL access.
func (a *Assembler) pipelineRunToMap(pr *tektonv1.PipelineRun) (map[string]any, error) {
	// Marshal to JSON and back to get a clean map representation
	jsonData, err := json.Marshal(pr)
	if err != nil {
		return nil, err
	}

	var prMap map[string]any
	if err := json.Unmarshal(jsonData, &prMap); err != nil {
		return nil, err
	}

	return prMap, nil
}

// repositoryToMap converts a Repository to a map for CEL access.
func (a *Assembler) repositoryToMap(repo *v1alpha1.Repository) (map[string]any, error) {
	// Marshal to JSON and back to get a clean map representation
	jsonData, err := json.Marshal(repo)
	if err != nil {
		return nil, err
	}

	var repoMap map[string]any
	if err := json.Unmarshal(jsonData, &repoMap); err != nil {
		return nil, err
	}

	return repoMap, nil
}
