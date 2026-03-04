package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	pipelinerunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1/pipelinerun"
	tektonv1lister "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"
	"knative.dev/pkg/system"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/customparams"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	pacapi "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/listers/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/llm"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	pac "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelineascode"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
)

// Reconciler implements controller.Reconciler for PipelineRun resources.
type Reconciler struct {
	run               *params.Run
	repoLister        pacapi.RepositoryLister
	pipelineRunLister tektonv1lister.PipelineRunLister
	kinteract         kubeinteraction.Interface
	qm                queuepkg.ManagerInterface
	metrics           *prmetrics.Recorder
	eventEmitter      *events.EventEmitter
	globalRepo        *v1alpha1.Repository
	secretNS          string
}

var (
	_ pipelinerunreconciler.Interface = (*Reconciler)(nil)
	_ pipelinerunreconciler.Finalizer = (*Reconciler)(nil)
)

// ReconcileKind is the main entry point for reconciling PipelineRun resources.
func (r *Reconciler) ReconcileKind(ctx context.Context, pr *tektonv1.PipelineRun) pkgreconciler.Event {
	ctx = info.StoreNS(ctx, system.Namespace())
	logger := logging.FromContext(ctx).With("namespace", pr.GetNamespace())

	logger.Debugf("reconciling pipelineRun %s/%s", pr.GetNamespace(), pr.GetName())

	// make sure we have the latest pipelinerun to reconcile, since there is something updating at the same time
	lpr, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(pr.GetNamespace()).Get(ctx, pr.GetName(), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("cannot get pipelineRun: %w", err)
	}

	if lpr.GetResourceVersion() != pr.GetResourceVersion() {
		logger.Debugf("Skipping reconciliation, pipelineRun was updated (cached version %s vs fresh version %s)", pr.GetResourceVersion(), lpr.GetResourceVersion())
		return nil
	}

	// if pipelineRun is in completed or failed state then return
	state, exist := pr.GetAnnotations()[keys.State]
	if exist && (state == kubeinteraction.StateCompleted || state == kubeinteraction.StateFailed) {
		return nil
	}

	reason := ""
	if len(pr.Status.GetConditions()) > 0 {
		reason = pr.Status.GetConditions()[0].GetReason()
	}
	// This condition handles cases where the PipelineRun has entered a "Running" state,
	// but its status in the Git provider remains "queued" (e.g., due to updates made by
	// another controller outside PaC). To maintain consistency between the PipelineRun
	// status and the Git provider status, we update both the PipelineRun resource and
	// the corresponding status on the Git provider here.
	scmReportingPLRStarted, exist := pr.GetAnnotations()[keys.SCMReportingPLRStarted]
	startReported := exist && scmReportingPLRStarted == "true"
	logger.Debugf("pipelineRun %s/%s scmReportingPLRStarted=%v, exist=%v", pr.GetNamespace(), pr.GetName(), startReported, exist)

	if reason == string(tektonv1.PipelineRunReasonRunning) && !startReported {
		logger.Infof("pipelineRun %s/%s is running but not yet reported to provider, updating status", pr.GetNamespace(), pr.GetName())
		repoName := pr.GetAnnotations()[keys.Repository]
		repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
		if err != nil {
			return fmt.Errorf("failed to get repository CR: %w", err)
		}
		return r.updatePipelineRunToInProgress(ctx, logger, repo, pr)
	}
	logger.Debugf("pipelineRun %s/%s condition not met: reason='%s', startReported=%v", pr.GetNamespace(), pr.GetName(), reason, startReported)

	// if its a GitHub App pipelineRun PR then process only if check run id is added otherwise wait
	if _, ok := pr.Annotations[keys.InstallationID]; ok {
		if _, ok := pr.Annotations[keys.CheckRunID]; !ok {
			return nil
		}
	}

	// queue pipelines which are in queued state and pending status
	// if status is not pending, it could be cancelled so let it be reported, even if state is queued
	if state == kubeinteraction.StateQueued && pr.Spec.Status == tektonv1.PipelineRunSpecStatusPending {
		return r.queuePipelineRun(ctx, logger, pr)
	}

	if !pr.IsDone() && !pr.IsCancelled() {
		return nil
	}

	// If we have a controllerInfo annotation, then we need to get the
	// configmap configuration for it
	//
	// The annotation is a json string with a label, the pac controller
	// configmap and the GitHub app secret .
	//
	// We always assume the controller is in the same namespace as the original
	// controller but that may changes
	if controllerInfo, ok := pr.GetAnnotations()[keys.ControllerInfo]; ok {
		var parsedControllerInfo *info.ControllerInfo
		if err := json.Unmarshal([]byte(controllerInfo), &parsedControllerInfo); err != nil {
			return fmt.Errorf("failed to parse controllerInfo: %w", err)
		}
		r.run.Info.Controller = parsedControllerInfo
	} else {
		r.run.Info.Controller = info.GetControllerInfoFromEnvOrDefault()
	}

	ctx = info.StoreCurrentControllerName(ctx, r.run.Info.Controller.Name)

	logFields := []interface{}{
		"pipeline-run", pr.GetName(),
		"event-sha", pr.GetAnnotations()[keys.SHA],
	}

	// Add source repository URL if available
	if repoURL := pr.GetAnnotations()[keys.RepoURL]; repoURL != "" {
		logFields = append(logFields, "source-repo-url", repoURL)
	}

	// Add branch information if available
	if targetBranch := pr.GetAnnotations()[keys.Branch]; targetBranch != "" {
		logFields = append(logFields, "target-branch", targetBranch)
		if sourceBranch := pr.GetAnnotations()[keys.SourceBranch]; sourceBranch != "" && sourceBranch != targetBranch {
			logFields = append(logFields, "source-branch", sourceBranch)
		}
	}

	// Add event type information if available
	if eventType := pr.GetAnnotations()[keys.EventType]; eventType != "" {
		logFields = append(logFields, "event-type", eventType)
	}

	logger = logger.With(logFields...)
	logger.Infof("pipelineRun %v/%v is done, reconciling to report status!  ", pr.GetNamespace(), pr.GetName())
	r.eventEmitter.SetLogger(logger)

	// use same pac opts across the reconciliation
	pacInfo := r.run.Info.GetPacOpts()

	detectedProvider, event, err := r.detectProvider(ctx, logger, pr)
	if err != nil {
		msg := fmt.Sprintf("detectProvider: %v", err)
		r.eventEmitter.EmitMessage(nil, zap.ErrorLevel, "RepositoryDetectProvider", msg)
		return nil
	}
	detectedProvider.SetPacInfo(&pacInfo)

	if repo, err := r.reportFinalStatus(ctx, logger, &pacInfo, event, pr, detectedProvider); err != nil {
		msg := fmt.Sprintf("report status: %v", err)
		r.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryReportFinalStatus", msg)
		return err
	}
	return nil
}

