package formatting

import (
	"strings"
	"testing"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	kv1 "knative.dev/pkg/apis/duck/v1"
)

func TestConditionEmoji(t *testing.T) {
	tests := []struct {
		name      string
		condition kv1.Conditions
		substr    string
	}{
		{
			name: "failed",
			condition: kv1.Conditions{
				{
					Status: corev1.ConditionFalse,
				},
			},
			substr: "Failed",
		},
		{
			name: "success",
			condition: kv1.Conditions{
				{
					Status: corev1.ConditionTrue,
				},
			},
			substr: "Succeeded",
		},
		{
			name: "Running",
			condition: kv1.Conditions{
				{
					Status: corev1.ConditionUnknown,
				},
			},
			substr: "Running",
		},
		{
			name:      "None",
			condition: kv1.Conditions{},
			substr:    nonAttributedStr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConditionEmoji(tt.condition)
			assert.Assert(t, strings.Contains(got, tt.substr))
		})
	}
}

func TestSkipEmoji(t *testing.T) {
	got := ConditionSad(
		kv1.Conditions{{Status: corev1.ConditionTrue}},
	)
	assert.Assert(t, !strings.Contains(got, "✅"))
}

func TestStepStateEmoji(t *testing.T) {
	tests := []struct {
		name  string
		state tektonv1.StepState
		want  string
	}{
		{
			name:  "succeeded",
			state: tektonv1.StepState{ContainerState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
			want:  "🟢 Succeeded",
		},
		{
			name:  "failed",
			state: tektonv1.StepState{ContainerState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 12}}},
			want:  "🔴 Failed",
		},
		{
			name:  "running",
			state: tektonv1.StepState{ContainerState: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			want:  "🟡 Running",
		},
		{
			name:  "pending",
			state: tektonv1.StepState{ContainerState: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}}},
			want:  "🔄 Pending",
		},
		{
			name:  "no state",
			state: tektonv1.StepState{},
			want:  nonAttributedStr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, StepStateEmoji(tt.state), tt.want)
		})
	}
}

func TestStepStateSad(t *testing.T) {
	got := StepStateSad(tektonv1.StepState{ContainerState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}})
	assert.Equal(t, got, "Succeeded")
}
