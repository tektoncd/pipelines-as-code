package cel

import (
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
)

// eventFromGitLab parses GitLab webhook payload for CEL evaluation.
func eventFromGitLab(bodyBytes []byte, headers map[string]string) (*info.Event, error) {
	return parseWebhookForCEL(bodyBytes, headers, &GitLabParser{})
}
