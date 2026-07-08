package wait

import (
	"context"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	paramsclients "github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	knativeapis "knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func makePipelineRun(name, sha, reason string) *pipelinev1.PipelineRun {
	pr := &pipelinev1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test-ns",
			Labels: map[string]string{
				keys.SHA: sha,
			},
		},
	}
	if reason != "" {
		pr.Status.Status = duckv1.Status{
			Conditions: []knativeapis.Condition{
				{
					Type:   knativeapis.ConditionSucceeded,
					Reason: reason,
				},
			},
		}
	}
	return pr
}

func makePipelineRunWithStatus(name, sha, reason string, condStatus corev1.ConditionStatus, created, completed time.Time) *pipelinev1.PipelineRun {
	pr := makePipelineRun(name, sha, reason)
	pr.CreationTimestamp = metav1.NewTime(created)
	if !completed.IsZero() {
		pr.Status.CompletionTime = &metav1.Time{Time: completed}
	}
	if reason != "" {
		pr.Status.Status = duckv1.Status{
			Conditions: []knativeapis.Condition{
				{
					Type:   knativeapis.ConditionSucceeded,
					Status: condStatus,
					Reason: reason,
				},
			},
		}
	}
	return pr
}

// withState sets the PaC state annotation the watcher patches after it has
// finished reporting a PipelineRun.
func withState(pr *pipelinev1.PipelineRun, state string) *pipelinev1.PipelineRun {
	if pr.Annotations == nil {
		pr.Annotations = map[string]string{}
	}
	pr.Annotations[keys.State] = state
	return pr
}

func seedAndMakeClients(ctx context.Context, t *testing.T, prs []*pipelinev1.PipelineRun) paramsclients.Clients {
	t.Helper()
	seeded, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Namespaces: []*corev1.Namespace{
			{ObjectMeta: metav1.ObjectMeta{Name: "test-ns"}},
		},
		PipelineRuns: prs,
	})
	return paramsclients.Clients{
		Tekton: seeded.Pipeline,
		Log:    zap.NewNop().Sugar(),
	}
}

type waitTestCase struct {
	name         string
	pipelineRuns []*pipelinev1.PipelineRun
	targetSHA    []string
	minStatus    int
	wantErr      bool
	wantCount    int
	wantOrder    []string
}

func runWaitTests(t *testing.T, tests []waitTestCase, waitFn func(context.Context, paramsclients.Clients, Opts) ([]pipelinev1.PipelineRun, error)) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			clients := seedAndMakeClients(ctx, t, tt.pipelineRuns)
			opts := Opts{
				Namespace:       "test-ns",
				MinNumberStatus: tt.minStatus,
				TargetSHA:       tt.targetSHA,
				PollTimeout:     10 * time.Millisecond,
			}

			prs, err := waitFn(ctx, clients, opts)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				assert.ErrorContains(t, err, "timed out")
				return
			}

			assert.NilError(t, err)
			assert.Equal(t, len(prs), tt.wantCount)
			if len(tt.wantOrder) > 0 {
				for i, name := range tt.wantOrder {
					assert.Equal(t, prs[i].Name, name, "index %d: expected %s got %s", i, name, prs[i].Name)
				}
			}
		})
	}
}

