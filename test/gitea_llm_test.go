//go:build e2e

package test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/tektoncd/pipelines-as-code/test/pkg/gitea"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestGiteaLLM tests the LLM analysis feature with a failing PipelineRun.
// Note: The YAML file is a PipelineRun definition that will fail at runtime (exit 1).
// The LLM analysis is triggered only after the PipelineRun executes and its status
// condition becomes False (see pkg/reconciler/reconciler.go:243).
func TestGiteaLLM(t *testing.T) {
	llmRoleName := "make the failure a beautiful success"
	topts := &tgitea.TestOpts{
		ExpectEvents: false,
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			// This PipelineRun will fail at runtime due to 'exit 1', triggering LLM analysis
			".tekton/pr.yaml": "testdata/failures/pipelinerun-exit-1.yaml",
		},
		CreateSecret: []corev1.Secret{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "llm-secret",
				},
				Data: map[string][]byte{
					"token": []byte("sk-xxxx"),
				},
			},
		},
		Settings: &v1alpha1.Settings{
			AIAnalysis: &v1alpha1.AIAnalysisConfig{
				Enabled:  true,
				Provider: "openai",
				APIURL:   "http://nonoai.pipelines-as-code:8765/v1",
				TokenSecretRef: &v1alpha1.Secret{
					Name: "llm-secret",
					Key:  "token",
				},
				Roles: []v1alpha1.AnalysisRole{
					{
						Name:         llmRoleName,
						Prompt:       "what is the meaning of life",
						ContextItems: &v1alpha1.ContextConfig{},
						Output:       "pr-comment",
					},
				},
			},
		},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	topts.Regexp = regexp.MustCompile(fmt.Sprintf(".*%s.*", llmRoleName))
	tgitea.WaitForPullRequestCommentGoldenMatch(t, topts, "gitea-llm-comment.golden")
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -run TestGiteaLLM ."
// End:
