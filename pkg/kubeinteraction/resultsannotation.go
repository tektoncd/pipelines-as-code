package kubeinteraction

import (
	"encoding/json"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
)

type ResultAnnotation struct {
	Repo          string `json:"repo"`
	Commit        string `json:"commit"`
	EventType     string `json:"eventType"`
	PullRequestID int    `json:"pull_request-id,omitempty"`
}

// Add annotation to PipelineRuns produced by PaC for capturing additional
// data specific for TektonResults.
func AddResultsAnnotation(event *info.Event, pipelineRun *tektonv1.PipelineRun) error {
	resultAnnotation := ResultAnnotation{
		Repo:          event.Repository,
		Commit:        event.SHA,
		EventType:     event.EventType,
		PullRequestID: event.PullRequestNumber,
	}

	resAnnotationJSON, err := json.Marshal(resultAnnotation)
	if err != nil {
		return err
	}

	// append the result annotation
	pipelineRun.Annotations[keys.ResultsRecordSummary] = string(resAnnotationJSON)

	return nil
}