func (r *Reconciler) reportFinalStatus(ctx context.Context, logger *zap.SugaredLogger, pacInfo *info.PacOpts, event *info.Event, pr *tektonv1.PipelineRun, provider provider.Interface) (*v1alpha1.Repository, error) {
	repoName := pr.GetAnnotations()[keys.Repository]
	repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
	if err != nil {
		return nil, fmt.Errorf("reportFinalStatus: %w", err)
	}

	r.secretNS = repo.GetNamespace()
	if r.globalRepo, err = r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); err == nil && r.globalRepo != nil {
		if repo.Spec.GitProvider != nil && repo.Spec.GitProvider.Secret == nil && r.globalRepo.Spec.GitProvider != nil && r.globalRepo.Spec.GitProvider.Secret != nil {
			r.secretNS = r.globalRepo.GetNamespace()
		}
		repo.Spec.Merge(r.globalRepo.Spec)
	}

	cp := customparams.NewCustomParams(event, repo, r.run, r.kinteract, r.eventEmitter, nil)
	maptemplate, _, err := cp.GetParams(ctx)
	if err != nil {
		r.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "ParamsError",
			fmt.Sprintf("error processing repository CR custom params: %s", err.Error()))
	}
	r.run.Clients.ConsoleUI().SetParams(maptemplate)

	if event.InstallationID > 0 {
		event.Provider.WebhookSecret, _ = pac.GetCurrentNSWebhookSecret(ctx, r.kinteract, r.run)
	} else {
		secretFromRepo := pac.SecretFromRepository{
			K8int:       r.kinteract,
			Config:      provider.GetConfig(),
			Event:       event,
			Repo:        repo,
			WebhookType: pacInfo.WebhookType,
			Logger:      logger,
			Namespace:   r.secretNS,
		}
		if err := secretFromRepo.Get(ctx); err != nil {
			return repo, fmt.Errorf("cannot get secret from repository: %w", err)
		}
	}

	if r.run.Clients.Log == nil {
		r.run.Clients.Log = logger
	}
	err = provider.SetClient(ctx, r.run, event, repo, r.eventEmitter)
	if err != nil {
		return repo, fmt.Errorf("cannot set client: %w", err)
	}

	finalState := kubeinteraction.StateCompleted
	newPr, err := r.postFinalStatus(ctx, logger, pacInfo, provider, event, pr)
	if err != nil {
		logger.Errorf("failed to post final status, moving on: %v", err)
		finalState = kubeinteraction.StateFailed
	}

	// Perform LLM analysis only for failed pipeline runs (best-effort, non-blocking)
	// Users can use CEL expressions in role configurations for more fine-grained control
	if len(newPr.Status.Conditions) > 0 && newPr.Status.Conditions[0].Status == corev1.ConditionFalse {
		if err := r.performLLMAnalysis(ctx, logger, repo, newPr, event, provider); err != nil {
			logger.Warnf("LLM analysis failed (non-blocking): %v", err)
			r.eventEmitter.EmitMessage(repo, zap.WarnLevel, "LLMAnalysisFailed",
				fmt.Sprintf("AI/LLM analysis failed for repository %s/%s and pipeline run %s: %v", repo.Namespace, repo.Name, newPr.Name, err))
		}
	}

	if err := r.updateRepoRunStatus(ctx, logger, newPr, repo, event); err != nil {
		return repo, fmt.Errorf("cannot update run status: %w", err)
	}

	if _, err := r.updatePipelineRunState(ctx, logger, pr, finalState); err != nil {
		return repo, fmt.Errorf("cannot update state: %w", err)
	}

	if err := r.emitMetrics(ctx, pr); err != nil {
		logger.Error("failed to emit metrics: ", err)
	}

	emitTimingSpans(pr)

	// remove pipelineRun from Queue and start the next one
	for {
		next := r.qm.RemoveAndTakeItemFromQueue(repo, pr)
		if next == "" {
			break
		}
		key := strings.Split(next, "/")
		pr, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(key[0]).Get(ctx, key[1], metav1.GetOptions{})
		if err != nil {
			logger.Errorf("cannot get pipeline for next in queue: %w", err)
			continue
		}

		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			logger.Errorf("failed to update status: %w", err)
			_ = r.qm.RemoveFromQueue(queuepkg.RepoKey(repo), queuepkg.PrKey(pr))
			continue
		}
		break
	}

	if err := r.cleanupPipelineRuns(ctx, logger, pacInfo, repo, pr); err != nil {
		return repo, fmt.Errorf("error cleaning pipelineruns: %w", err)
	}

	return repo, nil
}

