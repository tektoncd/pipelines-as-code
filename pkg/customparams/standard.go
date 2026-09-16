package customparams

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"go.uber.org/zap"
)

func (p *CustomParams) getChangedFiles(ctx context.Context) changedfiles.ChangedFiles {
	if p.vcx == nil {
		return changedfiles.ChangedFiles{}
	}
	changedFiles, err := p.vcx.GetFiles(ctx, p.event)
	if err != nil {
		p.eventEmitter.EmitMessage(p.repo, zap.ErrorLevel, "ParamsError", fmt.Sprintf("error getting changed files: %s", err.Error()))
		return changedfiles.ChangedFiles{}
	}
	changedFiles.RemoveDuplicates()
	return changedFiles
}

// makeStandardParamsFromEvent will create a map of standard params out of the event.
func (p *CustomParams) makeStandardParamsFromEvent(ctx context.Context) (map[string]string, map[string]any) {
	repoURL := p.event.URL
	// On bitbucket data center you are have a special url for checking it out, they
	// seemed to fix it in 2.0 but i guess we have to live with this until then.
	if p.event.CloneURL != "" {
		repoURL = p.event.CloneURL
	}
	changedFiles := p.getChangedFiles(ctx)
	triggerCommentAsSingleLine := strings.ReplaceAll(strings.ReplaceAll(p.event.TriggerComment, "\r\n", "\\n"), "\n", "\\n")
	pullRequestLabels := strings.Join(p.event.PullRequestLabel, "\\n")

	gitTag := ""
	if strings.HasPrefix(p.event.BaseBranch, "refs/tags/") {
		gitTag = strings.TrimPrefix(p.event.BaseBranch, "refs/tags/")
	}

	standardParams := map[string]string{
		"revision":            p.event.SHA,
		"repo_url":            repoURL,
		"repo_owner":          strings.ToLower(p.event.Organization),
		"repo_name":           strings.ToLower(p.event.Repository),
		"target_branch":       formatting.SanitizeBranch(p.event.BaseBranch),
		"source_branch":       formatting.SanitizeBranch(p.event.HeadBranch),
		"git_tag":             gitTag,
		"source_url":          p.event.HeadURL,
		"sender":              strings.ToLower(p.event.Sender),
		"target_namespace":    p.repo.GetNamespace(),
		"event_type":          opscomments.EventTypeBackwardCompat(p.eventEmitter, p.repo, p.event.EventType),
		"trigger_comment":     triggerCommentAsSingleLine,
		"pull_request_labels": pullRequestLabels,
	}
	changedFilesParams := map[string]any{
		"all":      changedFiles.All,
		"added":    changedFiles.Added,
		"deleted":  changedFiles.Deleted,
		"modified": changedFiles.Modified,
		"renamed":  changedFiles.Renamed,
	}
	return standardParams, changedFilesParams
}
