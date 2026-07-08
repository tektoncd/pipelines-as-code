package status

import (
	"context"
	"regexp"
	"sort"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	kstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

// snatched from prow
// https://github.com/kubernetes/test-infra/blob/3c8cbed65c421670a7d37239b8ffceb91e0eb16b/prow/spyglass/lenses/buildlog/lens.go#L95
var (
	ErorrRE                                       = regexp.MustCompile(`timed out|ERROR:|(FAIL|Failure \[)\b|panic\b|^E\d{4} \d\d:\d\d:\d\d\.\d\d\d]`)
	defaultNumLinesOfLogsInContainersToGrabForErr = int64(10)
)

type RunStatus struct {
	PipelineRunName    string
	StartTime          *metav1.Time
	CompletionTime     *metav1.Time
	SHA                string
	SHAURL             string
	Title              string
	LogURL             string
	TargetBranch       string
	EventType          string
	Reason             string
	CollectedTaskInfos map[string]pacv1alpha1.TaskInfos
}

func convertPRToRunStatus(ctx context.Context, cs *params.Run, pr tektonv1.PipelineRun, logurl string) RunStatus {
	kinteract, _ := kubeinteraction.NewKubernetesInteraction(cs)
	failurereasons := kstatus.CollectFailedTasksLogSnippet(ctx, cs, kinteract, &pr, defaultNumLinesOfLogsInContainersToGrabForErr)

	reason := ""
	if cond := pr.Status.GetCondition(apis.ConditionSucceeded); cond != nil {
		reason = cond.Reason
	}

	return RunStatus{
		PipelineRunName:    pr.GetName(),
		StartTime:          pr.Status.StartTime,
		CompletionTime:     pr.Status.CompletionTime,
		SHA:                pr.GetAnnotations()[keys.SHA],
		SHAURL:             pr.GetAnnotations()[keys.ShaURL],
		Title:              pr.GetAnnotations()[keys.ShaTitle],
		LogURL:             logurl,
		TargetBranch:       pr.GetAnnotations()[keys.Branch],
		EventType:          pr.GetAnnotations()[keys.EventType],
		Reason:             reason,
		CollectedTaskInfos: failurereasons,
	}
}

func sortRunStatuses(statuses []RunStatus) {
	sort.Slice(statuses, func(i, j int) bool {
		if statuses[j].StartTime == nil {
			return false
		}
		if statuses[i].StartTime == nil {
			return true
		}
		return statuses[j].StartTime.Before(statuses[i].StartTime)
	})
}

func GetRunStatus(ctx context.Context, cs *params.Run, repository pacv1alpha1.Repository) []RunStatus {
	label := keys.Repository + "=" + repository.Name
	prs, err := cs.Clients.Tekton.TektonV1().PipelineRuns(repository.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: label,
	})
	if err != nil {
		return nil
	}

	var statuses []RunStatus
	for i := range prs.Items {
		pr := prs.Items[i]
		logurl := cs.Clients.ConsoleUI().DetailURL(&pr)
		statuses = append(statuses, convertPRToRunStatus(ctx, cs, pr, logurl))
	}
	sortRunStatuses(statuses)
	return statuses
}
