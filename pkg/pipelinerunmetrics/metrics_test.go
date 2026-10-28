package pipelinerunmetrics

import (
	"context"
	"errors"
	"testing"
	"time"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	fake "github.com/tektoncd/pipeline/pkg/client/clientset/versioned/fake"
	informers "github.com/tektoncd/pipeline/pkg/client/informers/externalversions"
	listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

func TestRecorderMetrics(t *testing.T) {
	ResetRecorder()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	m, err := NewRecorder()
	assert.NilError(t, err)

	ctx := context.Background()
	assert.NilError(t, m.Count(ctx, "github", "pull_request", "ns", "repo"))
	assert.NilError(t, m.CountPRDuration(ctx, "ns", "repo", "succeeded", "", 10))
	assert.NilError(t, m.ReportGitProviderAPIUsage("github", "pull_request", "ns", "repo"))

	var rm metricdata.ResourceMetrics
	err = reader.Collect(ctx, &rm)
	assert.NilError(t, err, "error collecting metrics")

	names := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, met := range sm.Metrics {
			names[met.Name] = true
		}
	}
	assert.Assert(t, names["pipelines_as_code_pipelinerun_count"])
	assert.Assert(t, names["pipelines_as_code_pipelinerun_duration_seconds_sum"])
	assert.Assert(t, names["pipelines_as_code_git_provider_api_request_count"])
}

func TestRecorderNotInitialized(t *testing.T) {
	ResetRecorder()
	r := &Recorder{}
	ctx := context.Background()

	assert.Assert(t, r.Count(ctx, "github", "pull_request", "ns", "repo") != nil)
	assert.Assert(t, r.CountPRDuration(ctx, "ns", "repo", "succeeded", "", 10) != nil)
	assert.Assert(t, r.RunningPipelineRuns(nil, "ns", "repo", 1) != nil)
	assert.Assert(t, r.ReportGitProviderAPIUsage("github", "pull_request", "ns", "repo") != nil)
}

func TestObserveRunningPRsMetricsReportsRunningAndCompleted(t *testing.T) {
	tests := []struct {
		name          string
		pipelineRuns  []*tektonv1.PipelineRun
		wantByRepo    map[string]int64
		wantDataPoint int
	}{
		{
			name: "reports running count and zero for completed only repository",
			pipelineRuns: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "ns",
						Annotations: map[string]string{keys.Repository: "running-repo"},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "ns",
						Annotations: map[string]string{keys.Repository: "completed-repo"},
					},
					Status: tektonv1.PipelineRunStatus{
						Status: duckv1.Status{Conditions: []apis.Condition{{
							Type:   apis.ConditionSucceeded,
							Status: corev1.ConditionTrue,
							Reason: tektonv1.PipelineRunReasonSuccessful.String(),
						}}},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace:   "ns",
						Annotations: map[string]string{},
					},
				},
			},
			wantByRepo: map[string]int64{
				"running-repo":   1,
				"completed-repo": 0,
			},
			wantDataPoint: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ResetRecorder()
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(provider)
			recorder, err := NewRecorder()
			assert.NilError(t, err)

			_, err = recorder.meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
				return recorder.ObserveRunningPRsMetrics(o, tt.pipelineRuns)
			}, recorder.runningPRCount)
			assert.NilError(t, err)

			var rm metricdata.ResourceMetrics
			err = reader.Collect(context.Background(), &rm)
			assert.NilError(t, err)

			gotByRepo := map[string]int64{}
			for _, sm := range rm.ScopeMetrics {
				for _, met := range sm.Metrics {
					if met.Name != "pipelines_as_code_running_pipelineruns_count" {
						continue
					}
					count, ok := met.Data.(metricdata.Gauge[int64])
					assert.Assert(t, ok)
					for _, point := range count.DataPoints {
						repository := ""
						for _, attr := range point.Attributes.ToSlice() {
							if string(attr.Key) == "repository" {
								repository = attr.Value.AsString()
							}
						}
						gotByRepo[repository] = point.Value
					}
				}
			}
			assert.Equal(t, tt.wantDataPoint, len(gotByRepo))
			for repository, want := range tt.wantByRepo {
				assert.Equal(t, want, gotByRepo[repository])
			}
		})
	}
}

func TestObserveRunningPRsMetricsEmpty(t *testing.T) {
	ResetRecorder()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	m, err := NewRecorder()
	assert.NilError(t, err)

	err = m.ObserveRunningPRsMetrics(nil, nil)
	assert.NilError(t, err)
}

func TestReportRunningPipelineRuns(t *testing.T) {
	ResetRecorder()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	m, err := NewRecorder()
	assert.NilError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	fakeClient := fake.NewSimpleClientset()
	factory := informers.NewSharedInformerFactory(fakeClient, 0)
	lister := factory.Tekton().V1().PipelineRuns().Lister()

	done := make(chan struct{})
	go func() {
		m.ReportRunningPipelineRuns(ctx, lister)
		close(done)
	}()
	cancel()
	<-done
}

func TestReportRunningPipelineRunsListError(t *testing.T) {
	tests := []struct {
		name   string
		lister listers.PipelineRunLister
	}{
		{
			name:   "returns callback list error",
			lister: failingPipelineRunLister{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ResetRecorder()
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(provider)
			recorder, err := NewRecorder()
			assert.NilError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				recorder.ReportRunningPipelineRuns(ctx, tt.lister)
				close(done)
			}()

			for i := 0; i < 100; i++ {
				var rm metricdata.ResourceMetrics
				err = reader.Collect(context.Background(), &rm)
				if err != nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			assert.ErrorContains(t, err, "list failed")
			cancel()
			<-done
		})
	}
}

type failingPipelineRunLister struct{}

func (failingPipelineRunLister) List(_ labels.Selector) ([]*tektonv1.PipelineRun, error) {
	return nil, errors.New("list failed")
}

func (failingPipelineRunLister) PipelineRuns(_ string) listers.PipelineRunNamespaceLister {
	return failingPipelineRunNamespaceLister{}
}

type failingPipelineRunNamespaceLister struct{}

func (failingPipelineRunNamespaceLister) List(_ labels.Selector) ([]*tektonv1.PipelineRun, error) {
	return nil, errors.New("list failed")
}

func (failingPipelineRunNamespaceLister) Get(_ string) (*tektonv1.PipelineRun, error) {
	return nil, errors.New("get failed")
}
