package tracing

import (
	"unicode/utf8"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
)

const TracerName = "pipelines-as-code"

const AttrNamespace = "delivery.tekton.dev"

const MaxResultMessageLen = 1024

const truncatedSuffix = "...[truncated]"

// See docs/content/docs/operations/tracing.md for the full attribute schema.
var (
	NamespaceKey   = attribute.Key("namespace")
	PipelineRunKey = attribute.Key("pipelinerun")

	DeliveryApplicationKey    = attribute.Key(AttrNamespace + ".application")
	DeliveryComponentKey      = attribute.Key(AttrNamespace + ".component")
	DeliveryResultMessageKey  = attribute.Key(AttrNamespace + ".result_message")
	DeliveryPipelineRunUIDKey = attribute.Key(AttrNamespace + ".pipelinerun_uid")

	PACEventTypeKey = attribute.Key(pipelinesascode.GroupName + ".event_type")
)

// ResultEnum maps a PipelineRun Succeeded condition to a semconv
// cicd.pipeline.result enum value. Unknown reasons fall back to
// CICDPipelineResultError so that forward-compatible Tekton reasons are
// never silently dropped.
func ResultEnum(cond *apis.Condition) attribute.KeyValue {
	if cond.Status == corev1.ConditionTrue {
		return semconv.CICDPipelineResultSuccess
	}
	switch cond.Reason {
	case tektonv1.PipelineRunReasonCancelled.String(),
		tektonv1.PipelineRunReasonCancelledRunningFinally.String():
		return semconv.CICDPipelineResultCancellation
	case tektonv1.PipelineRunReasonTimedOut.String():
		return semconv.CICDPipelineResultTimeout
	case tektonv1.PipelineRunReasonFailed.String():
		return semconv.CICDPipelineResultFailure
	}
	return semconv.CICDPipelineResultError
}

// TruncateResultMessage caps msg at MaxResultMessageLen bytes, appending a
// marker when truncation occurs. The cut is walked back to a UTF-8 rune
// boundary so the result is always valid UTF-8.
func TruncateResultMessage(msg string) string {
	if len(msg) <= MaxResultMessageLen {
		return msg
	}
	keep := MaxResultMessageLen - len(truncatedSuffix)
	if keep < 0 {
		keep = 0
	}
	head := msg[:keep]
	for len(head) > 0 {
		r, size := utf8.DecodeLastRuneInString(head)
		if r != utf8.RuneError || size > 1 {
			break
		}
		head = head[:len(head)-size]
	}
	return head + truncatedSuffix
}
