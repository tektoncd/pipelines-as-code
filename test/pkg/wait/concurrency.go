package wait

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Counts is a breakdown of PipelineRuns by how far along they are.
type Counts struct {
	Total   int
	Pending int
	Running int
	Done    int
}

func (c Counts) String() string {
	return fmt.Sprintf("total=%d pending=%d running=%d done=%d", c.Total, c.Pending, c.Running, c.Done)
}

// CountPipelineRuns groups the PipelineRuns of a namespace by state. Pending
// means the queue is still holding it back; running means it has started and
// has not finished.
func CountPipelineRuns(ctx context.Context, clients clients.Clients, ns string) (Counts, []v1.PipelineRun, error) {
	list, err := clients.Tekton.TektonV1().PipelineRuns(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return Counts{}, nil, err
	}
	var counts Counts
	counts.Total = len(list.Items)
	for i := range list.Items {
		pr := &list.Items[i]
		switch {
		case pr.IsDone():
			counts.Done++
		case pr.Spec.Status == v1.PipelineRunSpecStatusPending:
			counts.Pending++
		case pr.Status.StartTime != nil && !pr.Status.StartTime.IsZero():
			counts.Running++
		}
	}
	return counts, list.Items, nil
}

// UntilCounts polls a namespace until its PipelineRuns satisfy cond, and fails
// the test with the last counts seen if they never do. describe is used in that
// failure message to say what was being waited for.
func UntilCounts(ctx context.Context, t *testing.T, clients clients.Clients, ns, describe string, timeout time.Duration, cond func(Counts) bool) (Counts, []v1.PipelineRun) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		counts, prs, err := CountPipelineRuns(ctx, clients, ns)
		assert.NilError(t, err)
		if cond(counts) {
			clients.Log.Infof("%s in %s: %s", describe, ns, counts)
			return counts, prs
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s in %s, last seen %s", timeout, describe, ns, counts)
		}
		clients.Log.Infof("waiting for %s in %s, currently %s", describe, ns, counts)
		time.Sleep(5 * time.Second)
	}
}

// Interval is the window a single PipelineRun occupied a concurrency slot for.
type Interval struct {
	Name  string
	Start time.Time
	End   time.Time
}

// Overlap is the busiest moment observed across a set of PipelineRuns: how many
// ran at once, when, and which ones they were.
type Overlap struct {
	Peak  int
	At    time.Time
	Names []string
}

// MaxConcurrency computes the largest number of PipelineRuns that were running
// at the same instant, from their recorded start and completion times.
//
// This is deliberately retrospective rather than a poll of the live cluster: a
// poller samples, and an overshoot that lasts less than the polling interval is
// invisible to it. Working from the recorded intervals cannot miss a window.
//
// PipelineRuns that never started are ignored. One still running when the
// snapshot was taken is treated as running until the latest end seen, which is
// the least favourable reading and so cannot hide an overshoot. Kubernetes
// timestamps have second granularity, so an interval that ends in the same
// second it started is treated as a point and cannot overlap anything.
func MaxConcurrency(prs []v1.PipelineRun) Overlap {
	intervals := make([]Interval, 0, len(prs))
	var latestEnd time.Time
	for i := range prs {
		pr := &prs[i]
		if pr.Status.StartTime == nil || pr.Status.StartTime.IsZero() {
			continue
		}
		iv := Interval{Name: pr.GetName(), Start: pr.Status.StartTime.Time}
		if pr.Status.CompletionTime != nil && !pr.Status.CompletionTime.IsZero() {
			iv.End = pr.Status.CompletionTime.Time
		}
		if iv.End.After(latestEnd) {
			latestEnd = iv.End
		}
		if iv.Start.After(latestEnd) {
			latestEnd = iv.Start
		}
		intervals = append(intervals, iv)
	}

	type edge struct {
		at    time.Time
		delta int
	}
	edges := make([]edge, 0, len(intervals)*2)
	for _, iv := range intervals {
		end := iv.End
		if end.IsZero() {
			// still running when we looked: assume it ran to the very end
			end = latestEnd
		}
		if !end.After(iv.Start) {
			// finished within the same second it started, so it never overlapped
			// anything we can prove
			continue
		}
		edges = append(edges, edge{at: iv.Start, delta: 1}, edge{at: end, delta: -1})
	}

	// ends before starts at the same instant, so a run that finishes exactly as
	// another begins is not counted as an overlap
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].at.Equal(edges[j].at) {
			return edges[i].delta < edges[j].delta
		}
		return edges[i].at.Before(edges[j].at)
	})

	var result Overlap
	current := 0
	for _, e := range edges {
		current += e.delta
		if current > result.Peak {
			result.Peak = current
			result.At = e.at
		}
	}

	for _, iv := range intervals {
		end := iv.End
		if end.IsZero() {
			end = latestEnd
		}
		if !iv.Start.After(result.At) && end.After(result.At) {
			result.Names = append(result.Names, iv.Name)
		}
	}
	sort.Strings(result.Names)
	return result
}

// AssertMaxConcurrency fails the test if more than limit PipelineRuns were ever
// running at the same time.
//
// Asserting that queued PipelineRuns all eventually succeed says nothing about
// whether the limit was honoured, since a queue that ignores the limit entirely
// also finishes every run. This is the assertion that actually tests the queue.
func AssertMaxConcurrency(t *testing.T, prs []v1.PipelineRun, limit int) {
	t.Helper()
	got := MaxConcurrency(prs)
	assert.Assert(t, got.Peak <= limit,
		"concurrency limit of %d exceeded: %d PipelineRuns were running at once at %s: %v\n%s",
		limit, got.Peak, got.At.Format(time.RFC3339), got.Names, formatIntervals(prs))
}

func formatIntervals(prs []v1.PipelineRun) string {
	out := "observed PipelineRun windows:\n"
	sorted := make([]v1.PipelineRun, len(prs))
	copy(sorted, prs)
	SortPipelineRunsByCreationMillis(sorted)
	for i := range sorted {
		pr := &sorted[i]
		start, end := "never started", "still running"
		if pr.Status.StartTime != nil && !pr.Status.StartTime.IsZero() {
			start = pr.Status.StartTime.Format(time.RFC3339)
		}
		if pr.Status.CompletionTime != nil && !pr.Status.CompletionTime.IsZero() {
			end = pr.Status.CompletionTime.Format(time.RFC3339)
		}
		out += fmt.Sprintf("  %-50s %s -> %s\n", pr.GetName(), start, end)
	}
	return out
}
