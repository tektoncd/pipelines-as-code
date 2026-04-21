package github

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	kstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
)

const (
	botType                      = "Bot"
	pendingApproval              = "Pending approval, waiting for an /ok-to-test"
	checkRunsFetchMaxRetries     = 2
	checkRunsFetchInitialBackoff = 200 * time.Millisecond
)

const taskStatusTemplate = `
<table>
  <tr><th>Status</th><th>Duration</th><th>Name</th></tr>

{{- range $taskrun := .TaskRunList }}
<tr>
<td>{{ formatCondition $taskrun.PipelineRunTaskRunStatus.Status.Conditions }}</td>
<td>{{ formatDuration $taskrun.PipelineRunTaskRunStatus.Status.StartTime $taskrun.PipelineRunTaskRunStatus.Status.CompletionTime }}</td><td>

{{ $taskrun.ConsoleLogURL }}

</td></tr>
{{- end }}
</table>`

// fetchAllCheckRunPages retrieves every page of check runs for the event SHA.
func (v *Provider) fetchAllCheckRunPages(ctx context.Context, runevent *info.Event) ([]*github.CheckRun, error) {
	var all []*github.CheckRun
	opt := github.ListOptions{PerPage: v.PaginedNumber}
	for {
		res, resp, err := wrapAPI(v, "list_check_runs_for_ref", func() (*github.ListCheckRunsResults, *github.Response, error) {
			return v.Client().Checks.ListCheckRunsForRef(ctx, runevent.Organization, runevent.Repository,
				runevent.SHA, &github.ListCheckRunsOptions{
					AppID:       v.ApplicationID,
					ListOptions: opt,
				})
		})
		if err != nil {
			return nil, err
		}
		all = append(all, res.CheckRuns...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return all, nil
}

func (v *Provider) searchCheckRuns(runs []*github.CheckRun, status providerstatus.StatusOpts) *int64 {
	for _, checkrun := range runs {
		if isPendingApprovalCheckrun(checkrun) || isFailedCheckrun(checkrun) {
			if v.canIUseCheckrunID(checkrun.ID) {
				return checkrun.ID
			}
		}
		if checkrun.ExternalID != nil && *checkrun.ExternalID == status.PipelineRunName {
			return checkrun.ID
		}
	}
	return nil
}

func (v *Provider) getExistingCheckRunID(ctx context.Context, runevent *info.Event, status providerstatus.StatusOpts) (*int64, error) {
	for {
		v.checkRunsCache.mu.Lock()
		if v.checkRunsCache.loading {
			done := v.checkRunsCache.done
			v.checkRunsCache.mu.Unlock()
			<-done
			continue
		}
		if v.checkRunsCache.populated {
			runs := v.checkRunsCache.runs
			v.checkRunsCache.mu.Unlock()
			return v.searchCheckRuns(runs, status), nil
		}

		v.checkRunsCache.loading = true
		v.checkRunsCache.done = make(chan struct{})
		v.checkRunsCache.mu.Unlock()

		runs, err := v.fetchAllCheckRunPagesWithRetry(ctx, runevent)

		v.checkRunsCache.mu.Lock()
		if err != nil {
			v.checkRunsCache.loading = false
		} else {
			v.checkRunsCache.runs = runs
			v.checkRunsCache.loading = false
			v.checkRunsCache.populated = true
		}
		close(v.checkRunsCache.done)
		v.checkRunsCache.mu.Unlock()

		if err != nil {
			return nil, err
		}
		return v.searchCheckRuns(runs, status), nil
	}
}

func (v *Provider) fetchAllCheckRunPagesWithRetry(ctx context.Context, runevent *info.Event) ([]*github.CheckRun, error) {
	var lastErr error
	for attempt := range checkRunsFetchMaxRetries + 1 {
		runs, err := v.fetchAllCheckRunPages(ctx, runevent)
		if err == nil {
			return runs, nil
		}
		lastErr = err

		if attempt == checkRunsFetchMaxRetries {
			return nil, err
		}

		backoff := time.Duration(1<<uint(attempt)) * checkRunsFetchInitialBackoff
		if v.Logger != nil {
			v.Logger.Debugf("check-runs lookup failed for %s/%s@%s (attempt %d/%d): %v; retrying in %v",
				runevent.Organization, runevent.Repository, runevent.SHA,
				attempt+1, checkRunsFetchMaxRetries+1, err, backoff)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-v.getClock().After(backoff):
		}
	}
	return nil, lastErr
}

func isPendingApprovalCheckrun(run *github.CheckRun) bool {
	if run == nil || run.Output == nil {
		return false
	}
	if run.Output.Title != nil && strings.Contains(*run.Output.Title, "Pending") &&
		run.Output.Summary != nil &&
		strings.Contains(*run.Output.Summary, "is waiting for approval") {
		return true
	}
	return false
}

func isFailedCheckrun(run *github.CheckRun) bool {
	if run == nil || run.Output == nil {
		return false
	}
	if run.Output.Title != nil && strings.Contains(*run.Output.Title, "pipelinerun start failure") &&
		run.Output.Summary != nil &&
		strings.Contains(*run.Output.Summary, "failed") {
		return true
	}
	return false
}

func (v *Provider) canIUseCheckrunID(checkrunid *int64) bool {
	v.mutex.Lock()
	defer v.mutex.Unlock()

	if v.checkRunID == 0 {
		v.checkRunID = *checkrunid
		return true
	}
	return false
}

func (v *Provider) createCheckRunStatus(ctx context.Context, runevent *info.Event, status providerstatus.StatusOpts) (*int64, error) {
	now := github.Timestamp{Time: time.Now()}
	checkrunoption := github.CreateCheckRunOptions{
		Name:    provider.GetCheckName(status, v.pacInfo),
		HeadSHA: runevent.SHA,
		Status:  github.Ptr(status.Status), // take status from statusOpts because it can be in_progress, queued, or failure // same for conclusion as well
		Output: &github.CheckRunOutput{
			Title:   github.Ptr(status.Title),
			Summary: github.Ptr(status.Summary),
			Text:    github.Ptr(status.Text),
		},
		DetailsURL: github.Ptr(status.DetailsURL),
		ExternalID: github.Ptr(status.PipelineRunName),
		StartedAt:  &now,
	}

	if status.Status != "in_progress" && status.Status != "queued" {
		checkrunoption.Conclusion = github.Ptr(string(status.Conclusion))
	}

	checkRun, _, err := wrapAPI(v, "create_check_run", func() (*github.CheckRun, *github.Response, error) {
		return v.Client().Checks.CreateCheckRun(ctx, runevent.Organization, runevent.Repository, checkrunoption)
	})
	if err != nil {
		return nil, err
	}
	return checkRun.ID, nil
}

func (v *Provider) getFailuresMessageAsAnnotations(ctx context.Context, pr *tektonv1.PipelineRun, pacopts *info.PacOpts) []*github.CheckRunAnnotation {
	annotations := []*github.CheckRunAnnotation{}
	r, err := regexp.Compile(pacopts.ErrorDetectionSimpleRegexp)
	if err != nil {
		v.Logger.Errorf("invalid regexp for filtering failure messages: %v", pacopts.ErrorDetectionSimpleRegexp)
		return annotations
	}
	intf, err := kubeinteraction.NewKubernetesInteraction(v.Run)
	if err != nil {
		v.Logger.Errorf("failed to create kubeinteraction: %v", err)
		return annotations
	}
	taskinfos := kstatus.CollectFailedTasksLogSnippet(ctx, v.Run, intf, pr, int64(pacopts.ErrorDetectionNumberOfLines))
	for _, taskinfo := range taskinfos {
		for _, errline := range strings.Split(taskinfo.LogSnippet, "\n") {
			results := map[string]string{}
			if !r.MatchString(errline) {
				continue
			}
			matches := r.FindStringSubmatch(errline)
			for i, name := range r.SubexpNames() {
				if i != 0 && name != "" {
					results[name] = matches[i]
				}
			}

			// check if we  have file in results
			var linenumber, errmsg, filename string
			var ok bool

			if filename, ok = results["filename"]; !ok {
				v.Logger.Errorf("regexp for filtering failure messages does not contain a filename regexp group: %v", pacopts.ErrorDetectionSimpleRegexp)
				continue
			}
			// remove ./ cause it would bug github otherwise
			filename = strings.TrimPrefix(filename, "./")

			if linenumber, ok = results["line"]; !ok {
				v.Logger.Errorf("regexp for filtering failure messages does not contain a line regexp group: %v", pacopts.ErrorDetectionSimpleRegexp)
				continue
			}

			if errmsg, ok = results["error"]; !ok {
				v.Logger.Errorf("regexp for filtering failure messages does not contain a error regexp group: %v", pacopts.ErrorDetectionSimpleRegexp)
				continue
			}

			ilinenumber, err := strconv.Atoi(linenumber)
			if err != nil {
				// can't do much regexp has probably failed to detect
				v.Logger.Errorf("cannot convert %s as integer: %v", linenumber, err)
				continue
			}
			annotations = append(annotations, &github.CheckRunAnnotation{
				Path:            github.Ptr(filename),
				StartLine:       github.Ptr(ilinenumber),
				EndLine:         github.Ptr(ilinenumber),
				AnnotationLevel: github.Ptr("failure"),
				Message:         github.Ptr(errmsg),
			})
		}
	}
	return annotations
}

// getOrUpdateCheckRunStatus create a status via the checkRun API, which is only
// available with GitHub apps tokens.
func (v *Provider) getOrUpdateCheckRunStatus(ctx context.Context, runevent *info.Event, statusOpts providerstatus.StatusOpts) error {
	var err error
	var checkRunID *int64
	var found bool
	pacopts := v.pacInfo

	// The purpose of this condition is to limit the generation of checkrun IDs
	// when multiple pipelineruns fail. In such cases, generate only one checkrun ID,
	// regardless of the number of failed pipelineruns.
	if statusOpts.Title == "Failed" && statusOpts.PipelineRunName == "" {
		// setting different title to handle multiple checkrun cases
		statusOpts.Title = "pipelinerun start failure"
		if statusOpts.InstanceCountForCheckRun >= 1 {
			return nil
		}
	}

	// check if pipelineRun has the label with checkRun-id
	if statusOpts.PipelineRun != nil {
		var id string
		id, found = statusOpts.PipelineRun.GetAnnotations()[keys.CheckRunID]
		if found {
			checkID, err := strconv.Atoi(id)
			if err != nil {
				return fmt.Errorf("api error: cannot convert checkrunid")
			}
			checkRunID = github.Ptr(int64(checkID))
		}
	}
	if !found {
		if checkRunID, _ = v.getExistingCheckRunID(ctx, runevent, statusOpts); checkRunID == nil {
			checkRunID, err = v.createCheckRunStatus(ctx, runevent, statusOpts)
			if err != nil {
				return err
			}
		}

		// Patch the pipelineRun with the checkRunID and logURL only when the pipelineRun is not nil and has a name
		// because on validation failed PipelineRun will provide PipelineRun struct but it is not a valid resource
		// created in cluster so if its only validation error report then ignore patching the pipelineRun.
		if statusOpts.PipelineRun != nil && (statusOpts.PipelineRun.GetName() != "" || statusOpts.PipelineRun.GetGenerateName() != "") {
			if _, err := action.PatchPipelineRun(ctx, v.Logger, "checkRunID and logURL", v.Run.Clients.Tekton, statusOpts.PipelineRun, metadataPatch(checkRunID, statusOpts.DetailsURL)); err != nil {
				return err
			}
		}
	}

	text := statusOpts.Text
	checkRunOutput := &github.CheckRunOutput{
		Title:   &statusOpts.Title,
		Summary: &statusOpts.Summary,
	}

	if statusOpts.PipelineRun != nil {
		if pacopts.ErrorDetection {
			checkRunOutput.Annotations = v.getFailuresMessageAsAnnotations(ctx, statusOpts.PipelineRun, pacopts)
		}
	}

	checkRunOutput.Text = github.Ptr(text)

	opts := github.UpdateCheckRunOptions{
		Name:   provider.GetCheckName(statusOpts, pacopts),
		Status: github.Ptr(statusOpts.Status),
		Output: checkRunOutput,
	}
	if statusOpts.PipelineRunName != "" {
		opts.ExternalID = github.Ptr(statusOpts.PipelineRunName)
	}
	if statusOpts.DetailsURL != "" {
		opts.DetailsURL = &statusOpts.DetailsURL
	}

	// Only set completed-at if conclusion is set (which means finished)
	if statusOpts.Conclusion != "" && statusOpts.Conclusion != providerstatus.ConclusionPending {
		opts.CompletedAt = &github.Timestamp{Time: time.Now()}
		opts.Conclusion = github.Ptr(string(statusOpts.Conclusion))
	}
	if isPipelineRunCancelledOrStopped(statusOpts.PipelineRun) {
		opts.Conclusion = github.Ptr("cancelled")
	}

	_, _, err = wrapAPI(v, "update_check_run", func() (*github.CheckRun, *github.Response, error) {
		return v.Client().Checks.UpdateCheckRun(ctx, runevent.Organization, runevent.Repository, *checkRunID, opts)
	})
	return err
}

func isPipelineRunCancelledOrStopped(run *tektonv1.PipelineRun) bool {
	if run == nil {
		return false
	}
	if run.IsCancelled() || run.IsGracefullyCancelled() || run.IsGracefullyStopped() {
		return true
	}
	return false
}

func metadataPatch(checkRunID *int64, logURL string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{
				keys.CheckRunID: strconv.FormatInt(*checkRunID, 10),
			},
			"annotations": map[string]string{
				keys.LogURL:     logURL,
				keys.CheckRunID: strconv.FormatInt(*checkRunID, 10),
			},
		},
	}
}