func TestUntilPipelineRunCreated(t *testing.T) {
	now := time.Now()
	runWaitTests(t, []waitTestCase{
		{
			name: "exact count match returns pipelineruns",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRunWithStatus("pr-b", "sha-1", "", "", now.Add(2*time.Second), time.Time{}),
				makePipelineRunWithStatus("pr-a", "sha-1", "", "", now, time.Time{}),
			},
			targetSHA: []string{"sha-1"},
			minStatus: 2,
			wantCount: 2,
			wantOrder: []string{"pr-a", "pr-b"},
		},
		{
			name: "sort order by creation time ascending with three pipelineruns",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRunWithStatus("pr-third", "sha-1", "", "", now.Add(3*time.Second), time.Time{}),
				makePipelineRunWithStatus("pr-first", "sha-1", "", "", now, time.Time{}),
				makePipelineRunWithStatus("pr-second", "sha-1", "", "", now.Add(1*time.Second), time.Time{}),
			},
			targetSHA: []string{"sha-1"},
			minStatus: 3,
			wantCount: 3,
			wantOrder: []string{"pr-first", "pr-second", "pr-third"},
		},
		{
			name: "times out when count does not match exactly",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRun("pr-1", "sha-1", ""),
			},
			targetSHA: []string{"sha-1"},
			minStatus: 2,
			wantErr:   true,
		},
		{
			name: "no target sha matches all pipelineruns",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRunWithStatus("pr-2", "sha-2", "", "", now.Add(1*time.Second), time.Time{}),
				makePipelineRunWithStatus("pr-1", "sha-1", "", "", now, time.Time{}),
			},
			minStatus: 2,
			wantCount: 2,
			wantOrder: []string{"pr-1", "pr-2"},
		},
		{
			name: "zero min status waits for one created pipelinerun",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRun("pr-1", "sha-1", ""),
			},
			targetSHA: []string{"sha-1"},
			minStatus: 0,
			wantCount: 1,
			wantOrder: []string{"pr-1"},
		},
	}, UntilPipelineRunCreated)
}

func TestUntilPipelineRunsFinished(t *testing.T) {
	now := time.Now()
	runWaitTests(t, []waitTestCase{
		{
			name: "mixed success and failure both returned sorted by completion",
			pipelineRuns: []*pipelinev1.PipelineRun{
				withState(makePipelineRunWithStatus("pr-late", "sha-1", "Succeeded", corev1.ConditionTrue, now, now.Add(10*time.Second)), kubeinteraction.StateCompleted),
				withState(makePipelineRunWithStatus("pr-early", "sha-1", "Failed", corev1.ConditionFalse, now, now.Add(5*time.Second)), kubeinteraction.StateFailed),
			},
			targetSHA: []string{"sha-1"},
			minStatus: 2,
			wantCount: 2,
			wantOrder: []string{"pr-early", "pr-late"},
		},
		{
			name: "running pipelinerun not counted as finished",
			pipelineRuns: []*pipelinev1.PipelineRun{
				withState(makePipelineRunWithStatus("pr-done", "sha-1", "Succeeded", corev1.ConditionTrue, now, now.Add(5*time.Second)), kubeinteraction.StateCompleted),
				makePipelineRun("pr-running", "sha-1", ""),
			},
			targetSHA: []string{"sha-1"},
			minStatus: 2,
			wantErr:   true,
		},
		{
			name: "terminal condition without watcher state annotation not counted as finished",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRunWithStatus("pr-not-reported", "sha-1", "Succeeded", corev1.ConditionTrue, now, now.Add(5*time.Second)),
			},
			targetSHA: []string{"sha-1"},
			minStatus: 1,
			wantErr:   true,
		},
		{
			name: "started state annotation not counted as finished",
			pipelineRuns: []*pipelinev1.PipelineRun{
				withState(makePipelineRunWithStatus("pr-started", "sha-1", "Succeeded", corev1.ConditionTrue, now, now.Add(5*time.Second)), kubeinteraction.StateStarted),
			},
			targetSHA: []string{"sha-1"},
			minStatus: 1,
			wantErr:   true,
		},
		{
			name: "no target sha returns all finished across shas",
			pipelineRuns: []*pipelinev1.PipelineRun{
				withState(makePipelineRunWithStatus("pr-b", "sha-2", "Failed", corev1.ConditionFalse, now, now.Add(8*time.Second)), kubeinteraction.StateFailed),
				withState(makePipelineRunWithStatus("pr-a", "sha-1", "Succeeded", corev1.ConditionTrue, now, now.Add(3*time.Second)), kubeinteraction.StateCompleted),
			},
			minStatus: 2,
			wantCount: 2,
			wantOrder: []string{"pr-a", "pr-b"},
		},
		{
			name: "times out when not enough finished",
			pipelineRuns: []*pipelinev1.PipelineRun{
				withState(makePipelineRunWithStatus("pr-1", "sha-1", "Succeeded", corev1.ConditionTrue, now, now.Add(5*time.Second)), kubeinteraction.StateCompleted),
			},
			targetSHA: []string{"sha-1"},
			minStatus: 3,
			wantErr:   true,
		},
		{
			name: "cancelled pipelinerun counted as finished",
			pipelineRuns: []*pipelinev1.PipelineRun{
				withState(makePipelineRunWithStatus("pr-1", "sha-1", "Cancelled", corev1.ConditionFalse, now, now.Add(5*time.Second)), kubeinteraction.StateCompleted),
			},
			targetSHA: []string{"sha-1"},
			minStatus: 1,
			wantCount: 1,
		},
		{
			name: "zero min status waits for one finished pipelinerun",
			pipelineRuns: []*pipelinev1.PipelineRun{
				withState(makePipelineRunWithStatus("pr-1", "sha-1", "Succeeded", corev1.ConditionTrue, now, now.Add(5*time.Second)), kubeinteraction.StateCompleted),
			},
			targetSHA: []string{"sha-1"},
			minStatus: 0,
			wantCount: 1,
			wantOrder: []string{"pr-1"},
		},
	}, UntilPipelineRunsFinished)
}

