package cel

import (
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
)

// eventFromGitea parses Gitea webhook payload for CEL evaluation.
func eventFromGitea(bodyBytes []byte, headers map[string]string) (*info.Event, error) {
	return parseWebhookForCEL(bodyBytes, headers, &GiteaParser{})
}

// eventFromForgejo parses Forgejo webhook payload for CEL evaluation.
func eventFromForgejo(bodyBytes []byte, headers map[string]string) (*info.Event, error) {
	return parseWebhookForCEL(bodyBytes, headers, &ForgejoParser{})
}
