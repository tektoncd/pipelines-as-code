package providermetrics

import (
	"context"
	"testing"

	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gotest.tools/v3/assert"
)

func TestRecordAPIUsage(t *testing.T) {
	type testCaseData struct {
		desc      string
		provider  string
		eventType string
		repo      *v1alpha1.Repository
		wantTags  map[string]string
	}

	testCases := []testCaseData{
		{
			desc:      "when repository is not nil",
			provider:  "github",
			eventType: "pull_request",
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-name",
					Namespace: "test-namespace",
				},
			},
			wantTags: map[string]string{"provider": "github", "event-type": "pull_request", "namespace": "test-namespace", "repository": "test-name"},
		},
		{
			desc:      "when repository is nil",
			provider:  "github",
			eventType: "pull_request",
			repo:      nil,
			wantTags:  map[string]string{"provider": "github", "event-type": "pull_request"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			ctx := context.Background()
			prmetrics.ResetRecorder()
			reader := sdkmetric.NewManualReader()
			metricProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(metricProvider)
			observer, _ := zapobserver.New(zap.InfoLevel)
			fakelogger := zap.New(observer).Sugar()

			RecordAPIUsage(fakelogger, testCase.provider, testCase.eventType, testCase.repo)

			var rm metricdata.ResourceMetrics
			err := reader.Collect(ctx, &rm)
			assert.NilError(t, err, "error collecting metrics")

			assert.Equal(t, len(rm.ScopeMetrics), 1)
			assert.Equal(t, len(rm.ScopeMetrics[0].Metrics), 1)
			assert.Equal(t, rm.ScopeMetrics[0].Metrics[0].Name, "pipelines_as_code_git_provider_api_request_count")
			count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
			assert.Assert(t, ok)
			assert.Equal(t, count.DataPoints[0].Value, int64(1))
		})
	}
}
