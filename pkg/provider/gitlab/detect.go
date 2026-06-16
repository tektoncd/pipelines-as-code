package gitlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/zap"
)

// Detect detects events and validates if it is a valid gitlab event Pipelines as Code supports and
// decides whether to process or reject it.
// returns a boolean value whether to process or reject, logger with event metadata, and error if any occurred.
func (v *Provider) Detect(req *http.Request, payload string, logger *zap.SugaredLogger) (bool, bool, *zap.SugaredLogger, string, error) {
	isGL := false
	event := req.Header.Get("X-Gitlab-Event")
	if event == "" {
		return false, false, logger, "no gitlab event", nil
	}

	// it is a GitLab event
	isGL = true

	setLoggerAndProceed := func(processEvent bool, reason string, err error) (bool, bool, *zap.SugaredLogger,
		string, error,
	) {
		logger = logger.With("provider", "gitlab", "event-id", req.Header.Get("X-Request-Id"))
		return isGL, processEvent, logger, reason, err
	}

	eventInt, err := gitlab.ParseWebhook(gitlab.EventType(event), []byte(payload))
	if err != nil {
		return setLoggerAndProceed(false, "", err)
	}
	_ = json.Unmarshal([]byte(payload), &eventInt)

	switch gitEvent := eventInt.(type) {
	case *gitlab.MergeEvent:
		// on a MR update, react only if OldRev is empty (no new commits pushed).
		// If OldRev is empty, it's a metadata-only update (e.g., label changes).
		if gitEvent.ObjectAttributes.Action == "update" && gitEvent.ObjectAttributes.OldRev == "" {
			if !hasOnlyLabelsChanged(gitEvent) {
				return setLoggerAndProceed(false, "this 'Merge Request' update event changes are not supported; cannot proceed", nil)
			}
		}

		if provider.Valid(gitEvent.ObjectAttributes.Action, []string{"open", "reopen", "update"}) {
			return setLoggerAndProceed(true, "", nil)
		}

		// on a MR Update only react when there is Oldrev set, since this means
		// there is a Push of commit in there
		if gitEvent.ObjectAttributes.Action == "update" && gitEvent.ObjectAttributes.OldRev != "" {
			return setLoggerAndProceed(true, "", nil)
		}
		if provider.Valid(gitEvent.ObjectAttributes.Action, []string{"open", "reopen", "close"}) {
			return setLoggerAndProceed(true, "", nil)
		}

		return setLoggerAndProceed(false, fmt.Sprintf("not a merge event we care about: \"%s\"", gitEvent.ObjectAttributes.Action), nil)
	case *gitlab.PushEvent, *gitlab.TagEvent:
		return setLoggerAndProceed(true, "", nil)
	case *gitlab.MergeCommentEvent:
		if gitEvent.MergeRequest.State == "opened" {
			return setLoggerAndProceed(true, "", nil)
		}
		return setLoggerAndProceed(false, "comments on closed merge requests is not supported", nil)
	case *gitlab.CommitCommentEvent:
		comment := gitEvent.ObjectAttributes.Note
		if gitEvent.ObjectAttributes.Action == gitlab.CommentEventActionCreate {
			if provider.IsTestRetestComment(comment) || provider.IsCancelComment(comment) {
				return setLoggerAndProceed(true, "", nil)
			}
			// truncate comment to make logs readable
			if len(comment) > 50 {
				comment = comment[:50] + "..."
			}
			return setLoggerAndProceed(false, fmt.Sprintf("gitlab: commit_comment: unsupported GitOps comment \"%s\" on pushed commits", comment), nil)
		}
		return setLoggerAndProceed(false, fmt.Sprintf("gitlab: commit_comment: unsupported action \"%s\" with comment \"%s\"", gitEvent.ObjectAttributes.Action, comment), nil)
	default:
		return setLoggerAndProceed(false, "", fmt.Errorf("gitlab: event \"%s\" is not supported", event))
	}
}

// hasOnlyLabelsChanged checks if the only change in the merge request is to its labels.
// This function ensures that other fields remain unchanged.
// Labels changes are treated when they're done standalone and not done with other changes
// for example, if a merge request is marked as draft and then the labels are changed, this is not a valid merge request update.
func hasOnlyLabelsChanged(gitEvent *gitlab.MergeEvent) bool {
	changes := gitEvent.Changes

	labelsChanged := isNewLabelAdded(changes.Labels.Previous, changes.Labels.Current)

	// Only Labels can change — everything else must be zero or nil
	onlyUpdatedAtOrLabels := labelsChanged &&
		!changes.Draft.Previous && !changes.Draft.Current &&
		changes.Assignees.Previous == nil && changes.Assignees.Current == nil &&
		changes.Reviewers.Previous == nil && changes.Reviewers.Current == nil &&
		changes.Description.Previous == "" && changes.Description.Current == "" &&
		changes.MergeStatus.Previous == "" && changes.MergeStatus.Current == "" &&
		changes.MilestoneID.Previous == 0 && changes.MilestoneID.Current == 0 &&
		changes.SourceBranch.Previous == "" && changes.SourceBranch.Current == "" &&
		changes.SourceProjectID.Previous == 0 && changes.SourceProjectID.Current == 0 &&
		changes.StateID.Previous == 0 && changes.StateID.Current == 0 &&
		changes.TargetBranch.Previous == "" && changes.TargetBranch.Current == "" &&
		changes.TargetProjectID.Previous == 0 && changes.TargetProjectID.Current == 0 &&
		changes.Title.Previous == "" && changes.Title.Current == ""

	return onlyUpdatedAtOrLabels
}

func isNewLabelAdded(previous, current []*gitlab.EventLabel) bool {
	return slices.ContainsFunc(current, func(label *gitlab.EventLabel) bool {
		if label == nil {
			return false
		}
		return !slices.ContainsFunc(previous, func(prev *gitlab.EventLabel) bool {
			return prev != nil && prev.Title == label.Title
		})
	})
}
