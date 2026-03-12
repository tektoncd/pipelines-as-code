package pipelinerunmetrics

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

func TestCountRunningPRs(t *testing.T) {
	annotations := map[string]string{
		keys.GitProvider: "github",
		keys.EventType:   "pull_request",
		keys.Repository:  "pac-repo",
	}

	ctx := context.Background()
	var plrs []*tektonv1.PipelineRun
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "pac-ns",
			Annotations: annotations,
		},
		Status: tektonv1.PipelineRunStatus{
			Status: duckv1.Status{Conditions: []apis.Condition{
				{
					Type:   apis.ConditionReady,
					Status: corev1.ConditionTrue,
					Reason: tektonv1.PipelineRunReasonRunning.String(),
				},
			}},
		},
	}

	numberOfRunningPRs := 10
	for i := 0; i < numberOfRunningPRs; i++ {
		plrs = append(plrs, pr)
	}

	ResetRecorder()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	m, err := NewRecorder()
	assert.NilError(t, err)

	_, err = m.meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		return m.ObserveRunningPRsMetrics(o, plrs)
	}, m.runningPRCount)
	assert.NilError(t, err)

	var rm metricdata.ResourceMetrics
	err = reader.Collect(ctx, &rm)
	assert.NilError(t, err, "error collecting metrics")

	assert.Equal(t, len(rm.ScopeMetrics), 1)
	assert.Equal(t, len(rm.ScopeMetrics[0].Metrics), 1)
	assert.Equal(t, rm.ScopeMetrics[0].Metrics[0].Name, "pipelines_as_code_running_pipelineruns_count")
	count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Gauge[int64])
	assert.Assert(t, ok)
	assert.Equal(t, count.DataPoints[0].Value, int64(numberOfRunningPRs))
}
