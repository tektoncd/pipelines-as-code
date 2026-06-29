//go:build e2e

// Copyright 2026 The Tekton Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	pacNamespace          = "pipelines-as-code"
	pacMetricsPort        = "9090"
	pacControllerSelector = "app.kubernetes.io/name=controller,app.kubernetes.io/part-of=pipelines-as-code"
	pacWatcherSelector    = "app.kubernetes.io/name=watcher,app.kubernetes.io/part-of=pipelines-as-code"
)

// pacKubeClient builds a kubernetes client from the default kubeconfig.
func pacKubeClient(t *testing.T) kubernetes.Interface {
	t.Helper()
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		t.Fatalf("Failed to build kubeconfig: %v", err)
	}
	return kubernetes.NewForConfigOrDie(cfg)
}

// scrapePACPodMetrics scrapes /metrics from the first Running/Ready pod
// matching labelSelector via the Kubernetes API proxy. Returns an error
// so callers can retry on transient failures without aborting the test.
func scrapePACPodMetrics(ctx context.Context, kubeClient kubernetes.Interface, labelSelector string) (map[string]*dto.MetricFamily, error) {
	pods, err := kubeClient.CoreV1().Pods(pacNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, err
	}

	var podName string
	for _, pod := range pods.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		podReady := len(pod.Status.ContainerStatuses) > 0
		for _, cs := range pod.Status.ContainerStatuses {
			if !cs.Ready {
				podReady = false
				break
			}
		}
		if podReady {
			podName = pod.Name
			break
		}
	}
	if podName == "" {
		return nil, fmt.Errorf("no Running/Ready PAC pod found for selector %q in namespace %s", labelSelector, pacNamespace)
	}

	result := kubeClient.CoreV1().RESTClient().Get().
		Resource("pods").
		Name(podName + ":" + pacMetricsPort).
		Namespace(pacNamespace).
		SubResource("proxy").
		Suffix("metrics").
		Do(ctx)

	body, err := result.Raw()
	if err != nil {
		return nil, err
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	return families, nil
}

// waitForControllerMetrics polls the PAC controller pod until the named
// metric appears. Transient errors are logged and retried until timeout.
func waitForControllerMetrics(ctx context.Context, t *testing.T, kubeClient kubernetes.Interface, metricName string, timeout time.Duration) map[string]*dto.MetricFamily {
	t.Helper()
	return waitForPACPodMetric(ctx, t, kubeClient, pacControllerSelector, metricName, timeout)
}

// waitForWatcherMetrics polls the PAC watcher pod until the named metric
// appears. Transient errors are logged and retried until timeout.
func waitForWatcherMetrics(ctx context.Context, t *testing.T, kubeClient kubernetes.Interface, metricName string, timeout time.Duration) map[string]*dto.MetricFamily {
	t.Helper()
	return waitForPACPodMetric(ctx, t, kubeClient, pacWatcherSelector, metricName, timeout)
}

// waitForPACPodMetric is the shared polling implementation used by
// waitForControllerMetrics and waitForWatcherMetrics.
func waitForPACPodMetric(ctx context.Context, t *testing.T, kubeClient kubernetes.Interface, labelSelector, metricName string, timeout time.Duration) map[string]*dto.MetricFamily {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		families, err := scrapePACPodMetrics(ctx, kubeClient, labelSelector)
		if err == nil {
			if _, ok := families[metricName]; ok {
				return families
			}
		} else {
			t.Logf("Retrying metrics scrape (%s): %v", labelSelector, err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Timed out waiting for metric %q (selector=%s, waited %v): %v", metricName, labelSelector, timeout, ctx.Err())
			return nil
		case <-time.After(5 * time.Second):
		}
	}
}

// counterValue returns the sum of all counter values for the given metric name.
func counterValue(families map[string]*dto.MetricFamily, name string) float64 {
	fam, ok := families[name]
	if !ok {
		return 0
	}
	var total float64
	for _, m := range fam.GetMetric() {
		if c := m.GetCounter(); c != nil {
			total += c.GetValue()
		}
	}
	return total
}

// TestOthersOTelMetricsController verifies that the PAC controller pod exposes the
// expected OTel metric families after the OC→OTel migration (PR #2567):
//   - http_client_* and kn_k8s_client_* (knative k8s client OTel instrumentation)
//   - go_* runtime metrics
//   - PAC application metrics logged (appear only after first PipelineRun)
func TestOthersOTelMetricsController(t *testing.T) {
	ctx := context.Background()
	kubeClient := pacKubeClient(t)

	t.Log("Waiting for PAC controller metrics (http_client_request_duration_seconds)")
	families := waitForControllerMetrics(ctx, t, kubeClient, "http_client_request_duration_seconds", 2*time.Minute)
	t.Logf("Scraped %d metric families from PAC controller", len(families))

	tests := []struct {
		name   string
		prefix string
		errMsg string
	}{
		{
			name:   "http_client_prefix",
			prefix: "http_client_",
			errMsg: "Expected at least one http_client_* metric from knative k8s client instrumentation, found none",
		},
		{
			name:   "kn_k8s_client_prefix",
			prefix: "kn_k8s_client_",
			errMsg: "Expected at least one kn_k8s_client_* metric from knative k8s client instrumentation, found none",
		},
		{
			name:   "go_runtime_prefix",
			prefix: "go_",
			errMsg: "Expected standard go_* runtime metrics, found none",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name := range families {
				if strings.HasPrefix(name, tt.prefix) {
					return
				}
			}
			t.Error(tt.errMsg)
		})
	}
}

