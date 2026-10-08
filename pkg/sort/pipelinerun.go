package sort

import (
	"sort"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// From tekton cli prsort package.
type prSortByCompletionTime []tektonv1.PipelineRun

func (prs prSortByCompletionTime) Len() int      { return len(prs) }
func (prs prSortByCompletionTime) Swap(i, j int) { prs[i], prs[j] = prs[j], prs[i] }
func (prs prSortByCompletionTime) Less(i, j int) bool {
	if prs[j].Status.CompletionTime == nil {
		return false
	}
	if prs[i].Status.CompletionTime == nil {
		return true
	}
	return prs[j].Status.CompletionTime.Before(prs[i].Status.CompletionTime)
}

func PipelineRunSortByCompletionTime(items []tektonv1.PipelineRun) []tektonv1.PipelineRun {
	sort.Sort(prSortByCompletionTime(items))
	return items
}

func PipelineRunSortByStartTime(prs []tektonv1.PipelineRun) {
	sort.Sort(byStartTime(prs))
}

type byStartTime []tektonv1.PipelineRun

func (prs byStartTime) Len() int      { return len(prs) }
func (prs byStartTime) Swap(i, j int) { prs[i], prs[j] = prs[j], prs[i] }
func (prs byStartTime) Less(i, j int) bool {
	// PipelineRuns that have not started yet are listed first
	if prs[i].Status.StartTime == nil {
		return true
	}
	if prs[j].Status.StartTime == nil {
		return false
	}
	// If times are equal, sort by name descending
	if prs[j].Status.StartTime.Time.Equal(prs[i].Status.StartTime.Time) {
		return prs[i].GetName() > prs[j].GetName()
	}
	return prs[j].Status.StartTime.Before(prs[i].Status.StartTime)
}
