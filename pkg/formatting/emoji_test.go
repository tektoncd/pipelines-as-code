package formatting

import (
	"strings"
	"testing"

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