func (r *Reconciler) updatePipelineRunToInProgress(ctx context.Context, logger *zap.SugaredLogger, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) error {
	pr, err := r.updatePipelineRunState(ctx, logger, pr, kubeinteraction.StateStarted)
	if err != nil {
		return fmt.Errorf("cannot update state: %w", err)
	}

	detectedProvider, event, err := r.initGitProviderClient(ctx, logger, repo, pr)
	if err != nil {
		return fmt.Errorf("cannot initialize git provider client: %w", err)
	}

	consoleURL := r.run.Clients.ConsoleUI().DetailURL(pr)

	mt := formatting.MessageTemplate{
		PipelineRunName: pr.GetName(),
		Namespace:       repo.GetNamespace(),
		ConsoleName:     r.run.Clients.ConsoleUI().GetName(),
		ConsoleURL:      consoleURL,
		TknBinary:       settings.TknBinaryName,
		TknBinaryURL:    settings.TknBinaryURL,
	}
	msg, err := mt.MakeTemplate(detectedProvider.GetTemplate(provider.StartingPipelineType))
	if err != nil {
		return fmt.Errorf("cannot create message template: %w", err)
	}
	status := providerstatus.StatusOpts{
		Status:                  "in_progress",
		Conclusion:              providerstatus.ConclusionPending,
		Text:                    msg,
		DetailsURL:              consoleURL,
		PipelineRunName:         pr.GetName(),
		PipelineRun:             pr,
		OriginalPipelineRunName: pr.GetAnnotations()[keys.OriginalPRName],
	}

	if err := createStatusWithRetry(ctx, logger, detectedProvider, event, status); err != nil {
		// if failed to report status for running state, let the pipelineRun continue,
		// pipelineRun is already started so we will try again once it completes
		logger.Errorf("failed to report status to running on provider continuing! error: %v", err)
		return nil
	}

	logger.Info("updated in_progress status on provider platform for pipelineRun ", pr.GetName())
	return nil
}

