package sort

import (
	"sort"

	pacv1alpha1 "github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
)

// From tekton cli prsort package.
type repositorySortByCompletionTime []pacv1alpha1.Repository

func (repositorys repositorySortByCompletionTime) Len() int { return len(repositorys) }
func (repositorys repositorySortByCompletionTime) Swap(i, j int) {
	repositorys[j], repositorys[i] = repositorys[i], repositorys[j]
}

func (repositorys repositorySortByCompletionTime) Less(i, j int) bool {
	return repositorys[j].CreationTimestamp.After(repositorys[i].CreationTimestamp.Time)
}

func RepositorySortByCreationOldestTime(repositorys []pacv1alpha1.Repository) {
	sort.Sort(repositorySortByCompletionTime(repositorys))
}
