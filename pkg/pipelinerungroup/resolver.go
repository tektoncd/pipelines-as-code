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
	// Runs holds the dependency states, for the condition and the params
	// injection at creation.
	Runs map[string]RunState
}

// ResolveFollowUp decides whether a pending follow-up is still waiting on its
// dependencies or ready for the creation of its run.
func ResolveFollowUp(followUp FollowUp, group []*tektonv1.PipelineRun,
	collected []FollowUpOnRun,
) (Decision, error) {
	if len(followUp.Deps) == 0 {
		return Decision{}, fmt.Errorf("follow-up %s has no dependencies", followUp.Name)
	}

	pendingFollowUp := map[string]bool{}
	consumedState := map[string]string{}
	for _, fu := range collected {
		if fu.FollowUp.Pending() {
			pendingFollowUp[fu.FollowUp.Name] = true
		} else {
			consumedState[fu.FollowUp.Name] = fu.FollowUp.State
		}
	}

	byDefinition := map[string]*tektonv1.PipelineRun{}
	for _, run := range group {
		byDefinition[DefinitionName(run)] = run
	}

	states := map[string]RunState{}
	for _, dep := range followUp.Deps {
		depRun := byDefinition[dep]
		switch {
		case depRun != nil && IsDoneRun(depRun):
			states[dep] = RunStateFromPR(depRun)
		case depRun != nil, pendingFollowUp[dep]:
			return Decision{Waiting: true}, nil
		case consumedState[dep] == StateCancelled:
			// a follow-up cancelled while it waited surfaces like a
			// cancelled run
			states[dep] = cancelledRunState()
		default:
			// no run and no pending follow-up: skipped, failed to start or
			// pruned; Error so a condition like "all succeeded" goes false
			// instead of the follow-up being stuck
			states[dep] = missingRunState()
		}
	}

	return Decision{Runs: states}, nil
}

// IsDoneRun reports whether a group member has finished, from PAC's own state
// or from the Tekton condition. A cancel request in the spec is not enough:
// the run is done once Tekton sets its terminal condition.
func IsDoneRun(pr *tektonv1.PipelineRun) bool {
	state := pr.GetAnnotations()[keys.State]
	if state == kubeinteraction.StateCompleted || state == kubeinteraction.StateFailed {
		return true
	}
	return pr.IsDone()
}

// SummarizeStatuses renders the dependency states for a skip reason, like
// "build=Succeeded, tests=Failed".
func SummarizeStatuses(deps []string, states map[string]RunState) string {
	parts := make([]string, 0, len(deps))
	for _, dep := range deps {
		state := states[dep]
		parts = append(parts, fmt.Sprintf("%s=%s", dep, state.Status))
	}
	return strings.Join(parts, ", ")
}
