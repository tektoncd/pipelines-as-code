package tracing

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
)

func TestAttributeKeyComposition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"application", string(DeliveryApplicationKey), AttrNamespace + ".application"},
		{"component", string(DeliveryComponentKey), AttrNamespace + ".component"},
		{"result message", string(DeliveryResultMessageKey), AttrNamespace + ".result_message"},
		{"pipelinerun uid", string(DeliveryPipelineRunUIDKey), AttrNamespace + ".pipelinerun_uid"},
		{"namespace bare", string(NamespaceKey), "namespace"},
		{"pipelinerun bare", string(PipelineRunKey), "pipelinerun"},
		{"pac event type", string(PACEventTypeKey), pipelinesascode.GroupName + ".event_type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.got, tt.want)
		})
	}
}

func TestResultEnum(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status corev1.ConditionStatus
		reason string
		want   string
	}{
		{
			name:   "successful",
			status: corev1.ConditionTrue,
			reason: tektonv1.PipelineRunReasonSuccessful.String(),
			want:   "success",
		},
		{
			name:   "completed with skipped tasks",
			status: corev1.ConditionTrue,
			reason: tektonv1.PipelineRunReasonCompleted.String(),
			want:   "success",
		},
		{
			name:   "failed",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonFailed.String(),
			want:   "failure",
		},
		{
			name:   "timed out",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonTimedOut.String(),
			want:   "timeout",
		},
		{
			name:   "cancelled",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonCancelled.String(),
			want:   "cancellation",
		},
		{
			name:   "cancelled running finally",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonCancelledRunningFinally.String(),
			want:   "cancellation",
		},
		{
			name:   "validation failed",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonFailedValidation.String(),
			want:   "error",
		},
		{
			name:   "couldnt get pipeline",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonCouldntGetPipeline.String(),
			want:   "error",
		},
		{
			name:   "couldnt get task",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonCouldntGetTask.String(),
			want:   "error",
		},
		{
			name:   "invalid graph",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonInvalidGraph.String(),
			want:   "error",
		},
		{
			name:   "parameter missing",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonParameterMissing.String(),
			want:   "error",
		},
		{
			name:   "parameter type mismatch",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonParameterTypeMismatch.String(),
			want:   "error",
		},
		{
			name:   "invalid workspace binding",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonInvalidWorkspaceBinding.String(),
			want:   "error",
		},
		{
			name:   "create run failed",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonCreateRunFailed.String(),
			want:   "error",
		},
		{
			name:   "resource verification failed",
			status: corev1.ConditionFalse,
			reason: tektonv1.PipelineRunReasonResourceVerificationFailed.String(),
			want:   "error",
		},
		{
			name:   "future reason from upstream",
			status: corev1.ConditionFalse,
			reason: "SomeFutureReasonNotYetMapped",
			want:   "error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cond := &apis.Condition{Type: apis.ConditionSucceeded, Status: tt.status, Reason: tt.reason}
			got := ResultEnum(cond)
			assert.Equal(t, got.Value.AsString(), tt.want)
		})
	}
}

func TestTruncateResultMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "short stays untouched", in: "step-build exited 1", want: "step-build exited 1"},
		{name: "exact limit stays untouched", in: strings.Repeat("a", MaxResultMessageLen), want: strings.Repeat("a", MaxResultMessageLen)},
		{
			name: "over limit is truncated with marker",
			in:   strings.Repeat("a", MaxResultMessageLen+50),
			want: strings.Repeat("a", MaxResultMessageLen-len("...[truncated]")) + "...[truncated]",
		},
		{
			// Multi-byte rune (é = 0xC3 0xA9) straddling the cut must not survive
			// as a partial sequence. The keep boundary is shifted back to the
			// previous valid rune, then padded with 1 byte by the suffix, so the
			// final length is MaxResultMessageLen - 1.
			name: "multi-byte rune at boundary is walked back to a valid boundary",
			in:   strings.Repeat("a", MaxResultMessageLen-len("...[truncated]")-1) + "é" + strings.Repeat("a", 50),
			want: strings.Repeat("a", MaxResultMessageLen-len("...[truncated]")-1) + "...[truncated]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := TruncateResultMessage(tt.in)
			assert.Equal(t, got, tt.want)
			assert.Assert(t, len(got) <= MaxResultMessageLen, "truncated length must not exceed limit")
			assert.Assert(t, utf8.ValidString(got), "truncated string must be valid UTF-8")
		})
	}
}