func (r *Reconciler) initGitProviderClient(ctx context.Context, logger *zap.SugaredLogger, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) (provider.Interface, *info.Event, error) {
	pacInfo := r.run.Info.GetPacOpts()
	detectedProvider, event, err := r.detectProvider(ctx, logger, pr)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot detect provider: %w", err)
	}
	detectedProvider.SetPacInfo(&pacInfo)

	// installation ID indicates Github App installation
	if event.InstallationID > 0 {
		event.Provider.WebhookSecret, _ = pac.GetCurrentNSWebhookSecret(ctx, r.kinteract, r.run)
	} else {
		// secretNS is needed when git provider is other than Github App.
		secretNS := repo.GetNamespace()
		if repo.Spec.GitProvider != nil && repo.Spec.GitProvider.Secret == nil && r.globalRepo != nil && r.globalRepo.Spec.GitProvider != nil && r.globalRepo.Spec.GitProvider.Secret != nil {
			secretNS = r.globalRepo.GetNamespace()
		}

		secretFromRepo := pac.SecretFromRepository{
			K8int:       r.kinteract,
			Config:      detectedProvider.GetConfig(),
			Event:       event,
			Repo:        repo,
			WebhookType: pacInfo.WebhookType,
			Logger:      logger,
			Namespace:   secretNS,
		}
		if err := secretFromRepo.Get(ctx); err != nil {
			return nil, nil, fmt.Errorf("cannot get secret from repository: %w", err)
		}
	}

	err = detectedProvider.SetClient(ctx, r.run, event, repo, r.eventEmitter)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot set client: %w", err)
	}

	return detectedProvider, event, nil
}

func (r *Reconciler) updatePipelineRunState(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun, state string) (*tektonv1.PipelineRun, error) {
	currentState := pr.GetAnnotations()[keys.State]
	logger.Infof("updating pipelineRun %v/%v state from %s to %s", pr.GetNamespace(), pr.GetName(), currentState, state)
	annotations := map[string]string{
		keys.State: state,
	}
	if state == kubeinteraction.StateStarted {
		annotations[keys.SCMReportingPLRStarted] = "true"
	}

	mergePatch := map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{
				keys.State: state,
			},
			"annotations": annotations,
		},
	}

	// if state is started then remove pipelineRun pending status
	if state == kubeinteraction.StateStarted {
		mergePatch["spec"] = map[string]any{
			"status": "",
		}
	}
	actionLog := state + " state"
	patchedPR, err := action.PatchPipelineRun(ctx, logger, actionLog, r.run.Clients.Tekton, pr, mergePatch)
	if err != nil {
		return pr, fmt.Errorf("error patching the pipelinerun: %w", err)
	}
	return patchedPR, nil
}

// performLLMAnalysis executes LLM analysis on the completed pipeline if configured.
func (r *Reconciler) performLLMAnalysis(
	ctx context.Context,
	logger *zap.SugaredLogger,
	repo *v1alpha1.Repository,
	pr *tektonv1.PipelineRun,
	event *info.Event,
	provider provider.Interface,
) error {
	orchestrator := llm.NewOrchestrator(r.run, r.kinteract, logger)
	return orchestrator.ExecuteAnalysis(ctx, repo, pr, event, provider)
}
