package reconciler

import (
	"context"
	"fmt"
	"strings"
	"time"

	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
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

// maxCheckRunOutputTextBytes is GitHub's limit on a check run's output text.
// GitHub counts it in bytes, not characters.
const maxCheckRunOutputTextBytes = 65535

// getFailureSnippet renders one block per failed task, in completion order,
// with secrets attached to the PipelineRun masked. It stops adding blocks
// before the snippet passes maxBytes and ends with a line counting the failed
// tasks it left out. The first failed task is always shown, even when its
// block alone is longer than maxBytes, as it was before more were shown.
func (r *Reconciler) getFailureSnippet(ctx context.Context, pr *tektonv1.PipelineRun, maxBytes int) string {
	lines := int64(settings.DefaultSettings().ErrorLogSnippetNumberOfLines)
	if r.run.Info.Pac != nil {
		lines = int64(r.run.Info.Pac.ErrorLogSnippetNumberOfLines)
	}
	taskinfos := kstatus.CollectFailedTasksLogSnippet(ctx, r.run, r.kinteract, pr, lines)
	if len(taskinfos) == 0 {
		return ""
	}
	sortedTaskInfos := sort.TaskInfos(taskinfos)
	secretValues := secrets.GetSecretsAttachedToPipelineRun(ctx, r.kinteract, pr)

	blocks := make([]string, 0, len(sortedTaskInfos))
	size := 0
	for i, ti := range sortedTaskInfos {
		block := secrets.ReplaceSecretsInText(failedTaskBlock(ti), secretValues)
		needed := size + len(block)
		if i > 0 {
			needed++ // newline before the block
		}
		// Keep room for the line about the tasks after this one.
		if left := len(sortedTaskInfos) - i - 1; left > 0 {
			needed += 1 + len(moreFailedTasksLine(left))
		}
		if i > 0 && needed > maxBytes {
			blocks = append(blocks, moreFailedTasksLine(len(sortedTaskInfos)-i))
			break
		}
		blocks = append(blocks, block)
		size += len(block)
		if i > 0 {
			size++
		}
	}
	return strings.Join(blocks, "\n")
}

func failedTaskBlock(ti pacv1alpha1.TaskInfos) string {
	text := strings.TrimSpace(ti.LogSnippet)
	if text == "" {
		text = ti.Message
	}
	name := ti.Name
	if ti.DisplayName != "" {
		name = strings.ToLower(ti.DisplayName)
	}
	return fmt.Sprintf("task <b>%s</b> has the status <b>\"%s\"</b>:\n<pre>%s</pre>", name, ti.Reason, text)
}

func moreFailedTasksLine(n int) string {
	if n == 1 {
		return "and 1 more failed task"
	}
	return fmt.Sprintf("and %d more failed tasks", n)
}

// detailURL returns the console URL for a PipelineRun on the paths that do not
// resolve the repository custom parameters themselves. The URL recorded on the
// PipelineRun when it started was rendered with those parameters, so it is
// preferred over rendering a new one from the unscoped console.
func (r *Reconciler) detailURL(pr *tektonv1.PipelineRun) string {
	if logURL := pr.GetAnnotations()[apipac.LogURL]; logURL != "" {
		return logURL
	}
	return r.run.Clients.ConsoleUI().DetailURL(pr)
}

func (r *Reconciler) postFinalStatus(ctx context.Context, logger *zap.SugaredLogger, pacInfo *info.PacOpts, vcx provider.Interface, event *info.Event, createdPR *tektonv1.PipelineRun, console consoleui.Interface) (*tektonv1.PipelineRun, map[string]*tektonv1.PipelineRunTaskRunStatus, error) {
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
		taskStatusText, err = sort.TaskStatusTmpl(pr, trStatus, r.run, pacInfo, vcx.GetConfig())
		if err != nil {
			return pr, trStatus, err
		}
	} else {
		taskStatusText = pr.Status.GetCondition(apis.ConditionSucceeded).Message
	}

	namespaceURL := console.NamespaceURL(pr)
	consoleURL := console.DetailURL(pr)
	mt := formatting.MessageTemplate{
		PipelineRunName: pr.GetName(),
		Namespace:       pr.GetNamespace(),
		NamespaceURL:    namespaceURL,
		ConsoleName:     console.GetName(),
		ConsoleURL:      consoleURL,
		TknBinary:       settings.TknBinaryName,
		TknBinaryURL:    settings.TknBinaryURL,
		TaskStatus:      taskStatusText,
	}
	if pacInfo.ErrorLogSnippet {
		// Measure the rest of the status text, with the snippet's heading,
		// so the snippet can take what is left of the check run limit.
		maxSnippetBytes := maxCheckRunOutputTextBytes
		mt.FailureSnippet = "-"
		if withoutSnippet, err := mt.MakeTemplate(vcx.GetTemplate(provider.PipelineRunStatusType)); err == nil {
			maxSnippetBytes -= len(withoutSnippet) - len(mt.FailureSnippet)
		}
		mt.FailureSnippet = r.getFailureSnippet(ctx, pr, maxSnippetBytes)
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
		DetailsURL:              consoleURL,
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
