package pipelinerungroup

import (
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// Decision is what the watcher should do with a pending follow-up.
type Decision struct {
	// Waiting is true when at least one dependency has not finished yet.
	Waiting bool
	// Runs holds the dependency states.
	Runs map[string]RunState
}

// ResolveFollowUp decides whether a pending follow-up is still waiting on its
// dependencies or ready for the creation of its run.
func ResolveFollowUp(followUp FollowUp, group []*tektonv1.PipelineRun) (Decision, error) {
	if len(followUp.Deps) == 0 {
		return Decision{}, fmt.Errorf("follow-up %s has no dependencies", followUp.Name)
	}

	byDefinition := map[string]*tektonv1.PipelineRun{}
	for _, run := range group {
		byDefinition[DefinitionName(run)] = run
	}

	states := map[string]RunState{}
	for _, dep := range followUp.Deps {
		depRun := byDefinition[dep]
		switch {
		case depRun == nil:
			// pruned: Error, and the follow-up's CEL decides
			states[dep] = missingRunState()
		case !isDoneRun(depRun):
			return Decision{Waiting: true}, nil
		default:
			states[dep] = RunStateFromPR(depRun)
		}
	}
	return Decision{Runs: states}, nil
}

// isDoneRun reports whether a group member has finished.
func isDoneRun(pr *tektonv1.PipelineRun) bool {
	state := pr.GetAnnotations()[keys.State]
	if state == kubeinteraction.StateCompleted || state == kubeinteraction.StateFailed {
		return true
	}
	return pr.IsDone()
}

// SummarizeStatuses renders the dependency states, like "build=Succeeded".
func SummarizeStatuses(deps []string, states map[string]RunState) string {
	parts := make([]string, 0, len(deps))
	for _, dep := range deps {
		state := states[dep]
		parts = append(parts, fmt.Sprintf("%s=%s", dep, state.Status))
	}
	return strings.Join(parts, ", ")
}
