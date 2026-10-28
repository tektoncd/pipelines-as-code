package prompt

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestSelectRepo(t *testing.T) {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace1",
		},
	}
	repo1 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo1",
			Namespace: namespace.GetName(),
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: nil,
			URL:         "https://anurl.com/owner/repo",
		},
	}
	repo2 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo2",
			Namespace: namespace.GetName(),
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: nil,
			URL:         "https://anurl.com/owner1/repo1",
		},
	}
	tests := []struct {
		name         string
		askStubs     func(*AskStubber)
		repositories []*v1alpha1.Repository
		repoName     string
		wantRepo     *v1alpha1.Repository
		wantError    string
	}{{
		name:      "When no repository exist",
		wantRepo:  nil,
		wantError: "no repo found",
	}, {
		name:         "When one repository exist",
		repositories: []*v1alpha1.Repository{repo1},
		wantRepo:     repo1,
		wantError:    "",
	}, {
		name: "When more than one repository exist",
		askStubs: func(as *AskStubber) {
			as.StubOne("repo2")
		},
		repositories: []*v1alpha1.Repository{repo1, repo2},
		wantRepo:     repo2,
		wantError:    "",
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as, teardown := InitAskStubber()
			defer teardown()
			if tt.askStubs != nil {
				tt.askStubs(as)
			}
			tdata := testclient.Data{
				Repositories: tt.repositories,
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
				},
			}
			gotRepo, gotErr := SelectRepo(ctx, cs, namespace.GetName())
			if gotErr != nil {
				if resError := cmp.Diff(gotErr.Error(), tt.wantError); resError != "" {
					t.Errorf("Diff %s:", resError)
				}
			}
			if res := cmp.Diff(gotRepo, tt.wantRepo); res != "" {
				t.Errorf("Diff %s:", res)
			}
		})
	}
}
