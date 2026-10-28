package gitea

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/tektoncd/pipelines-as-code/pkg/params/triggertype"
	"github.com/tektoncd/pipelines-as-code/pkg/provider"
	"github.com/tektoncd/pipelines-as-code/pkg/provider/gitea/forgejostructs"
	"go.uber.org/zap"
)

var (
	pullRequestOpenSyncEvent = []string{"opened", "synchronize", "synchronized", "reopened"}
	pullRequestLabelUpdated  = "label_updated"
	pullRequestLabelClosed   = "closed"
)

// Detect processes event and detect if it is a gitea event, whether to process or reject it
// returns (if is a Gitea event, whether to process or reject, logger with event metadata,, error if any occurred).
func (v *Provider) Detect(req *http.Request, payload string, logger *zap.SugaredLogger) (bool, bool, *zap.SugaredLogger, string, error) {
	isGitea := false
	eventType := req.Header.Get("X-Gitea-Event-Type")
	if eventType == "" {
		return false, false, logger, "not a gitea event", nil
	}

	isGitea = true
	setLoggerAndProceed := func(processEvent bool, reason string, err error) (bool, bool, *zap.SugaredLogger,
		string, error,
	) {
		logger = logger.With("provider", "gitea", "event-id", req.Header.Get("X-Gitea-Delivery"))
		return isGitea, processEvent, logger, reason, err
	}

	eventInt, err := parseWebhook(whEventType(eventType), []byte(payload))
	if err != nil {
		return setLoggerAndProceed(false, "", err)
	}
	_ = json.Unmarshal([]byte(payload), &eventInt)
	eType, errReason := detectTriggerTypeFromPayload(eventType, eventInt)
	if eType != "" {
		return setLoggerAndProceed(true, "", nil)
	}

	return setLoggerAndProceed(false, errReason, nil)
}

// detectTriggerTypeFromPayload will detect the event type from the payload,
// filtering out the events that are not supported.
func detectTriggerTypeFromPayload(ghEventType string, eventInt any) (triggertype.Trigger, string) {
	switch event := eventInt.(type) {
	case *forgejostructs.PushPayload:
		if event.Pusher != nil {
			return triggertype.Push, ""
		}
		return "", "invalid payload: no pusher in event"
	case *forgejostructs.PullRequestPayload:
		if provider.Valid(string(event.Action), append(pullRequestOpenSyncEvent, pullRequestLabelUpdated, pullRequestLabelClosed)) {
			return triggertype.PullRequest, ""
		}
		return "", fmt.Sprintf("pull_request: unsupported action \"%s\"", event.Action)
	case *forgejostructs.IssueCommentPayload:
		if event.Action == "created" &&
			event.Issue.PullRequest != nil &&
			event.Issue.State == "open" {
			if provider.IsTestRetestComment(event.Comment.Body) {
				return triggertype.Retest, ""
			}
			if provider.IsOkToTestComment(event.Comment.Body) {
				return triggertype.OkToTest, ""
			}
			if provider.IsCancelComment(event.Comment.Body) {
				return triggertype.Cancel, ""
			}
			// this ignores the comment if it is not a PAC gitops comment and not return an error
			return triggertype.Comment, ""
		}
		return "", "skip: not a PAC gitops comment"
	}
	return "", fmt.Sprintf("gitea: event \"%v\" is not supported", ghEventType)
}
