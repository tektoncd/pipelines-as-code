package cel

import (
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
)

// eventFromBitbucketCloud parses Bitbucket Cloud webhook payload for CEL evaluation.
func eventFromBitbucketCloud(bodyBytes []byte, headers map[string]string) (*info.Event, error) {
	return parseWebhookForCEL(bodyBytes, headers, &BitbucketCloudParser{})
}
