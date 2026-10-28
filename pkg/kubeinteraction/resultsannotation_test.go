package kubeinteraction

import (
	"encoding/json"
	"testing"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAddResultsAnnotation(t *testing.T) {
	testCases := []struct {
		name          string
		event         *info.Event
		expectedError error
	}{
		{
			name: "Valid Event",
			event: &info.Event{
				Repository:        "tektoncd/results",
				SHA:               "8789abb6",
				EventType:         "PR",
				PullRequestNumber: 123,
			},
			expectedError: nil,
		},
		{
			name:          "Empty Event",
			event:         &info.Event{},
			expectedError: nil,
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			// Prepare test data
			pipelineRun := &v1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			}
			err := AddResultsAnnotation(tt.event, pipelineRun)
			assert.NilError(t, err)

			// If no error, check annotations
			if err == nil {
				// Expected result annotation
				resultAnnotation := ResultAnnotation{
					Repo:          tt.event.Repository,
					Commit:        tt.event.SHA,
					EventType:     tt.event.EventType,
					PullRequestID: tt.event.PullRequestNumber,
				}
				expectedJSON, err := json.Marshal(resultAnnotation)
				if err != nil {
					t.Fatalf("Failed to marshal expected result annotation: %v", err)
				}
				expectedAnnotation := string(expectedJSON)

				// Check if annotation is added correctly
				assert.Assert(t, pipelineRun.Annotations[keys.ResultsRecordSummary] == expectedAnnotation, "Unexpected record summary annotation. Expected: %s, Got: %s", expectedAnnotation, pipelineRun.Annotations[keys.ResultsRecordSummary])
			}
		})
	}
}
