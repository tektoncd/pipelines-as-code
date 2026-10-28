package sort

import (
	"testing"
	"time"

	pacv1alpha1 "github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTaskInfos(t *testing.T) {
	type args struct {
		taskinfos map[string]pacv1alpha1.TaskInfos
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "test sort",
			args: args{
				taskinfos: map[string]pacv1alpha1.TaskInfos{
					"task-after": {
						Name: "after",
						CompletionTime: &metav1.Time{
							Time: time.Date(2021, 1, 2, 0, 0, 0, 0, time.UTC),
						},
					},
					"task-before": {
						Name: "before",
						CompletionTime: &metav1.Time{
							Time: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
						},
					},
				},
			},
			want: "before",
		},
		{
			name: "same completion time sorts by name",
			args: args{
				taskinfos: map[string]pacv1alpha1.TaskInfos{
					"task-zebra": {
						Name: "zebra",
						CompletionTime: &metav1.Time{
							Time: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
						},
					},
					"task-alpha": {
						Name: "alpha",
						CompletionTime: &metav1.Time{
							Time: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC),
						},
					},
				},
			},
			want: "alpha",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TaskInfos(tt.args.taskinfos)
			assert.Equal(t, tt.want, got[0].Name)
		})
	}
}
