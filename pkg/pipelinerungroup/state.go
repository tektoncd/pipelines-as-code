package pipelinerungroup

import (
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/random"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// Decorate stamps the group identity on a PipelineRun of the event.
func Decorate(pr *tektonv1.PipelineRun, groupID string) error {
	if groupID == "" {
		return fmt.Errorf("group id must not be empty")
	}
	if formatting.CleanValueKubernetes(groupID) != groupID {
		return fmt.Errorf("group id %q is not a valid label value", groupID)
	}
	if pr.GetLabels() == nil {
		pr.Labels = map[string]string{}
	}
	if pr.GetAnnotations() == nil {
		pr.Annotations = map[string]string{}
	}
	pr.Labels[keys.GroupID] = formatting.CleanValueKubernetes(groupID)
	pr.Annotations[keys.GroupID] = groupID
	return nil
}

// GroupIDOf returns the group identity of a run, empty when it has none.
func GroupIDOf(pr *tektonv1.PipelineRun) string {
	return pr.GetAnnotations()[keys.GroupID]
}

// GroupSelector lists every run of a group, whatever its state.
func GroupSelector(groupID string) string {
	return fmt.Sprintf("%s=%s", keys.GroupID, formatting.CleanValueKubernetes(groupID))
}

// NewGroupID generates a label-safe identifier for one event's group.
func NewGroupID() string {
	return random.AlphaString(10)
}
