package formatting

import (
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	knativeapis "knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
)

func makeRepoStatus(prname, sha, conditionReason string, cw *clockwork.FakeClock, started, completed time.Duration) v1alpha1.RepositoryRunStatus {
	return v1alpha1.RepositoryRunStatus{
		Status: knativeduckv1.Status{
			Conditions: []knativeapis.Condition{
				{
					Reason: conditionReason,
				},
			},
		},
		PipelineRunName: prname,
		StartTime:       &metav1.Time{Time: cw.Now().Add(started)},
		CompletionTime:  &metav1.Time{Time: cw.Now().Add(completed)},
		SHA:             github.Ptr(sha),
		SHAURL:          github.Ptr("https://anurl.com/repo/owner/commit/SHA"),
		Title:           github.Ptr("A title"),
		EventType:       github.Ptr("pull_request"),
		TargetBranch:    github.Ptr("TargetBranch"),
		LogURL:          github.Ptr("https://help.me.obiwan.kenobi"),
	}
}

func TestShowLastAge(t *testing.T) {
	cw := clockwork.NewFakeClock()
	tests := []struct {
		name       string
		want       string
		repository v1alpha1.Repository
	}{
		{
			name: "show last age",
			repository: v1alpha1.Repository{
				Status: []v1alpha1.RepositoryRunStatus{
					makeRepoStatus("firstfinished", "sha1", "Success", cw, -20*time.Minute, -25*time.Minute),
					makeRepoStatus("lastfinished", "sha2", "Success", cw, -10*time.Minute, -15*time.Minute),
				},
			},
			want: "15 minutes ago",
		},
		{
			name: "non status",
			repository: v1alpha1.Repository{
				Status: []v1alpha1.RepositoryRunStatus{},
			},
			want: nonAttributedStr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShowLastAge(tt.repository, cw); got != tt.want {
				t.Errorf("ShowLastAge() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShowLastSHA(t *testing.T) {
	cw := clockwork.NewFakeClock()
	tests := []struct {
		repository v1alpha1.Repository
		name       string
		want       string
	}{
		{
			name: "show last sha",
			repository: v1alpha1.Repository{
				Status: []v1alpha1.RepositoryRunStatus{
					makeRepoStatus("firstfinished", "sha1", "Success", cw, -20*time.Minute, -25*time.Minute),
					makeRepoStatus("lastfinished", "shalast", "Success", cw, -10*time.Minute, -15*time.Minute),
				},
			},
			want: "shalast",
		},
		{
			name: "non status",
			repository: v1alpha1.Repository{
				Status: []v1alpha1.RepositoryRunStatus{},
			},
			want: nonAttributedStr,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShowLastSHA(tt.repository); got != tt.want {
				t.Errorf("ShowLastSHA() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShowStatus(t *testing.T) {
	cw := clockwork.NewFakeClock()
	tests := []struct {
		name       string
		repository v1alpha1.Repository
		want       string
	}{
		{
			name: "show status",
			repository: v1alpha1.Repository{
				Status: []v1alpha1.RepositoryRunStatus{
					makeRepoStatus("firstfinished", "sha1", "Success", cw, -20*time.Minute, -25*time.Minute),
					makeRepoStatus("lastfinished", "shalast", "LastSuccess", cw, -10*time.Minute, -15*time.Minute),
				},
			},
			want: "LastSuccess",
		},
		{
			name: "non status",
			repository: v1alpha1.Repository{
				Status: []v1alpha1.RepositoryRunStatus{},
			},
			want: "NoRun",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShowStatus(tt.repository, cli.NewColorScheme(false, false)); got != tt.want {
				t.Errorf("ShowStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}
