package bitbucketcloud

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	"go.uber.org/zap"
)

var (
	pullRequestsClosed         = []string{"pullrequest:closed", "pullrequest:fulfilled", "pullrequest:rejected"}
	pullRequestsCreated        = []string{"pullrequest:created", "pullrequest:updated"}
	pullRequestsCommentCreated = []string{"pullrequest:comment_created"}
	pushRepo                   = []string{"repo:push"}
	PullRequestAllEvents       = append(append(append(append([]string{}, pullRequestsCreated...), pullRequestsCommentCreated...), pullRequestsClosed...), pushRepo...)
)

func (v *Provider) Detect(req *http.Request, payload string, logger *zap.SugaredLogger) (bool, bool, *zap.SugaredLogger, string, error) {
	isBitCloud := false
	reqHeader := req.Header
	event := reqHeader.Get("X-Event-Key")
	if event == "" {
		return false, false, logger, "", nil
	}

	eventInt, err := parsePayloadType(event, payload)
	if err != nil || eventInt == nil {
		logger.Error("skip processing event", zap.String("event", event), zap.Error(err))
		return false, false, logger, "", err
	}

	// it is a Bitbucket cloud event
	isBitCloud = true

	setLoggerAndProceed := func(processEvent bool, reason string, err error) (bool, bool, *zap.SugaredLogger,
		string, error,
	) {
		logger = logger.With("provider", "bitbucket-cloud", "event-id", reqHeader.Get("X-Request-UUID"))
		return isBitCloud, processEvent, logger, reason, err
	}

	_ = json.Unmarshal([]byte(payload), &eventInt)

	switch e := eventInt.(type) {
	case *types.PullRequestEvent:
		if provider.Valid(event, pullRequestsClosed) {
			return setLoggerAndProceed(true, "", nil)
		}

		if provider.Valid(event, pullRequestsCreated) {
			return setLoggerAndProceed(true, "", nil)
		}

		if provider.Valid(event, pullRequestsCommentCreated) {
			if provider.IsTestRetestComment(e.Comment.Content.Raw) {
				return setLoggerAndProceed(true, "", nil)
			}
			if provider.IsOkToTestComment(e.Comment.Content.Raw) {
				return setLoggerAndProceed(true, "", nil)
			}
			if provider.IsCancelComment(e.Comment.Content.Raw) {
				return setLoggerAndProceed(true, "", nil)
			}
		}
		return setLoggerAndProceed(false, fmt.Sprintf("not a valid gitops comment: \"%s\"", event), nil)

	case *types.PushRequestEvent:
		if provider.Valid(event, pushRepo) {
			if e.Push.Changes != nil {
				return setLoggerAndProceed(true, "", nil)
			}
		}
		return setLoggerAndProceed(false, fmt.Sprintf("invalid push event: \"%s\"", event), nil)

	default:
		return setLoggerAndProceed(false, "", fmt.Errorf("bitbucket-cloud: event \"%s\" is not supported", event))
	}
}
