package cel

import (
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
)

// eventFromGitHub parses GitHub webhook payload for CEL evaluation.
func eventFromGitHub(bodyBytes []byte, headers map[string]string) (*info.Event, error) {
	return parseWebhookForCEL(bodyBytes, headers, &GitHubParser{})
}

// eventFromGitHubWithToken parses GitHub webhook payload for CEL evaluation with API token enrichment.
func eventFromGitHubWithToken(bodyBytes []byte, headers map[string]string, token string) (*info.Event, error) {
	return parseWebhookForCEL(bodyBytes, headers, &GitHubParserWithToken{Token: token})
}