func TestUntilPipelineRunHasReason(t *testing.T) {
	tests := []struct {
		name            string
		pipelineRuns    []*pipelinev1.PipelineRun
		targetSHA       []string
		reason          string
		minNumberStatus int
		wantErr         bool
	}{
		{
			name: "match by target sha and reason",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRun("pr-1", "sha-1", "Succeeded"),
				makePipelineRun("pr-2", "sha-2", "Cancelled"),
			},
			targetSHA:       []string{"sha-2"},
			reason:          "Cancelled",
			minNumberStatus: 1,
		},
		{
			name: "wrong reason for matching sha",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRun("pr-1", "sha-2", "Succeeded"),
			},
			targetSHA:       []string{"sha-2"},
			reason:          "Cancelled",
			minNumberStatus: 1,
			wantErr:         true,
		},
		{
			name: "reason exists on a different sha only",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRun("pr-1", "sha-1", "Cancelled"),
			},
			targetSHA:       []string{"sha-2"},
			reason:          "Cancelled",
			minNumberStatus: 1,
			wantErr:         true,
		},
		{
			name: "match without target sha filter",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRun("pr-1", "sha-1", "Cancelled"),
			},
			reason:          "Cancelled",
			minNumberStatus: 1,
		},
		{
			name: "zero min status waits for one matching reason",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRun("pr-1", "sha-1", "Succeeded"),
			},
			targetSHA: []string{"sha-1"},
			reason:    "Succeeded",
		},
		{
			name: "pipelinerun without conditions",
			pipelineRuns: []*pipelinev1.PipelineRun{
				makePipelineRun("pr-1", "sha-2", ""),
			},
			targetSHA:       []string{"sha-2"},
			reason:          "Cancelled",
			minNumberStatus: 1,
			wantErr:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			clients := seedAndMakeClients(ctx, t, tt.pipelineRuns)
			opts := Opts{
				Namespace:       "test-ns",
				MinNumberStatus: tt.minNumberStatus,
				TargetSHA:       tt.targetSHA,
				PollTimeout:     10 * time.Millisecond,
			}

			prs, err := UntilPipelineRunHasReason(ctx, clients, pipelinev1.PipelineRunReason(tt.reason), opts)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				assert.ErrorContains(t, err, "timed out")
				return
			}

			assert.NilError(t, err)
			assert.Assert(t, len(prs) >= 1)
		})
	}
}
