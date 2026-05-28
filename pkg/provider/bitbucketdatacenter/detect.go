package bitbucketdatacenter

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter/types"
	"go.uber.org/zap"
)

// Detect processes event and detect if it is a bitbucket data center event, whether to process or reject it
// returns (if is a bitbucket data center event, whether to process or reject, error if any occurred).
func (v *Provider) Detect(req *http.Request, payload string, logger *zap.SugaredLogger) (bool, bool, *zap.SugaredLogger, string, error) {
	isBitDataCenter := false
	event := req.Header.Get("X-Event-Key")
	if event == "" {
		return false, false, logger, "", nil
	}

	eventPayload, err := parsePayloadType(event)
	if err != nil || eventPayload == nil {
		return false, false, logger, "", err
	}

	// it is a Bitbucket data center event
	isBitDataCenter = true

	setLoggerAndProceed := func(processEvent bool, reason string, err error) (bool, bool, *zap.SugaredLogger, string,
		error,
	) {
		logger = logger.With("provider", "bitbucket-datacenter", "event-id", req.Header.Get("X-Request-Id"))
		return isBitDataCenter, processEvent, logger, reason, err
	}

	_ = json.Unmarshal([]byte(payload), &eventPayload)

	switch e := eventPayload.(type) {
	case *types.PullRequestEvent:
		if provider.Valid(event, []string{"pr:from_ref_updated", "pr:opened", "pr:merged", "pr:modified", "pr:reviewer:approved"}) {
			return setLoggerAndProceed(true, "", nil)
		}
		if provider.Valid(event, []string{"pr:comment:added"}) {
			if provider.IsTestRetestComment(e.Comment.Text) {
				return setLoggerAndProceed(true, "", nil)
			}
			if provider.IsOkToTestComment(e.Comment.Text) {
				return setLoggerAndProceed(true, "", nil)
			}
			if provider.IsCancelComment(e.Comment.Text) {
				return setLoggerAndProceed(true, "", nil)
			}
		}
		return setLoggerAndProceed(false, fmt.Sprintf("not a recognized bitbucket event: \"%s\"", event), nil)

	case *types.PushRequestEvent:
		if provider.Valid(event, []string{"repo:refs_changed"}) {
			if e.Changes != nil {
				return setLoggerAndProceed(true, "", nil)
			}
		}
		return setLoggerAndProceed(false, fmt.Sprintf("not an event we support: \"%s\"", event), nil)

	default:
		return setLoggerAndProceed(false, "", fmt.Errorf("bitbucket-datacenter: event \"%s\" is not supported", event))
	}
}
