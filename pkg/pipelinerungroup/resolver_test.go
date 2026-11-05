package pipelinerungroup

import (
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/apis"
)

// groupPR builds a created run of a group: a template with the group and
// state annotations a created PipelineRun carries.
func groupPR(name, uid, state string) *tektonv1.PipelineRun {
	pr := prTemplate(name, "", "")
	pr.Name = name + "-generated"
	pr.UID = types.UID(uid)
	if state != "" {
		pr.Annotations[keys.State] = state
		pr.Labels = map[string]string{keys.State: state}
	}
	pr.Annotations[keys.GroupID] = "group1"
	return pr
}

// finishedPR builds a done dependency of the group.
func finishedPR(name, uid string, status corev1.ConditionStatus, reason string) *tektonv1.PipelineRun {
	pr := groupPR(name, uid, kubeinteraction.StateCompleted)
	pr.Status.Conditions = []apis.Condition{{
		Type:   apis.ConditionSucceeded,
		Status: status,
		Reason: reason,
	}}
	return pr
}

// pendingFollowUp builds the follow-up a follow-up definition becomes at event time.
// consumedFollowUp is a follow-up of the "tests" definition moved to a
// terminal state.
func consumedFollowUp(name, state string) FollowUp {
	followUp := pendingFollowUp(name, []string{"tests"})
	followUp.State = state
	return followUp
}

func pendingFollowUp(name string, deps []string) FollowUp {
	return FollowUp{
		ID:    name + "-group1",
		Name:  name,
		Deps:  deps,
		State: StatePending,
	}
}

// onRun pairs a pending follow-up with the run it is stored on, like the
// follow-ups stamped at event time on the dependency runs of the group.
func onRun(followUp FollowUp, run *tektonv1.PipelineRun) []FollowUpOnRun {
	return []FollowUpOnRun{{FollowUp: followUp, Run: run}}
}

func TestResolveFollowUp(t *testing.T) {
	tests := []struct {
		name         string
		followUp     FollowUp
		group        []*tektonv1.PipelineRun
		collected    []FollowUpOnRun
		expectedWait bool
		expectedRun  string
		wantErr      string
	}{
		{
			name:     "all dependencies done makes the follow-up ready",
			followUp: pendingFollowUp("deploy", []string{"build"}),
			group: []*tektonv1.PipelineRun{
				finishedPR("build", "uid-build", corev1.ConditionTrue, "Succeeded"),
			},
			expectedRun: "Succeeded",
		},
		{
			name:     "a running dependency keeps the follow-up waiting",
			followUp: pendingFollowUp("deploy", []string{"build"}),
			group: []*tektonv1.PipelineRun{
				groupPR("build", "uid-build", kubeinteraction.StateStarted),
			},
			expectedWait: true,
		},
		{
			name:     "a queued dependency keeps the follow-up waiting",
			followUp: pendingFollowUp("deploy", []string{"build"}),
			group: []*tektonv1.PipelineRun{
				groupPR("build", "uid-build", kubeinteraction.StateQueued),
			},
			expectedWait: true,
		},
		{
			name:     "a chained dependency that is still a pending follow-up keeps the follow-up waiting",
			followUp: pendingFollowUp("deploy", []string{"tests"}),
			group: []*tektonv1.PipelineRun{
				finishedPR("build", "uid-build", corev1.ConditionTrue, "Succeeded"),
			},
			collected: onRun(pendingFollowUp("tests", []string{"build"}),
				groupPR("build", "uid-build", kubeinteraction.StateCompleted)),
			expectedWait: true,
		},
		{
			name:     "a missing dependency surfaces as Error for the condition to decide",
			followUp: pendingFollowUp("deploy", []string{"build"}),
			group:    []*tektonv1.PipelineRun{},
			// expectedRun asserts the state the CEL condition will see
			expectedRun: "Error",
		},
		{
			name:     "a dependency follow-up cancelled while waiting surfaces as Cancelled",
			followUp: pendingFollowUp("deploy", []string{"build"}),
			group:    []*tektonv1.PipelineRun{},
			collected: onRun(consumedFollowUp("build", StateCancelled),
				groupPR("tests", "uid-tests", kubeinteraction.StateCompleted)),
			expectedRun: "Cancelled",
		},
		{
			name:     "a dependency follow-up that was skipped surfaces as Error",
			followUp: pendingFollowUp("deploy", []string{"build"}),
			group:    []*tektonv1.PipelineRun{},
			collected: onRun(consumedFollowUp("build", StateSkipped),
				groupPR("tests", "uid-tests", kubeinteraction.StateCompleted)),
			expectedRun: "Error",
		},
		{
			name:     "a dependency follow-up whose run could not start surfaces as Error",
			followUp: pendingFollowUp("deploy", []string{"build"}),
			group:    []*tektonv1.PipelineRun{},
			collected: onRun(consumedFollowUp("build", StateFailed),
				groupPR("tests", "uid-tests", kubeinteraction.StateCompleted)),
			expectedRun: "Error",
		},
		{
			name:     "a failed dependency is ready with the failure state",
			followUp: pendingFollowUp("deploy", []string{"build"}),
			group: []*tektonv1.PipelineRun{
				finishedPR("build", "uid-build", corev1.ConditionFalse, "Failed"),
			},
			expectedRun: "Failed",
		},
		{
			name:     "a dependency whose cancel is still being processed keeps the follow-up waiting",
			followUp: pendingFollowUp("deploy", []string{"build"}),
			group: []*tektonv1.PipelineRun{
				func() *tektonv1.PipelineRun {
					pr := groupPR("build", "uid-build", kubeinteraction.StateStarted)
					pr.Spec.Status = tektonv1.PipelineRunSpecStatusCancelled
					return pr
				}(),
			},
			expectedWait: true,
		},
		{
			name:     "a follow-up without dependencies is an error",
			followUp: pendingFollowUp("deploy", nil),
			group:    []*tektonv1.PipelineRun{},
			wantErr:  "has no dependencies",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := ResolveFollowUp(tt.followUp, tt.group, tt.collected)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.expectedWait, decision.Waiting)
			if tt.expectedRun != "" {
				assert.Assert(t, !decision.Waiting, "expected the follow-up to be ready")
				assert.Equal(t, tt.expectedRun, decision.Runs["build"].Status)
			}
		})
	}
}

