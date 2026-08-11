package kubestuff

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	watcherNamespace  = "pipelines-as-code"
	watcherSelector   = "app=pipelines-as-code-watcher"
	watcherDeployment = "pipelines-as-code-watcher"
	watcherContainer  = "pac-watcher"
	watcherProbesPort = "probes"
)

// WatcherHealth is a snapshot of how many times the watcher has restarted.
type WatcherHealth struct {
	restarts map[string]int32
	runcnx   *params.Run
}

// SnapshotWatcherHealth records the current restart count of every watcher pod.
//
// A watcher that aborts is restarted by Kubernetes and silently rebuilds its
// queues, so a crash usually leaves the test passing and no assertion looking
// at it. Comparing restart counts across the test is what makes it visible.
func SnapshotWatcherHealth(ctx context.Context, t *testing.T, runcnx *params.Run) *WatcherHealth {
	t.Helper()
	h := &WatcherHealth{restarts: map[string]int32{}, runcnx: runcnx}
	pods := watcherPods(ctx, t, runcnx)
	for i := range pods {
		h.restarts[pods[i].GetName()] = totalRestarts(&pods[i])
	}
	return h
}

// Assert fails the test if any watcher pod restarted since the snapshot, or if
// its log contains a Go panic or an unrecoverable runtime error.
func (h *WatcherHealth) Assert(ctx context.Context, t *testing.T) {
	t.Helper()
	pods := watcherPods(ctx, t, h.runcnx)
	for i := range pods {
		pod := &pods[i]
		name := pod.GetName()
		now := totalRestarts(pod)
		before, seen := h.restarts[name]
		if !seen {
			// a pod that appeared during the test means the previous one went away
			assert.Assert(t, now == 0,
				"watcher pod %s appeared mid-test and has already restarted %d times:\n%s",
				name, now, watcherLog(ctx, h.runcnx, name))
			continue
		}
		assert.Assert(t, now == before,
			"watcher pod %s restarted %d time(s) during the test, it most likely crashed:\n%s",
			name, now-before, watcherLog(ctx, h.runcnx, name))
	}
	assertNoWatcherCrash(ctx, t, h.runcnx, pods)
}

func assertNoWatcherCrash(ctx context.Context, t *testing.T, runcnx *params.Run, pods []corev1.Pod) {
	t.Helper()
	for i := range pods {
		log := watcherLog(ctx, runcnx, pods[i].GetName())
		for _, needle := range []string{"panic: ", "fatal error: ", "WARNING: DATA RACE"} {
			assert.Assert(t, !strings.Contains(log, needle),
				"watcher pod %s log contains %q:\n%s", pods[i].GetName(), needle, log)
		}
	}
}

// BounceWatcher scales the watcher down to zero and back up again, waiting for
// it to come back ready. This is how a test forces the queue to be rebuilt from
// what is already in the cluster.
func BounceWatcher(ctx context.Context, t *testing.T, runcnx *params.Run) {
	t.Helper()
	ScaleDeployment(ctx, t, runcnx, 0, watcherDeployment, watcherNamespace)
	waitForWatcherReplicas(ctx, t, runcnx, 0)
	ScaleDeployment(ctx, t, runcnx, 1, watcherDeployment, watcherNamespace)
	waitForWatcherReplicas(ctx, t, runcnx, 1)
}

// QueueSnapshot asks the watcher what it currently believes about its queues.
//
// The endpoint answers 503 while the reconciler holds the queue lock, since it
// gives up rather than block the thing it is reporting on. That is expected
// under load, so retry for a bit before calling it a failure.
func QueueSnapshot(ctx context.Context, t *testing.T, runcnx *params.Run) map[string]queue.RepoQueue {
	t.Helper()
	pods := watcherPods(ctx, t, runcnx)
	pod := &pods[0]

	var lastErr error
	for range 15 {
		raw, err := runcnx.Clients.Kube.CoreV1().Pods(watcherNamespace).
			ProxyGet("http", pod.GetName(), probesPort(t, pod), "/debug/queue", nil).DoRaw(ctx)
		if err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		snapshot := map[string]queue.RepoQueue{}
		assert.NilError(t, json.Unmarshal(raw, &snapshot), "unexpected payload from the queue debug endpoint: %s", raw)
		return snapshot
	}
	t.Fatalf("could not read the queue debug endpoint on %s: %v", pod.GetName(), lastErr)
	return nil
}

