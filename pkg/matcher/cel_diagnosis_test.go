package matcher

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	prgroup "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerungroup"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

func TestFailedRunFollowUpIdentity(t *testing.T) {
	const condition = `event == "pull_request" && runs.tests.status == "Failed" && !(runs.tests.reason in ["Cancelled", "CancelledRunningFinally", "StoppedRunningFinally"])`
	tests := []struct {
		name   string
		status corev1.ConditionStatus
		reason string
		want   bool
	}{
		{name: "failed tests", status: corev1.ConditionFalse, reason: "Failed", want: true},
		{name: "timeout", status: corev1.ConditionFalse, reason: "PipelineRunTimeout", want: true},
		{name: "successful tests", status: corev1.ConditionTrue, reason: "Succeeded"},
		{name: "cancelled tests", status: corev1.ConditionFalse, reason: "Cancelled"},
		{name: "cancellation running finally", status: corev1.ConditionFalse, reason: "CancelledRunningFinally"},
		{name: "stopped tests", status: corev1.ConditionFalse, reason: "StoppedRunningFinally"},
		{name: "running tests", status: corev1.ConditionUnknown, reason: "Running"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: "tests-run", UID: "source-uid"}}
			pr.Status.Conditions = []apis.Condition{{Type: apis.ConditionSucceeded, Status: tt.status, Reason: tt.reason}}
			states := map[string]prgroup.RunState{"tests": prgroup.RunStateFromPR(pr)}
			event := info.NewEvent()
			event.TriggerTarget = triggertype.PullRequest
			event.EventType = "pull_request"
			ok, err := EvaluateFollowUp(context.Background(), condition, event, &testprovider.TestProviderImp{}, nil, nil, nil, prgroup.BuildRunsMap([]string{"tests"}, states))
			assert.NilError(t, err)
			assert.Equal(t, ok, tt.want)
			followUp := &tektonv1.PipelineRun{Spec: tektonv1.PipelineRunSpec{Params: tektonv1.Params{
				{Name: "source-run", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "{{ runs.tests.name }}"}},
				{Name: "source-uid", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "{{ runs.tests.uid }}"}},
			}}}
			assert.Equal(t, len(prgroup.InjectRunParams(followUp, states)), 0)
			assert.Equal(t, followUp.Spec.Params[0].Value.StringVal, "tests-run")
			assert.Equal(t, followUp.Spec.Params[1].Value.StringVal, "source-uid")
		})
	}
}
