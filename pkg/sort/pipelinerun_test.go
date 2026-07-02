package sort

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

func TestPipelineRunSortByCompletionTime(t *testing.T) {
	clock := clockwork.NewFakeClock()
	ns := "namespace"
	labels := map[string]string{}
	success := tektonv1.PipelineRunReasonSuccessful.String()

	pruns := []tektonv1.PipelineRun{
		*tektontest.MakePRCompletion(clock, "troisieme", ns, success, nil, labels, 30),
		*tektontest.MakePRCompletion(clock, "premier", ns, success, nil, labels, 10),
		*tektontest.MakePRCompletion(clock, "second", ns, success, nil, labels, 20),
	}

	noCompletionTimePR := tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-completion-time",
			Namespace: ns,
			Labels:    labels,
		},
		Status: tektonv1.PipelineRunStatus{
			Status: duckv1.Status{
				Conditions: duckv1.Conditions{
					{
						Type:   apis.ConditionSucceeded,
						Status: "True",
						Reason: success,
					},
				},
			},
		},
	}

	prunsMissing := []tektonv1.PipelineRun{
		*tektontest.MakePRCompletion(clock, "troisieme", ns, success, nil, labels, 30),
		*tektontest.MakePRCompletion(clock, "premier", ns, success, nil, labels, 10),
		noCompletionTimePR,
	}

	prunsWithOneMissing := []tektonv1.PipelineRun{
		*tektontest.MakePRCompletion(clock, "premier", ns, success, nil, labels, 10),
		noCompletionTimePR,
	}

	prunsWithUncompletedFirst := []tektonv1.PipelineRun{
		noCompletionTimePR,
		*tektontest.MakePRCompletion(clock, "premier", ns, success, nil, labels, 10),
	}

	tests := []struct {
		name     string
		pruns    []tektonv1.PipelineRun
		wantName []string
	}{
		{
			name:     "sort by completion time",
			pruns:    pruns,
			wantName: []string{"premier", "second", "troisieme"},
		},
		{
			name:     "sort by completion time with missing",
			pruns:    prunsMissing,
			wantName: []string{"no-completion-time", "premier", "troisieme"},
		},
		{
			name:     "sort by completion time with one missing",
			pruns:    prunsWithOneMissing,
			wantName: []string{"no-completion-time", "premier"},
		},
		{
			name:     "sort with uncompleted item first",
			pruns:    prunsWithUncompletedFirst,
			wantName: []string{"no-completion-time", "premier"},
		},
		{
			name:     "empty list",
			pruns:    []tektonv1.PipelineRun{},
			wantName: []string{},
		},
		{
			name:     "single item",
			pruns:    []tektonv1.PipelineRun{*tektontest.MakePRCompletion(clock, "premier", ns, success, nil, labels, 10)},
			wantName: []string{"premier"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PipelineRunSortByCompletionTime(tt.pruns)
			gotNames := make([]string, len(got))
			for i, pr := range got {
				gotNames[i] = pr.GetName()
			}
			assert.DeepEqual(t, tt.wantName, gotNames)
		})
	}
}

func TestPipelineRunSortByCompletionTimeSameTime(t *testing.T) {
	clock := clockwork.NewFakeClock()
	ns := "namespace"
	labels := map[string]string{}
	success := tektonv1.PipelineRunReasonSuccessful.String()

	pruns := []tektonv1.PipelineRun{
		*tektontest.MakePRCompletion(clock, "first-same-time", ns, success, nil, labels, 15),
		*tektontest.MakePRCompletion(clock, "second-same-time", ns, success, nil, labels, 15),
		*tektontest.MakePRCompletion(clock, "third-same-time", ns, success, nil, labels, 15),
		*tektontest.MakePRCompletion(clock, "earlier", ns, success, nil, labels, 10),
		*tektontest.MakePRCompletion(clock, "later", ns, success, nil, labels, 20),
	}

	got := PipelineRunSortByCompletionTime(pruns)
	gotNames := make([]string, len(got))
	for i, pr := range got {
		gotNames[i] = pr.GetName()
	}

	// Verify that "earlier" comes first
	assert.Equal(t, "earlier", gotNames[0])
	// Verify that "later" comes last
	assert.Equal(t, "later", gotNames[len(gotNames)-1])
	// Verify that the three items with same time are in positions 1-3 (any order)
	sameTimeNames := gotNames[1:4]
	expectedSameTime := map[string]bool{
		"first-same-time":  false,
		"second-same-time": false,
		"third-same-time":  false,
	}
	for _, name := range sameTimeNames {
		if _, exists := expectedSameTime[name]; exists {
			expectedSameTime[name] = true
		} else {
			t.Errorf("Unexpected name %s in same-time group", name)
		}
	}
	// Verify all expected names were found
	for name, found := range expectedSameTime {
		if !found {
			t.Errorf("Expected name %s not found in same-time group", name)
		}
	}
}