// TestOthersOTelMetricsWatcher verifies that the PAC watcher pod exposes the
// expected OTel metric families after the OC→OTel migration (PR #2567):
//   - kn_workqueue_* (knative reconciler workqueue)
//   - http_client_* and kn_k8s_client_* (knative k8s client OTel instrumentation)
//   - go_* runtime metrics
func TestOthersOTelMetricsWatcher(t *testing.T) {
	ctx := context.Background()
	kubeClient := pacKubeClient(t)

	t.Log("Waiting for PAC watcher metrics (go_goroutines)")
	families := waitForWatcherMetrics(ctx, t, kubeClient, "go_goroutines", 2*time.Minute)
	t.Logf("Scraped %d metric families from PAC watcher", len(families))

	tests := []struct {
		name   string
		prefix string
		errMsg string
	}{
		{
			name:   "kn_workqueue_prefix",
			prefix: "kn_workqueue_",
			errMsg: "Expected at least one kn_workqueue_* metric on the PAC watcher, found none",
		},
		{
			name:   "http_client_prefix",
			prefix: "http_client_",
			errMsg: "Expected at least one http_client_* metric on the PAC watcher, found none",
		},
		{
			name:   "kn_k8s_client_prefix",
			prefix: "kn_k8s_client_",
			errMsg: "Expected at least one kn_k8s_client_* metric on the PAC watcher, found none",
		},
		{
			name:   "go_runtime_prefix",
			prefix: "go_",
			errMsg: "Expected standard go_* runtime metrics on PAC watcher, found none",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for name := range families {
				if strings.HasPrefix(name, tt.prefix) {
					return
				}
			}
			t.Error(tt.errMsg)
		})
	}
}

// TestOthersOTelMetricsAfterPACRun triggers a real PAC PipelineRun via Gitea
// and asserts that pipelines_as_code_pipelinerun_count_total increments by
// exactly 1 on the watcher pod. The test fails if Gitea is not configured
// (TEST_GITEA_API_URL and related env vars are unset).
//
// PAC application metrics (pipelinerun_count, pipelinerun_duration, etc.) are
// emitted by the watcher's reconciler when it processes completed PipelineRuns,
// NOT by the controller which only handles incoming webhooks.
func TestOthersOTelMetricsAfterPACRun(t *testing.T) {
	ctx := context.Background()
	kubeClient := pacKubeClient(t)

	// Baseline: scrape the watcher before the PAC run to compute an exact delta.
	baseline, err := scrapePACPodMetrics(ctx, kubeClient, pacWatcherSelector)
	if err != nil {
		t.Skipf("PAC watcher metrics not reachable, skipping: %v", err)
	}
	baseCount := counterValue(baseline, "pipelines_as_code_pipelinerun_count_total")

	// TestPR sets up Gitea, creates a repo, pushes .tekton/pr.yaml, creates a
	// PR, waits for PAC to process it. It fails if Gitea is not configured
	// via TEST_GITEA_API_URL / TEST_GITEA_PASSWORD env vars.
	topts := &tgitea.TestOpts{
		Regexp:         successRegexp,
		TargetEvent:    triggertype.PullRequest.String(),
		YAMLFiles:      map[string]string{".tekton/pr.yaml": "testdata/always-good-pipelinerun.yaml"},
		CheckForStatus: "success",
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	// Assert exact delta == 1 on the watcher (one PipelineRun reconciled).
	after := waitForWatcherMetrics(ctx, t, kubeClient, "pipelines_as_code_pipelinerun_count_total", 2*time.Minute)
	delta := counterValue(after, "pipelines_as_code_pipelinerun_count_total") - baseCount
	if delta != 1 {
		t.Errorf("pipelinerun_count_total delta = %v, want exactly 1", delta)
	}
	t.Logf("pipelines_as_code_pipelinerun_count_total delta: %v", delta)

	// Assert all PAC application metrics are present on the watcher after a real run.
	appTests := []struct {
		name       string
		metricName string
		errMsg     string
	}{
		{
			name:       "pipelinerun_count_total",
			metricName: "pipelines_as_code_pipelinerun_count_total",
			errMsg:     "pipelines_as_code_pipelinerun_count_total not found on watcher after PAC run",
		},
		{
			name:       "pipelinerun_duration_seconds_sum_total",
			metricName: "pipelines_as_code_pipelinerun_duration_seconds_sum_total",
			errMsg:     "pipelines_as_code_pipelinerun_duration_seconds_sum_total not found on watcher after PAC run",
		},
		{
			name:       "git_provider_api_request_count_total",
			metricName: "pipelines_as_code_git_provider_api_request_count_total",
			errMsg:     "pipelines_as_code_git_provider_api_request_count_total not found on watcher after PAC run",
		},
	}
	for _, tt := range appTests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := after[tt.metricName]; !ok {
				t.Error(tt.errMsg)
			}
		})
	}
}
