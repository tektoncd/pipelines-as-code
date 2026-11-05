package reconciler

import (
	"maps"
	"slices"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	prgroup "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerungroup"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

// groupRun builds a run of a follow-up group with the given follow-ups
// stored on it.
func groupRun(t *testing.T, name, definition, groupID string, followUps ...prgroup.FollowUp) *tektonv1.PipelineRun {
	t.Helper()
	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "namespace",
			Labels: map[string]string{
				keys.GroupID: groupID,
			},
			Annotations: map[string]string{
				keys.GroupID:        groupID,
				keys.OriginalPRName: definition,
			},
		},
	}
	assert.NilError(t, prgroup.AddFollowUps(pr, followUps...))
	return pr
}

func TestHasWaitingDependent(t *testing.T) {
	deploy := prgroup.FollowUp{
		ID:    "deploy-group1",
		Name:  "deploy",
		Deps:  []string{"build"},
		State: prgroup.StatePending,
	}
	createdFollowUp := deploy
	createdFollowUp.State = prgroup.StateCreated
	publish := prgroup.FollowUp{
		ID:    "publish-group1",
		Name:  "publish",
		Deps:  []string{"tests"},
		State: prgroup.StatePending,
	}
	createdPublish := publish
	createdPublish.State = prgroup.StateCreated

	tests := []struct {
		name     string
		pr       *tektonv1.PipelineRun
		group    []*tektonv1.PipelineRun
		expected bool
	}{
		{
			name: "a run outside any group is prunable",
			pr:   groupRun(t, "plain", "build", ""),
		},
		{
			name: "a run with a pending follow-up stored on it is not prunable",
			pr:   groupRun(t, "build", "build", "group1", deploy),
			group: []*tektonv1.PipelineRun{
				groupRun(t, "build", "build", "group1", deploy),
			},
			expected: true,
		},
		{
			// a failed migration cleanup left the pending copy on lint after
			// the copy that counts, migrated onto tests, went terminal
			name: "a run with a stale pending copy of a terminal follow-up is prunable",
			pr:   groupRun(t, "lint", "lint", "group1", publish),
			group: []*tektonv1.PipelineRun{
				groupRun(t, "lint", "lint", "group1", publish),
				groupRun(t, "tests", "tests", "group1", createdPublish),
			},
		},
		{
			name: "a run named as a dependency of a pending follow-up stored on another run is not prunable",
			pr:   groupRun(t, "build", "build", "group1"),
			group: []*tektonv1.PipelineRun{
				groupRun(t, "build", "build", "group1"),
				groupRun(t, "tests", "tests", "group1", deploy),
			},
			expected: true,
		},
		{
			name: "a run no pending follow-up names as a dependency is prunable",
			pr:   groupRun(t, "tests", "tests", "group1"),
			group: []*tektonv1.PipelineRun{
				groupRun(t, "tests", "tests", "group1"),
				groupRun(t, "build", "build", "group1", deploy),
			},
		},
		{
			name: "a run whose group only has terminal follow-ups is prunable",
			pr:   groupRun(t, "build", "build", "group1", createdFollowUp),
			group: []*tektonv1.PipelineRun{
				groupRun(t, "build", "build", "group1", createdFollowUp),
			},
		},
		{
			name: "a run of a group with a corrupt follow-up annotation is prunable",
			pr:   groupRun(t, "build", "build", "group1"),
			group: []*tektonv1.PipelineRun{
				groupRun(t, "build", "build", "group1"),
				func() *tektonv1.PipelineRun {
					corrupt := groupRun(t, "broken", "broken", "group1")
					corrupt.Annotations[keys.FollowUps] = "{not json"
					return corrupt
				}(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			tdata := testclient.Data{PipelineRuns: tt.group}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)

			r := &Reconciler{run: &params.Run{Clients: clients.Clients{Tekton: stdata.Pipeline}}}
			waiting, err := r.hasWaitingDependent(ctx, zap.NewNop().Sugar(), tt.pr)
			assert.NilError(t, err)
			assert.Equal(t, tt.expected, waiting)
		})
	}
}

