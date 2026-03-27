package github

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"go.uber.org/zap"
)

var (
	pullRequestOpenSyncEvent = []string{"opened", "synchronize", "synchronized", "reopened", "ready_for_review"}
	pullRequestLabelEvent    = []string{"labeled"}
)

// Detect processes event and detect if it is a github event, whether to process or reject it
// returns (if is a GH event, whether to process or reject, error if any occurred).
func (v *Provider) Detect(req *http.Request, payload string, logger *zap.SugaredLogger) (bool, bool, *zap.SugaredLogger, string, error) {
	// gitea set x-github-event too, so skip it for the gitea driver
	if h := req.Header.Get("X-Gitea-Event-Type"); h != "" {
		return false, false, logger, "", nil
	}
	isGH := false
	eventType := req.Header.Get("X-Github-Event")
	if eventType == "" {
		return false, false, logger, "", nil
	}

	// it is a Github event
	isGH = true

	setLoggerAndProceed := func(processEvent bool, reason string, err error) (bool, bool, *zap.SugaredLogger,
		string, error,
	) {
		logger = logger.With("provider", "github", "event-id", req.Header.Get("X-GitHub-Delivery"))
		return isGH, processEvent, logger, reason, err
	}

	eventInt, err := github.ParseWebHook(eventType, []byte(payload))
	if err != nil {
		return setLoggerAndProceed(false, "", err)
	}

	_ = json.Unmarshal([]byte(payload), &eventInt)
	// at this moment we don't any info about repository CR so passing "/" as gitOpsCommentPrefix
	// if its valid event then that will be done in ParsePayload function ahead.
	eType, errReason := v.detectTriggerTypeFromPayload(eventType, eventInt, "/")
	if eType != "" {
		return setLoggerAndProceed(true, "", nil)
	}

	return setLoggerAndProceed(false, errReason, nil)
}

// detectTriggerTypeFromPayload will detect the event type from the payload,
// filtering out the events that are not supported.
// first arg will get the event type and the second one will get an error string explaining why it's not supported.
func (v *Provider) detectTriggerTypeFromPayload(ghEventType string, eventInt any, gitOpsCommentPrefix string) (triggertype.Trigger, string) {
	switch event := eventInt.(type) {
	case *github.PushEvent:
		if event.GetPusher() != nil {
			return triggertype.Push, ""
		}
		return "", "no pusher in payload"
	case *github.PullRequestEvent:
		if event.GetAction() == "closed" {
			return triggertype.PullRequestClosed, ""
		}

		if provider.Valid(event.GetAction(), pullRequestOpenSyncEvent) || provider.Valid(event.GetAction(), pullRequestLabelEvent) {
			return triggertype.PullRequest, ""
		}
		return "", fmt.Sprintf("pull_request: unsupported action \"%s\"", event.GetAction())
	case *github.IssueCommentEvent:
		if event.GetAction() == "created" &&
			event.GetIssue().IsPullRequest() &&
			event.GetIssue().GetState() == "open" {
			if opscomments.IsTestRetestComment(event.GetComment().GetBody(), gitOpsCommentPrefix) {
				return triggertype.Retest, ""
			}
			if opscomments.IsOkToTestComment(event.GetComment().GetBody(), gitOpsCommentPrefix) {
				return triggertype.OkToTest, ""
			}
			if opscomments.IsCancelComment(event.GetComment().GetBody(), gitOpsCommentPrefix) {
				return triggertype.Cancel, ""
			}
		}
		return triggertype.Comment, ""
	case *github.CheckSuiteEvent:
		if event.GetAction() == "rerequested" && event.GetCheckSuite() != nil {
			return triggertype.CheckSuiteRerequested, ""
		}
		return "", fmt.Sprintf("check_suite: unsupported action \"%s\"", event.GetAction())
	case *github.CheckRunEvent:
		if event.GetAction() == "rerequested" && event.GetCheckRun() != nil {
			return triggertype.CheckRunRerequested, ""
		}
		return "", fmt.Sprintf("check_run: unsupported action \"%s\"", event.GetAction())
	case *github.CommitCommentEvent:
		if event.GetAction() == "created" {
			if opscomments.IsTestRetestComment(event.GetComment().GetBody(), gitOpsCommentPrefix) {
				return triggertype.Retest, ""
			}
			if opscomments.IsCancelComment(event.GetComment().GetBody(), gitOpsCommentPrefix) {
				return triggertype.Cancel, ""
			}
			// Here, the `/ok-to-test` command is ignored because it is intended for pull requests.
			// For unauthorized users, it has no relevance to pushed commits, as only authorized users
			// are allowed to run CI on pushed commits. Therefore, the `ok-to-test` command holds no significance in this context.
			// However, it is left to be processed by the `on-comment` annotation rather than returning an error.
		}
		return triggertype.Comment, ""
	}
	return "", fmt.Sprintf("github: event \"%v\" is not supported", ghEventType)
}
