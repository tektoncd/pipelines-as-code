//go:build e2e

package test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGiteaResultsAnnotations(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipeline.yaml": "testdata/pipelinerun.yaml",
		},
		CheckForStatus: "success",
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	// assertions for checking results specific annotation in the PipelineRuns manifest here
	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)
	for _, pr := range prs.Items {
		annotations := pr.GetAnnotations()
		assert.Assert(t, annotations != nil, "Annotations should not be nil")

		val, exists := annotations[keys.PullRequest]
		if !exists {
			t.Fatalf("Annotation %s does not exist", keys.PullRequest)
		}

		pullRequestNumber, err := strconv.Atoi(val)
		assert.NilError(t, err)

		// Assert specific annotation
		resultAnnotation := kubeinteraction.ResultAnnotation{
			Repo:          topts.TargetNS,
			Commit:        topts.PullRequest.Head.Sha,
			EventType:     topts.TargetEvent,
			PullRequestID: pullRequestNumber,
		}
		expectedJSON, err := json.Marshal(resultAnnotation)
		assert.NilError(t, err)
		expectedResultAnnotation := string(expectedJSON)

		// an example of results annotation format
		// results.tekton.dev/recordSummaryAnnotations:{"repo":"pac-demo","commit":"62f8c8b7e4c3fc38cfbe7fcce2660e5b95de2d9a","eventType":"pull_request","pull_request-id":7}
		assert.Equal(t, annotations[keys.ResultsRecordSummary], expectedResultAnnotation, "Unexpected annotation value")
	}
}