// TestCleanupPipelineRunsKeepsFollowUpRuns runs the real max-keep-runs cleanup
// after the newest build completed: the older build runs a pending follow-up
// still needs are kept, the others pruned.
func TestCleanupPipelineRunsKeepsFollowUpRuns(t *testing.T) {
	clock := clockwork.NewFakeClock()
	const ns = "namespace"
	labels := map[string]string{
		keys.OriginalPRName: "build",
		keys.Repository:     "repo",
		keys.State:          kubeinteraction.StateCompleted,
	}
	// buildRun is a completed build run, minutesAgo old, of the given group
	buildRun := func(t *testing.T, name string, minutesAgo int, groupID string, followUps ...prgroup.FollowUp) *tektonv1.PipelineRun {
		t.Helper()
		pr := tektontest.MakePRCompletion(clock, name, ns, tektonv1.PipelineRunReasonSuccessful.String(),
			map[string]string{keys.OriginalPRName: "build"}, maps.Clone(labels), minutesAgo)
		if groupID != "" {
			pr.Labels[keys.GroupID] = groupID
			pr.Annotations[keys.GroupID] = groupID
		}
		assert.NilError(t, prgroup.AddFollowUps(pr, followUps...))
		return pr
	}
	deploy := prgroup.FollowUp{ID: "deploy-group1", Name: "deploy", Deps: []string{"build", "tests"}, State: prgroup.StatePending}

	tests := []struct {
		name     string
		setup    func(t *testing.T) []*tektonv1.PipelineRun
		wantKept []string
	}{
		{
			name: "an older run storing a pending follow-up is kept",
			setup: func(t *testing.T) []*tektonv1.PipelineRun {
				return []*tektonv1.PipelineRun{
					buildRun(t, "build-new", 10, "group2"),
					buildRun(t, "build-old", 20, "group1", deploy),
				}
			},
			wantKept: []string{"build-new", "build-old"},
		},
		{
			name: "an older dependency a pending follow-up waits on is kept",
			setup: func(t *testing.T) []*tektonv1.PipelineRun {
				testsRun := groupRun(t, "tests-1", "tests", "group1", deploy)
				return []*tektonv1.PipelineRun{
					buildRun(t, "build-new", 10, "group2"),
					buildRun(t, "build-old", 20, "group1"),
					testsRun,
				}
			},
			wantKept: []string{"build-new", "build-old", "tests-1"},
		},
		{
			name: "an older run no follow-up needs is pruned",
			setup: func(t *testing.T) []*tektonv1.PipelineRun {
				return []*tektonv1.PipelineRun{
					buildRun(t, "build-new", 10, "group2", deploy),
					buildRun(t, "build-old", 20, ""),
				}
			},
			wantKept: []string{"build-new"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			runs := tt.setup(t)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{PipelineRuns: runs})
			cs := &params.Run{Clients: clients.Clients{Tekton: stdata.Pipeline, Kube: stdata.Kube}}
			r := &Reconciler{run: cs, kinteract: kubeinteraction.Interaction{Run: cs}}

			current := runs[0].DeepCopy()
			current.Annotations[keys.MaxKeepRuns] = "1"
			repo := &v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: ns}}
			assert.NilError(t, r.cleanupPipelineRuns(ctx, zap.NewNop().Sugar(), info.NewPacOpts(), repo, current))

			list, err := stdata.Pipeline.TektonV1().PipelineRuns(ns).List(ctx, metav1.ListOptions{})
			assert.NilError(t, err)
			kept := make([]string, 0, len(list.Items))
			for i := range list.Items {
				kept = append(kept, list.Items[i].GetName())
			}
			slices.Sort(kept)
			assert.DeepEqual(t, tt.wantKept, kept)
		})
	}
}
