package reconciler

import (
	"context"
	"fmt"
	"strings"
	"time"

	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
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

var backoffSchedule = []time.Duration{
	1 * time.Second,
	3 * time.Second,
	5 * time.Second,
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
