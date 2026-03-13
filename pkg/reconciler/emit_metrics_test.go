package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

// TestCountPipelineRun tests pipelinerun count metric.
func TestCountPipelineRun(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		tags        map[string]string
		wantErr     bool
	}{
		{
			name: "provider is GitHub App",
			annotations: map[string]string{
				keys.GitProvider:    "github",
				keys.EventType:      "pull_request",
				keys.InstallationID: "123",
			},
			tags: map[string]string{
				"provider":   "github-app",
				"event-type": "pull_request",
			},
			wantErr: false,
		},
		{
			name: "provider is GitHub Enterprise App",
			annotations: map[string]string{
				keys.GitProvider:    "github-enterprise",
				keys.EventType:      "pull_request",
				keys.InstallationID: "123",
			},
			tags: map[string]string{
				"provider":   "github-enterprise-app",
				"event-type": "pull_request",
			},
			wantErr: false,
		},
		{
			name: "provider is GitHub Webhook",
			annotations: map[string]string{
				keys.GitProvider: "github",
				keys.EventType:   "pull_request",
			},
			tags: map[string]string{
				"provider":   "github-webhook",
				"event-type": "pull_request",
			},
			wantErr: false,
		},
		{
			name: "provider is GitLab",
			annotations: map[string]string{
				keys.GitProvider: "gitlab",
				keys.EventType:   "push",
			},
			tags: map[string]string{
				"provider":   "gitlab-webhook",
				"event-type": "push",
			},
			wantErr: false,
		},
		{
			name: "provider is Forgejo",
			annotations: map[string]string{
				keys.GitProvider: "forgejo",
				keys.EventType:   "push",
			},
			tags: map[string]string{
				"provider":   "forgejo-webhook",
				"event-type": "push",
			},
			wantErr: false,
		},
		{
			name: "unsupported provider",
			annotations: map[string]string{
				keys.GitProvider: "unsupported",
				keys.EventType:   "push",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prmetrics.ResetRecorder()
			ctx := context.Background()
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(provider)
			m, err := prmetrics.NewRecorder()
			assert.NilError(t, err)
			r := &Reconciler{
				metrics: m,
			}
			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}

			if err = r.countPipelineRun(ctx, pr); (err != nil) != tt.wantErr {
				t.Errorf("countPipelineRun() error = %v, wantErr %v. error: %v", err != nil, tt.wantErr, err)
			}

			var rm metricdata.ResourceMetrics
			err = reader.Collect(ctx, &rm)
			assert.NilError(t, err, "error collecting metrics")

			if !tt.wantErr {
				assert.Equal(t, len(rm.ScopeMetrics), 1)
				assert.Equal(t, len(rm.ScopeMetrics[0].Metrics), 1)
				assert.Equal(t, rm.ScopeMetrics[0].Metrics[0].Name, "pipelines_as_code_pipelinerun_count")
				count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
				assert.Assert(t, ok)
				assert.Equal(t, count.DataPoints[0].Value, int64(1))
			}
		})
	}
}

