package cel

import (
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
)

// eventFromBitbucketDataCenter parses Bitbucket Data Center webhook payload for CEL evaluation.
func eventFromBitbucketDataCenter(bodyBytes []byte, headers map[string]string) (*info.Event, error) {
	return parseWebhookForCEL(bodyBytes, headers, &BitbucketDataCenterParser{})
}
