package pipelinerunmetrics

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	listers "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"k8s.io/apimachinery/pkg/labels"
	"knative.dev/pkg/logging"
)

// Recorder holds keys for metrics.
type Recorder struct {
	initialized                bool
	meter                      metric.Meter
	prCount                    metric.Int64Counter
	prDurationCount            metric.Float64Counter
	runningPRCount             metric.Int64Counter
	gitProviderAPIRequestCount metric.Int64Counter
	ReportingPeriod            time.Duration
}

var (
	Once           sync.Once
	R              *Recorder
	ErrRegistering error
)

// NewRecorder creates a new metrics recorder instance
// to log the PAC PipelineRun related metrics.
func NewRecorder() (*Recorder, error) {
	Once.Do(func() {
		R = &Recorder{
			initialized: true,

			// Default to 30s intervals.
			ReportingPeriod: 30 * time.Second,
		}

		R.meter = otel.Meter("pipelines-as-code")
		R.prCount, ErrRegistering = R.meter.Int64Counter("pipelines_as_code_pipelinerun_count", metric.WithDescription("number of pipelineruns by pipelines as code"))
		if ErrRegistering != nil {
			return
		}

		R.prDurationCount, ErrRegistering = R.meter.Float64Counter("pipelines_as_code_pipelinerun_duration_seconds_sum", metric.WithDescription("number of seconds all pipelineruns completed in by pipelines as code"))
		if ErrRegistering != nil {
			return
		}

		R.runningPRCount, ErrRegistering = R.meter.Int64Counter("pipelines_as_code_running_pipelineruns_count", metric.WithDescription("number of running pipelineruns by pipelines as code"))
		if ErrRegistering != nil {
			return
		}

		R.gitProviderAPIRequestCount, ErrRegistering = R.meter.Int64Counter("pipelines_as_code_git_provider_api_request_count", metric.WithDescription("number of API requests from pipelines as code to git providers"))
		if ErrRegistering != nil {
			return
		}

	})

	return R, ErrRegistering
}

func (r Recorder) assertInitialized() error {
	if !r.initialized {
		return fmt.Errorf(
			"ignoring the metrics recording for pipelineruns, failed to initialize the metrics recorder")
	}
	return nil
}

// Count logs number of times a pipelinerun is ran for a provider.
func (r *Recorder) Count(ctx context.Context, provider, event, namespace, repository string) error {
	if err := r.assertInitialized(); err != nil {
		return err
	}

	attribs := []attribute.KeyValue{
		attribute.String("provider", provider),
		attribute.String("event", event),
		attribute.String("namespace", namespace),
		attribute.String("repository", repository),
	}

	r.prCount.Add(ctx, 1, metric.WithAttributes(attribs...))
	return nil
}

// CountPRDuration collects duration taken by a pipelinerun in seconds accumulate them in prDurationCount.
func (r *Recorder) CountPRDuration(ctx context.Context, namespace, repository, status, reason string, duration time.Duration) error {
	if err := r.assertInitialized(); err != nil {
		return err
	}

	attribs := []attribute.KeyValue{
		attribute.String("namespace", namespace),
		attribute.String("repository", repository),
		attribute.String("status", status),
		attribute.String("reason", reason),
	}

	r.prDurationCount.Add(ctx, duration.Seconds(), metric.WithAttributes(attribs...))
	return nil
}

// RunningPipelineRuns emits the number of running PipelineRuns for a repository and namespace.
func (r *Recorder) RunningPipelineRuns(ctx context.Context, namespace, repository string, runningPRs int) error {
	if err := r.assertInitialized(); err != nil {
		return err
	}

	attribs := []attribute.KeyValue{
		attribute.String("namespace", namespace),
		attribute.String("repository", repository),
	}

	r.runningPRCount.Add(ctx, int64(runningPRs), metric.WithAttributes(attribs...))
	return nil
}

func (r *Recorder) EmitRunningPRsMetrics(ctx context.Context, plrs []*tektonv1.PipelineRun) error {
	if len(plrs) == 0 {
		return nil
	}

	// bifurcate PipelineRuns based on their namespace and repository
	runningPRs := map[string]int{}
	completedPRsKeys := map[string]struct{}{}
	for _, pr := range plrs {
		// Check if PipelineRun has Repository annotation it means PR is created by PAC.
		if repository, ok := pr.GetAnnotations()[keys.Repository]; ok {
			key := fmt.Sprintf("%s/%s", pr.GetNamespace(), repository)
			// check if PipelineRun is running.
			if !pr.IsDone() {
				runningPRs[key]++
			} else {
				// add it in completed, and we don't want completed PipelineRuns count.
				completedPRsKeys[key] = struct{}{}
			}
		}
	}

	for k, v := range runningPRs {
		nsKeys := strings.Split(k, "/")
		if err := r.RunningPipelineRuns(ctx, nsKeys[0], nsKeys[1], v); err != nil {
			return err
		}
	}

	// report zero for the keys which aren't in runningPRs.
	for key := range completedPRsKeys {
		// if key isn't there in runningPRs then it should be reported 0
		// otherwise it was reported in previous loop.
		if _, ok := runningPRs[key]; !ok {
			nsKeys := strings.Split(key, "/")
			if err := r.RunningPipelineRuns(ctx, nsKeys[0], nsKeys[1], 0); err != nil {
				return err
			}
		}
	}

	return nil
}

// ReportRunningPipelineRuns reports running PipelineRuns on our configured ReportingPeriod
// until the context is cancelled.
func (r *Recorder) ReportRunningPipelineRuns(ctx context.Context, lister listers.PipelineRunLister) {
	logger := logging.FromContext(ctx)

	for {
		delay := time.NewTimer(r.ReportingPeriod)
		select {
		case <-ctx.Done():
			// When the context is cancelled, stop reporting.
			if !delay.Stop() {
				<-delay.C
			}
			return

		case <-delay.C:
			plrs, err := lister.List(labels.Everything())
			if err != nil {
				logger.Warnf("Failed to list PipelineRuns : %v", err)
				continue
			}
			// Every 30s surface a metric for the number of running pipelines.
			if err := r.EmitRunningPRsMetrics(ctx, plrs); err != nil {
				logger.Warnf("Failed to log the metrics : %v", err)
			}
		}
	}
}

func (r *Recorder) ReportGitProviderAPIUsage(provider, event, namespace, repository string) error {
	if err := r.assertInitialized(); err != nil {
		return err
	}

	attribs := []attribute.KeyValue{
		attribute.String("provider", provider),
		attribute.String("event", event),
		attribute.String("namespace", namespace),
		attribute.String("repository", repository),
	}

	r.gitProviderAPIRequestCount.Add(context.Background(), 1, metric.WithAttributes(attribs...))
	return nil
}

func ResetRecorder() {
	Once = sync.Once{}
	R = nil
	ErrRegistering = nil
}