// TestCalculatePipelineRunDuration tests pipelinerun duration metric.
func TestCalculatePipelineRunDuration(t *testing.T) {
	startTime := metav1.Now()
	tests := []struct {
		name           string
		annotations    map[string]string
		conditionType  apis.ConditionType
		status         corev1.ConditionStatus
		reason         string
		completionTime metav1.Time
		tags           map[string]string
	}{
		{
			name: "pipelinerun succeeded",
			annotations: map[string]string{
				keys.Repository: "pac-repo",
			},
			conditionType:  apis.ConditionSucceeded,
			status:         corev1.ConditionTrue,
			reason:         tektonv1.PipelineRunReasonSuccessful.String(),
			completionTime: metav1.NewTime(startTime.Add(time.Minute)),
			tags: map[string]string{
				"namespace":  "pac-ns",
				"reason":     tektonv1.PipelineRunReasonSuccessful.String(),
				"repository": "pac-repo",
				"status":     "success",
			},
		},
		{
			name: "pipelinerun completed",
			annotations: map[string]string{
				keys.Repository: "pac-repo",
			},
			conditionType:  apis.ConditionSucceeded,
			status:         corev1.ConditionTrue,
			reason:         tektonv1.PipelineRunReasonCompleted.String(),
			completionTime: metav1.NewTime(startTime.Add(time.Minute)),
			tags: map[string]string{
				"namespace":  "pac-ns",
				"reason":     tektonv1.PipelineRunReasonCompleted.String(),
				"repository": "pac-repo",
				"status":     "success",
			},
		},
		{
			name: "pipelinerun failed",
			annotations: map[string]string{
				keys.Repository: "pac-repo",
			},
			conditionType:  apis.ConditionSucceeded,
			status:         corev1.ConditionFalse,
			reason:         tektonv1.PipelineRunReasonFailed.String(),
			completionTime: metav1.NewTime(startTime.Add(2 * time.Minute)),
			tags: map[string]string{
				"namespace":  "pac-ns",
				"reason":     tektonv1.PipelineRunReasonFailed.String(),
				"repository": "pac-repo",
				"status":     "failed",
			},
		},
		{
			name: "pipelinerun cancelled",
			annotations: map[string]string{
				keys.Repository: "pac-repo",
			},
			conditionType:  apis.ConditionSucceeded,
			status:         corev1.ConditionFalse,
			reason:         tektonv1.PipelineRunReasonCancelled.String(),
			completionTime: metav1.NewTime(startTime.Add(2 * time.Second)),
			tags: map[string]string{
				"namespace":  "pac-ns",
				"reason":     tektonv1.PipelineRunReasonCancelled.String(),
				"repository": "pac-repo",
				"status":     "cancelled",
			},
		},
		{
			name: "pipelinerun timed out",
			annotations: map[string]string{
				keys.Repository: "pac-repo",
			},
			conditionType:  apis.ConditionSucceeded,
			status:         corev1.ConditionFalse,
			reason:         tektonv1.PipelineRunReasonTimedOut.String(),
			completionTime: metav1.NewTime(startTime.Add(10 * time.Minute)),
			tags: map[string]string{
				"namespace":  "pac-ns",
				"reason":     tektonv1.PipelineRunReasonTimedOut.String(),
				"repository": "pac-repo",
				"status":     "failed",
			},
		},
		{
			name: "pipelinerun failed due to couldn't get pipeline",
			annotations: map[string]string{
				keys.Repository: "pac-repo",
			},
			conditionType:  apis.ConditionSucceeded,
			status:         corev1.ConditionFalse,
			reason:         tektonv1.PipelineRunReasonCouldntGetPipeline.String(),
			completionTime: metav1.NewTime(startTime.Add(time.Second)),
			tags: map[string]string{
				"namespace":  "pac-ns",
				"reason":     tektonv1.PipelineRunReasonCouldntGetPipeline.String(),
				"repository": "pac-repo",
				"status":     "failed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prmetrics.ResetRecorder()
			ctx := context.Background()
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			otel.SetMeterProvider(provider)
			m, err := prmetrics.NewRecorder()
			assert.NilError(t, err)
			r := &Reconciler{
				metrics: m,
			}
			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "pac-ns",
					Annotations: tt.annotations,
				},
				Status: tektonv1.PipelineRunStatus{
					Status: duckv1.Status{Conditions: []apis.Condition{
						{
							Type:   tt.conditionType,
							Status: tt.status,
							Reason: tt.reason,
						},
					}},
					PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
						StartTime:      &startTime,
						CompletionTime: &tt.completionTime,
					},
				},
			}

			if err = r.calculatePRDuration(ctx, pr); err != nil {
				t.Errorf("calculatePRDuration() error = %v", err)
			}

			duration := tt.completionTime.Sub(startTime.Time)

			var rm metricdata.ResourceMetrics
			err = reader.Collect(ctx, &rm)
			assert.NilError(t, err, "error collecting metrics")

			assert.Equal(t, len(rm.ScopeMetrics), 1)
			assert.Equal(t, len(rm.ScopeMetrics[0].Metrics), 1)
			assert.Equal(t, rm.ScopeMetrics[0].Metrics[0].Name, "pipelines_as_code_pipelinerun_duration_seconds_sum")
			durationMetric, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[float64])
			assert.Assert(t, ok)
			assert.Equal(t, durationMetric.DataPoints[0].Value, duration.Seconds())
		})
	}
}

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

	prmetrics.ResetRecorder()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	m, err := prmetrics.NewRecorder()
	assert.NilError(t, err)
	r := &Reconciler{
		metrics: m,
	}

	err = r.metrics.EmitRunningPRsMetrics(ctx, plrs)
	assert.NilError(t, err)

	var rm metricdata.ResourceMetrics
	err = reader.Collect(ctx, &rm)
	assert.NilError(t, err, "error collecting metrics")

	assert.Equal(t, len(rm.ScopeMetrics), 1)
	assert.Equal(t, len(rm.ScopeMetrics[0].Metrics), 1)
	assert.Equal(t, rm.ScopeMetrics[0].Metrics[0].Name, "pipelines_as_code_running_pipelineruns_count")
	count, ok := rm.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
	assert.Assert(t, ok)
	assert.Equal(t, count.DataPoints[0].Value, int64(numberOfRunningPRs))
}
