package reconciler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v84/github"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacv1a1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	kstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/pipelineascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

const (
	maxPipelineRunStatusRun = 5
)

var backoffSchedule = []time.Duration{
	1 * time.Second,
	3 * time.Second,
	5 * time.Second,
}

func (r *Reconciler) updateRepoRunStatus(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun, repo *pacv1a1.Repository, event *info.Event) error {
	refsanitized := formatting.SanitizeBranch(event.BaseBranch)
	repoStatus := pacv1a1.RepositoryRunStatus{
		Status:          pr.Status.Status,
		PipelineRunName: pr.Name,
		StartTime:       pr.Status.StartTime,
		CompletionTime:  pr.Status.CompletionTime,
		SHA:             &event.SHA,
		SHAURL:          &event.SHAURL,
		Title:           &event.SHATitle,
		LogURL:          github.Ptr(r.run.Clients.ConsoleUI().DetailURL(pr)),
		EventType:       &event.EventType,
		TargetBranch:    &refsanitized,
	}

	// Get repository again in case it was updated while we were running the CI
	// we try multiple time until we get right in case of conflicts.
	// that's what the error message tell us anyway, so i guess we listen.
	maxRun := 10
	for i := 0; i < maxRun; i++ {
		lastrepo, err := r.run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(
			pr.GetNamespace()).Get(ctx, repo.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		// Append PipelineRun status files to the repo status
		if len(lastrepo.Status) >= maxPipelineRunStatusRun {
			copy(lastrepo.Status, lastrepo.Status[len(lastrepo.Status)-maxPipelineRunStatusRun+1:])
			lastrepo.Status = lastrepo.Status[:maxPipelineRunStatusRun-1]
		}

		lastrepo.Status = append(lastrepo.Status, repoStatus)
		nrepo, err := r.run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(lastrepo.Namespace).Update(
			ctx, lastrepo, metav1.UpdateOptions{})
		if err != nil {
			logger.Infof("Could not update repo %s, retrying %d/%d: %s", lastrepo.Namespace, i, maxRun, err.Error())
			continue
		}
		logger.Infof("Repository status of %s has been updated", nrepo.Name)
		logger.Warn("The `pipelinerun_status` field in the Repository CR is scheduled for deprecation and will be removed in a future release. Please avoid relying on it.")
		return nil
	}

	return fmt.Errorf("cannot update %s", repo.Name)
}

func (r *Reconciler) getFailureSnippet(ctx context.Context, pr *tektonv1.PipelineRun) string {
	lines := int64(settings.DefaultSettings().ErrorLogSnippetNumberOfLines)
	if r.run.Info.Pac != nil {
		lines = int64(r.run.Info.Pac.ErrorLogSnippetNumberOfLines)
	}
	taskinfos := kstatus.CollectFailedTasksLogSnippet(ctx, r.run, r.kinteract, pr, lines)
	if len(taskinfos) == 0 {
		return ""
	}
	sortedTaskInfos := sort.TaskInfos(taskinfos)
	text := strings.TrimSpace(sortedTaskInfos[0].LogSnippet)
	if text == "" {
		text = sortedTaskInfos[0].Message
	}
	name := sortedTaskInfos[0].Name
	if sortedTaskInfos[0].DisplayName != "" {
		name = strings.ToLower(sortedTaskInfos[0].DisplayName)
	}
	return fmt.Sprintf("task <b>%s</b> has the status <b>\"%s\"</b>:\n<pre>%s</pre>", name, sortedTaskInfos[0].Reason, text)
}

func (r *Reconciler) postFinalStatus(ctx context.Context, logger *zap.SugaredLogger, pacInfo *info.PacOpts, vcx provider.Interface, event *info.Event, createdPR *tektonv1.PipelineRun) (*tektonv1.PipelineRun, map[string]*tektonv1.PipelineRunTaskRunStatus, error) {
	pr, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(createdPR.GetNamespace()).Get(
		ctx, createdPR.GetName(), metav1.GetOptions{},
	)
	if err != nil {
		return pr, nil, err
	}

	trStatus := kstatus.GetStatusFromTaskStatusOrFromAsking(ctx, pr, r.run)
	var taskStatusText string
	if len(trStatus) > 0 {
		var err error
		taskStatusText, err = sort.TaskStatusTmpl(pr, trStatus, r.run, vcx.GetConfig())
		if err != nil {
			return pr, trStatus, err
		}
	} else {
		taskStatusText = pr.Status.GetCondition(apis.ConditionSucceeded).Message
	}

	namespaceURL := r.run.Clients.ConsoleUI().NamespaceURL(pr)
	consoleURL := r.run.Clients.ConsoleUI().DetailURL(pr)
	mt := formatting.MessageTemplate{
		PipelineRunName: pr.GetName(),
		Namespace:       pr.GetNamespace(),
		NamespaceURL:    namespaceURL,
		ConsoleName:     r.run.Clients.ConsoleUI().GetName(),
		ConsoleURL:      consoleURL,
		TknBinary:       settings.TknBinaryName,
		TknBinaryURL:    settings.TknBinaryURL,
		TaskStatus:      taskStatusText,
	}
	if pacInfo.ErrorLogSnippet {
		failures := r.getFailureSnippet(ctx, pr)
		if failures != "" {
			secretValues := secrets.GetSecretsAttachedToPipelineRun(ctx, r.kinteract, pr)
			failures = secrets.ReplaceSecretsInText(failures, secretValues)
			mt.FailureSnippet = failures
		}
	}
	var tmplStatusText string
	if tmplStatusText, err = mt.MakeTemplate(vcx.GetTemplate(provider.PipelineRunStatusType)); err != nil {
		return nil, trStatus, fmt.Errorf("cannot create message template: %w", err)
	}

	status := status.StatusOpts{
		Status:                  pipelineascode.CompletedStatus,
		PipelineRun:             pr,
		Conclusion:              formatting.PipelineRunStatus(pr),
		Text:                    tmplStatusText,
		PipelineRunName:         pr.Name,
		DetailsURL:              r.run.Clients.ConsoleUI().DetailURL(pr),
		OriginalPipelineRunName: pr.GetAnnotations()[apipac.OriginalPRName],
	}

	err = createStatusWithRetry(ctx, logger, vcx, event, status)
	logger.Infof("pipelinerun %s has a status of '%s'", pr.Name, status.Conclusion)
	return pr, trStatus, err
}

func createStatusWithRetry(ctx context.Context, logger *zap.SugaredLogger, vcx provider.Interface, event *info.Event, statusOpts status.StatusOpts) error {
	var finalError error
	for _, backoff := range backoffSchedule {
		err := vcx.CreateStatus(ctx, event, statusOpts)
		if err == nil {
			return nil
		}
		logger.Infof("failed to create status, error: %v, retrying in %v", err, backoff)
		time.Sleep(backoff)
		finalError = err
	}
	return fmt.Errorf("failed to report status: %w", finalError)
}