func TestSummarizeStatusesEveryDependency(t *testing.T) {
	followUp := pendingFollowUp("deploy", []string{"build", "tests"})
	group := []*tektonv1.PipelineRun{
		finishedPR("build", "uid-build", corev1.ConditionTrue, "Succeeded"),
		finishedPR("tests", "uid-tests", corev1.ConditionFalse, "Failed"),
	}
	decision, err := ResolveFollowUp(followUp, group, nil)
	assert.NilError(t, err)
	assert.Assert(t, !decision.Waiting)
	summary := SummarizeStatuses(followUp.Deps, decision.Runs)
	assert.Assert(t, strings.Contains(summary, "build=Succeeded, tests=Failed"),
		"summary %q does not summarize the dependencies", summary)
}

func TestIsDoneRun(t *testing.T) {
	tests := []struct {
		name     string
		pr       *tektonv1.PipelineRun
		expected bool
	}{
		{
			name:     "state completed",
			pr:       groupPR("build", "uid", kubeinteraction.StateCompleted),
			expected: true,
		},
		{
			name:     "state failed",
			pr:       groupPR("build", "uid", kubeinteraction.StateFailed),
			expected: true,
		},
		{
			name:     "state started",
			pr:       groupPR("build", "uid", kubeinteraction.StateStarted),
			expected: false,
		},
		{
			name:     "tekton condition done without a pac state",
			pr:       donePR("build", corev1.ConditionFalse, string(tektonv1.PipelineRunReasonSuccessful)),
			expected: true,
		},
		{
			name: "a cancel request still being processed is not done",
			pr: func() *tektonv1.PipelineRun {
				pr := groupPR("build", "uid", kubeinteraction.StateStarted)
				pr.Spec.Status = tektonv1.PipelineRunSpecStatusCancelled
				pr.Status.Conditions = []apis.Condition{{Type: apis.ConditionSucceeded, Status: corev1.ConditionUnknown}}
				return pr
			}(),
			expected: false,
		},
		{
			name:     "a cancelled run is done once tekton sets its condition",
			pr:       donePR("build", corev1.ConditionFalse, string(tektonv1.PipelineRunReasonCancelled)),
			expected: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsDoneRun(tt.pr))
		})
	}
}
