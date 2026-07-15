//go:build e2e

package test

import (
	"regexp"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
)

func TestGiteaCreateContainerConfigErrorSnippet(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun-missing-secret.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "failure",
	}
	_, cleanup := tgitea.TestPR(t, topts)
	defer cleanup()

	topts.Regexp = regexp.MustCompile(`CreateContainerConfigError.*missing-step-secret`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)
}