// createStatusCommit use the classic/old statuses API which is available when we
// don't have a github app token.
func (v *Provider) createStatusCommit(ctx context.Context, runevent *info.Event, status providerstatus.StatusOpts) error {
	var err error
	now := time.Now()
	switch status.Conclusion {
	case providerstatus.ConclusionNeutral:
		status.Conclusion = providerstatus.ConclusionSuccess // We don't have a choice other than setting as success, no pending here.
	case providerstatus.ConclusionPending:
		if status.Title != "" {
			status.Conclusion = providerstatus.ConclusionPending
		}
	default:
	}
	if status.Status == "in_progress" {
		status.Conclusion = providerstatus.ConclusionPending
	}

	ghstatus := github.RepoStatus{
		State:       github.Ptr(string(status.Conclusion)),
		TargetURL:   github.Ptr(status.DetailsURL),
		Description: github.Ptr(status.Title),
		Context:     github.Ptr(provider.GetCheckName(status, v.pacInfo)),
		CreatedAt:   &github.Timestamp{Time: now},
	}

	if _, _, err := wrapAPI(v, "create_status", func() (*github.RepoStatus, *github.Response, error) {
		return v.Client().Repositories.CreateStatus(ctx,
			runevent.Organization, runevent.Repository, runevent.SHA, ghstatus)
	}); err != nil {
		return err
	}
	eventType := triggertype.IsPullRequestType(runevent.EventType)
	if opscomments.IsAnyOpsEventType(eventType.String()) {
		eventType = triggertype.PullRequest
	}

	var commentStrategy string
	if v.repo != nil && v.repo.Spec.Settings != nil && v.repo.Spec.Settings.Github != nil {
		commentStrategy = v.repo.Spec.Settings.Github.CommentStrategy
	}

	switch commentStrategy {
	case provider.DisableAllCommentStrategy:
		v.Logger.Warn("github: comments related to PipelineRuns status have been disabled for Github pull requests")
		return nil
	case provider.UpdateCommentStrategy:
		if (status.Status == "completed" || (status.Status == "queued" && status.Title == pendingApproval)) &&
			status.Text != "" && eventType == triggertype.PullRequest {
			statusComment := v.formatPipelineComment(runevent.SHA, status)
			// Creating the prefix that is added to the status comment for a pipeline run.
			plrStatusCommentPrefix := fmt.Sprintf(provider.PlrStatusCommentPrefixTemplate, status.OriginalPipelineRunName)
			// The entire markdown comment, including the prefix that is added to the pull request for the pipelinerun.
			markdownStatusComment := fmt.Sprintf("%s\n%s", plrStatusCommentPrefix, statusComment)

			if err := v.CreateComment(ctx, runevent, markdownStatusComment, plrStatusCommentPrefix); err != nil {
				v.eventEmitter.EmitMessage(
					v.repo,
					zap.ErrorLevel,
					"PipelineRunCommentCreationError",
					fmt.Sprintf("failed to create comment: %s", err.Error()),
				)
				return err
			}
		}
	default:
		if (status.Status == "completed" || (status.Status == "queued" && status.Title == pendingApproval)) &&
			status.Text != "" && eventType == triggertype.PullRequest {
			_, _, err = wrapAPI(v, "create_issue_comment", func() (*github.IssueComment, *github.Response, error) {
				return v.Client().Issues.CreateComment(ctx, runevent.Organization, runevent.Repository,
					runevent.PullRequestNumber,
					&github.IssueComment{
						Body: github.Ptr(fmt.Sprintf("%s<br>%s", status.Summary, status.Text)),
					},
				)
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (v *Provider) CreateStatus(ctx context.Context, runevent *info.Event, statusOpts providerstatus.StatusOpts) error {
	if v.ghClient == nil {
		return fmt.Errorf("cannot set status on github no token or url set")
	}

	// If the request comes from a bot user, skip setting the status and just log the event silently
	if statusOpts.AccessDenied && v.userType == botType {
		return nil
	}

	switch statusOpts.Conclusion {
	case providerstatus.ConclusionSuccess:
		statusOpts.Title = "Success"
		statusOpts.Summary = "has <b>successfully</b> validated your commit."
	case providerstatus.ConclusionFailure:
		if statusOpts.Title == "" {
			statusOpts.Title = "Failed"
		}
		statusOpts.Summary = "has <b>failed</b>."
	case providerstatus.ConclusionPending:
		// for concurrency set title as pending
		if statusOpts.Title == "" {
			statusOpts.Title = "Pending"
			statusOpts.Summary = "is skipping this commit."
		} else {
			// for unauthorized user set title as Pending approval
			statusOpts.Summary = "is waiting for approval."
		}
	case providerstatus.ConclusionCancelled:
		statusOpts.Title = "Cancelled"
		statusOpts.Summary = "has been <b>cancelled</b>."
	case providerstatus.ConclusionNeutral:
		if statusOpts.Title == "" {
			statusOpts.Title = "Unknown"
		}
		statusOpts.Summary = "<b>Completed</b>"
	case providerstatus.ConclusionCompleted, providerstatus.ConclusionSkipped:
	}

	if statusOpts.Status == "in_progress" {
		statusOpts.Title = "CI has Started"
		statusOpts.Summary = "is running."
	}

	onPr := ""
	if statusOpts.OriginalPipelineRunName != "" {
		onPr = "/" + statusOpts.OriginalPipelineRunName
	}
	statusOpts.Summary = fmt.Sprintf("%s%s %s", v.pacInfo.ApplicationName, onPr, statusOpts.Summary)
	// If we have an installationID which mean we have a github apps and we can use the checkRun API
	if runevent.InstallationID > 0 {
		return v.getOrUpdateCheckRunStatus(ctx, runevent, statusOpts)
	}

	// Otherwise use the update status commit API
	return v.createStatusCommit(ctx, runevent, statusOpts)
}

func (v *Provider) formatPipelineComment(sha string, status providerstatus.StatusOpts) string {
	var emoji, title string

	switch {
	case status.Status == "queued":
		emoji = "⏳"
		title = "Queued"
	case status.Status == "in_progress":
		emoji = "🚀"
		title = "Running"
	case status.Status == "completed" && status.Conclusion == "success":
		emoji = "✅"
		title = "Success"
	case status.Status == "completed" && status.Conclusion == "failure":
		emoji = "❌"
		title = "Failed"
	case status.Status == "completed" && status.Conclusion == "cancelled":
		emoji = "⚠️"
		title = "Cancelled"
	case status.Status == "completed" && status.Conclusion == "neutral":
		emoji = "ℹ️"
		title = "Completed"
	default:
		emoji = "ℹ️"
		title = "Status Update"
	}

	return fmt.Sprintf("%s **%s: %s for %s**\n\n%s<br>%s",
		emoji, title, status.OriginalPipelineRunName, sha, status.Summary, status.Text)
}
