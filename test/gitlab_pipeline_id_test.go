//go:build e2e

package test

import (
	"strconv"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitlab "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitlab"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGitlabPipelineIDAnnotation(t *testing.T) {
	topts := &tgitlab.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipelinerun.yaml": "testdata/pipelinerun.yaml",
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	sopt := twait.SuccessOpt{
		Title:           "Committing files from test on " + topts.TargetRefName,
		OnEvent:         "Merge Request",
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 1,
		SHA:             topts.SHA,
	}
	twait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prs.Items) >= 1, "Expected at least one PipelineRun")

	for _, pr := range prs.Items {
		pipelineID, ok := pr.Annotations[keys.GitLabPipelineID]
		assert.Assert(t, ok, "PipelineRun %s missing %s annotation", pr.Name, keys.GitLabPipelineID)
		assert.Assert(t, pipelineID != "", "PipelineRun %s has empty %s annotation", pr.Name, keys.GitLabPipelineID)
		pid, err := strconv.ParseInt(pipelineID, 10, 64)
		assert.NilError(t, err, "PipelineRun %s has non-numeric %s annotation: %s", pr.Name, keys.GitLabPipelineID, pipelineID)
		assert.Assert(t, pid > 0, "PipelineRun %s has invalid %s value: %d", pr.Name, keys.GitLabPipelineID, pid)
		t.Logf("PipelineRun %s has %s: %s", pr.Name, keys.GitLabPipelineID, pipelineID)
	}
}