func TestPipelineRunSortByStartTime(t *testing.T) {
	clock := clockwork.NewFakeClock()
	ns := "namespace"
	labels := map[string]string{}
	success := tektonv1.PipelineRunReasonSuccessful.String()
	startedEarlierPR := tektontest.MakePRCompletion(clock, "earlier", ns, success, nil, labels, 5)
	startedEarlierPR.Status.StartTime = &metav1.Time{Time: clock.Now().Add(100 * time.Minute)}

	noCompletionPR := tektontest.MakePRCompletion(clock, "noCompletion", ns, success, nil, labels, 5)
	noCompletionPR.Status.StartTime = &metav1.Time{Time: clock.Now().Add(500 * time.Minute)}
	noCompletionPR.Status.CompletionTime = nil

	notStartedYet := tektontest.MakePRCompletion(clock, "notStarted", ns, success, nil, labels, 5)
	notStartedYet.Status.StartTime = nil
	notStartedYet.Status.CompletionTime = nil

	prunsWithOneNotStarted := []tektonv1.PipelineRun{
		*tektontest.MakePRCompletion(clock, "otherFirst", ns, success, nil, labels, 30),
		*notStartedYet,
	}

	prunsWithNotStartedFirst := []tektonv1.PipelineRun{
		*notStartedYet,
		*tektontest.MakePRCompletion(clock, "otherFirst", ns, success, nil, labels, 30),
	}

	tests := []struct {
		name     string
		pruns    []tektonv1.PipelineRun
		wantName []string
	}{
		{
			name: "finished last started first",
			pruns: []tektonv1.PipelineRun{
				*tektontest.MakePRCompletion(clock, "otherFirst", ns, success, nil, labels, 30),
				*tektontest.MakePRCompletion(clock, "otherSecond", ns, success, nil, labels, 10),
				*startedEarlierPR,
			},
			wantName: []string{"earlier", "otherFirst", "otherSecond"},
		},
		{
			name: "no completion but started first",
			pruns: []tektonv1.PipelineRun{
				*noCompletionPR,
				*tektontest.MakePRCompletion(clock, "otherFirst", ns, success, nil, labels, 30),
				*tektontest.MakePRCompletion(clock, "otherSecond", ns, success, nil, labels, 10),
			},
			wantName: []string{"noCompletion", "otherFirst", "otherSecond"},
		},

		{
			name: "not started yet",
			pruns: []tektonv1.PipelineRun{
				*tektontest.MakePRCompletion(clock, "otherFirst", ns, success, nil, labels, 30),
				*tektontest.MakePRCompletion(clock, "otherSecond", ns, success, nil, labels, 10),
				*notStartedYet,
			},
			wantName: []string{"notStarted", "otherFirst", "otherSecond"},
		},
		{
			name:     "not started yet single",
			pruns:    prunsWithOneNotStarted,
			wantName: []string{"notStarted", "otherFirst"},
		},
		{
			name:     "sort with not-started item first",
			pruns:    prunsWithNotStartedFirst,
			wantName: []string{"notStarted", "otherFirst"},
		},
		{
			name:     "empty list",
			pruns:    []tektonv1.PipelineRun{},
			wantName: []string{},
		},
		{
			name:     "single item",
			pruns:    []tektonv1.PipelineRun{*startedEarlierPR},
			wantName: []string{"earlier"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			PipelineRunSortByStartTime(tt.pruns)
			gotNames := make([]string, len(tt.pruns))
			for i, pr := range tt.pruns {
				gotNames[i] = pr.GetName()
			}
			assert.DeepEqual(t, tt.wantName, gotNames)
		})
	}
}

func TestPipelineRunSortByStartTimeSameTime(t *testing.T) {
	clock := clockwork.NewFakeClock()
	ns := "namespace"
	labels := map[string]string{}
	success := tektonv1.PipelineRunReasonSuccessful.String()

	sameStartTime := clock.Now().Add(200 * time.Minute)

	pr1 := tektontest.MakePRCompletion(clock, "first-same-start", ns, success, nil, labels, 5)
	pr1.Status.StartTime = &metav1.Time{Time: sameStartTime}
	pr1.Status.CompletionTime = &metav1.Time{Time: sameStartTime.Add(10 * time.Minute)}

	pr2 := tektontest.MakePRCompletion(clock, "second-same-start", ns, success, nil, labels, 5)
	pr2.Status.StartTime = &metav1.Time{Time: sameStartTime}
	pr2.Status.CompletionTime = &metav1.Time{Time: sameStartTime.Add(15 * time.Minute)}

	pr3 := tektontest.MakePRCompletion(clock, "third-same-start", ns, success, nil, labels, 5)
	pr3.Status.StartTime = &metav1.Time{Time: sameStartTime}
	pr3.Status.CompletionTime = &metav1.Time{Time: sameStartTime.Add(20 * time.Minute)}

	earlierPR := tektontest.MakePRCompletion(clock, "started-earlier", ns, success, nil, labels, 5)
	earlierPR.Status.StartTime = &metav1.Time{Time: sameStartTime.Add(-60 * time.Minute)}

	laterPR := tektontest.MakePRCompletion(clock, "started-later", ns, success, nil, labels, 5)
	laterPR.Status.StartTime = &metav1.Time{Time: sameStartTime.Add(60 * time.Minute)}

	pruns := []tektonv1.PipelineRun{*pr1, *pr2, *pr3, *earlierPR, *laterPR}

	PipelineRunSortByStartTime(pruns)
	gotNames := make([]string, len(pruns))
	for i, pr := range pruns {
		gotNames[i] = pr.GetName()
	}

	// With stable sorting by name descending when times are equal:
	// - "started-later" comes first (later time)
	// - Same-time items sorted by name descending: third, second, first
	// - "started-earlier" comes last (earlier time)
	expectedOrder := []string{
		"started-later",
		"third-same-start",
		"second-same-start",
		"first-same-start",
		"started-earlier",
	}
	assert.DeepEqual(t, expectedOrder, gotNames)
}
