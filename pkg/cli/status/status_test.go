package status

import (
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

var shaValues = []string{"1234", "abcd"}

func TestGetRunStatus(t *testing.T) {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace1",
		},
	}
	repo1 := v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo1",
			Namespace: namespace.GetName(),
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: nil,
			URL:         "https://anurl.com/owner/repo",
		},
	}

	pipelineRun := getPipelineRun("pr", shaValues[0], repo1)
	pipelineRun1 := getPipelineRun("pr1", shaValues[1], repo1)

	tdata := testclient.Data{
		Repositories: []*v1alpha1.Repository{&repo1},
		PipelineRuns: []*tektonv1.PipelineRun{pipelineRun, pipelineRun1},
	}
	ctx, _ := rtesting.SetupFakeContext(t)
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)
	cs := &params.Run{
		Clients: clients.Clients{
			PipelineAsCode: stdata.PipelineAsCode,
			Tekton:         stdata.Pipeline,
		},
	}
	cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})

	if runStatus := GetRunStatus(ctx, cs, repo1); len(runStatus) != 2 {
		t.Errorf("got %d, want 2", len(runStatus))
	}
}

func getPipelineRun(prName, sha string, repo1 v1alpha1.Repository) *tektonv1.PipelineRun {
	cw := clockwork.NewFakeClock()
	return &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      prName,
			Namespace: repo1.GetNamespace(),
			Labels: map[string]string{
				keys.Repository: repo1.GetName(),
				keys.SHA:        sha,
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			PipelineRef: nil,
		},
		Status: tektonv1.PipelineRunStatus{
			PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
				StartTime: &metav1.Time{Time: cw.Now().Add(-16 * time.Minute)},
			},
		},
	}
}