// probesPort returns the probe port as a number. The pod proxy does not resolve
// port names, and the port is configurable, so read it off the container.
func probesPort(t *testing.T, pod *corev1.Pod) string {
	t.Helper()
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			if port.Name == watcherProbesPort {
				return strconv.Itoa(int(port.ContainerPort))
			}
		}
	}
	t.Fatalf("watcher pod %s has no container port named %q", pod.GetName(), watcherProbesPort)
	return ""
}

// AssertQueueDrained checks the watcher is no longer holding a concurrency slot
// for a repository. A slot that outlives every run it was taken for is a leak,
// and the next run for that repository will never start.
func AssertQueueDrained(ctx context.Context, t *testing.T, runcnx *params.Run, ns, name string) {
	t.Helper()
	key := ns + "/" + name
	var last queue.RepoQueue
	for range 30 {
		snapshot := QueueSnapshot(ctx, t, runcnx)
		var known bool
		last, known = snapshot[key]
		// An absent key would drain trivially, so insist the watcher actually
		// knows about this repository before believing the empty result.
		assert.Assert(t, known, "the watcher holds no queue at all for %s, it has %v", key, keysOf(snapshot))
		if len(last.Running) == 0 && len(last.Pending) == 0 {
			runcnx.Clients.Log.Infof("queue for %s is drained", key)
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("the watcher still holds slots for %s after every run finished: running=%v pending=%v",
		key, last.Running, last.Pending)
}

func keysOf(snapshot map[string]queue.RepoQueue) []string {
	out := make([]string, 0, len(snapshot))
	for key := range snapshot {
		out = append(out, key)
	}
	return out
}

func waitForWatcherReplicas(ctx context.Context, t *testing.T, runcnx *params.Run, want int32) {
	t.Helper()
	for range 60 {
		dep, err := runcnx.Clients.Kube.AppsV1().Deployments(watcherNamespace).Get(ctx, watcherDeployment, metav1.GetOptions{})
		assert.NilError(t, err)
		if dep.Status.ReadyReplicas == want && dep.Status.Replicas == want {
			runcnx.Clients.Log.Infof("watcher is at %d ready replica(s)", want)
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("watcher did not settle at %d ready replica(s) within 2 minutes", want)
}

func watcherPods(ctx context.Context, t *testing.T, runcnx *params.Run) []corev1.Pod {
	t.Helper()
	pods, err := runcnx.Clients.Kube.CoreV1().Pods(watcherNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: watcherSelector,
	})
	assert.NilError(t, err)
	assert.Assert(t, len(pods.Items) > 0, "no watcher pod found in %s with selector %s", watcherNamespace, watcherSelector)
	return pods.Items
}

func totalRestarts(pod *corev1.Pod) int32 {
	var total int32
	for _, cs := range pod.Status.ContainerStatuses {
		total += cs.RestartCount
	}
	return total
}

// watcherLog returns the previous container log when there is one, since that
// is where a crash is recorded, and falls back to the current one.
func watcherLog(ctx context.Context, runcnx *params.Run, podName string) string {
	for _, previous := range []bool{true, false} {
		req := runcnx.Clients.Kube.CoreV1().Pods(watcherNamespace).GetLogs(podName, &corev1.PodLogOptions{
			Previous: previous,
		})
		stream, err := req.Stream(ctx)
		if err != nil {
			continue
		}
		out, err := io.ReadAll(stream)
		_ = stream.Close()
		if err != nil || len(out) == 0 {
			continue
		}
		return fmt.Sprintf("--- watcher log (previous=%v) ---\n%s", previous, string(out))
	}
	return "(no watcher log available)"
}
