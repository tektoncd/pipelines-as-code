//go:build e2e

package test

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGiteaTektonDirRecursive(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipelinerun.yaml":                "testdata/pipelinerun.yaml",
			".tekton/subdir/pipelinerun.yaml":         "testdata/pipelinerun.yaml",
			".tekton/subdir/subdir/pipelinerun.yaml":  "testdata/pipelinerun.yaml",
			".tekton/another-subdir/pipelinerun.yaml": "testdata/pipelinerun.yaml",
		},
		CheckForStatus:       "success",
		CheckForNumberStatus: 4,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 4, "should have 4 pipelineruns")
}
