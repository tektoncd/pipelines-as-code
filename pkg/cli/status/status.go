package status

import (
	"context"
	"regexp"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	kstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	sortrepostatus "github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// snatched from prow
// https://github.com/kubernetes/test-infra/blob/3c8cbed65c421670a7d37239b8ffceb91e0eb16b/prow/spyglass/lenses/buildlog/lens.go#L95
var (
	ErorrRE                                       = regexp.MustCompile(`timed out|ERROR:|(FAIL|Failure \[)\b|panic\b|^E\d{4} \d\d:\d\d:\d\d\.\d\d\d]`)
	defaultNumLinesOfLogsInContainersToGrabForErr = int64(10)
)

// RepositoryRunStatusRemoveSameSHA remove an existing status with the same
// SHA. This would come from repo pipelinerun_status. We don't want the doublons
// and we rather use the ones from the live PR on cluster.
func RepositoryRunStatusRemoveSameSHA(rs []pacv1alpha1.RepositoryRunStatus, livePrSHA string) []pacv1alpha1.RepositoryRunStatus {
	newRepositoryStatus := []pacv1alpha1.RepositoryRunStatus{}
	for _, value := range rs {
		if value.SHA != nil && *value.SHA == livePrSHA {
			continue
		}
		newRepositoryStatus = append(newRepositoryStatus, value)
	}
	return newRepositoryStatus
}

func convertPrStatusToRepositoryStatus(ctx context.Context, cs *params.Run, pr tektonv1.PipelineRun, logurl string) pacv1alpha1.RepositoryRunStatus {
	kinteract, _ := kubeinteraction.NewKubernetesInteraction(cs)
	failurereasons := kstatus.CollectFailedTasksLogSnippet(ctx, cs, kinteract, &pr, defaultNumLinesOfLogsInContainersToGrabForErr)
	prSHA := pr.GetAnnotations()[keys.SHA]
	return pacv1alpha1.RepositoryRunStatus{
		Status:             pr.Status.Status,
		LogURL:             &logurl,
		PipelineRunName:    pr.GetName(),
		CollectedTaskInfos: &failurereasons,
		StartTime:          pr.Status.StartTime,
		SHA:                github.Ptr(prSHA),
		SHAURL:             github.Ptr(pr.GetAnnotations()[keys.ShaURL]),
		Title:              github.Ptr(pr.GetAnnotations()[keys.ShaTitle]),
		TargetBranch:       github.Ptr(pr.GetAnnotations()[keys.Branch]),
		EventType:          github.Ptr(pr.GetAnnotations()[keys.EventType]),
	}
}

func MixLivePRandRepoStatus(ctx context.Context, cs *params.Run, repository pacv1alpha1.Repository) []pacv1alpha1.RepositoryRunStatus {
	repositorystatus := repository.Status
	label := keys.Repository + "=" + repository.Name
	prs, err := cs.Clients.Tekton.TektonV1().PipelineRuns(repository.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: label,
	})
	if err != nil {
		return sortrepostatus.RepositorySortRunStatus(repositorystatus)
	}

	for i := range prs.Items {
		pr := prs.Items[i]
		repositorystatus = RepositoryRunStatusRemoveSameSHA(repositorystatus, pr.GetAnnotations()[keys.SHA])
		logurl := cs.Clients.ConsoleUI().DetailURL(&pr)
		repositorystatus = append(repositorystatus, convertPrStatusToRepositoryStatus(ctx, cs, pr, logurl))
	}
	return sortrepostatus.RepositorySortRunStatus(repositorystatus)
}
