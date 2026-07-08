package sort

import (
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSortRepositories(t *testing.T) {
	repositories := []v1alpha1.Repository{
		{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(metav1.Now().Add(-2 * time.Minute)),
			},
			Spec: v1alpha1.RepositorySpec{URL: "https://middle/one"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(metav1.Now().Add(-3 * time.Minute)),
			},
			Spec: v1alpha1.RepositorySpec{URL: "https://first/one"},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				CreationTimestamp: metav1.NewTime(metav1.Now().Add(-1 * time.Minute)),
			},
			Spec: v1alpha1.RepositorySpec{URL: "https://last/one"},
		},
	}

	RepositorySortByCreationOldestTime(repositories)
	assert.Equal(t, repositories[0].Spec.URL, "https://first/one", repositories[0].Spec.URL)
	assert.Equal(t, repositories[1].Spec.URL, "https://middle/one", repositories[1].Spec.URL)
	assert.Equal(t, repositories[2].Spec.URL, "https://last/one", repositories[1].Spec.URL)
}
