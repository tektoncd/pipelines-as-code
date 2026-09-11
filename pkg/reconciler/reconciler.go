package reconciler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	pipelinerunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1/pipelinerun"
	tektonv1lister "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
	_ "github.com/openshift-pipelines/pipelines-as-code/pkg/llm/providers/gemini" // register Gemini provider via init
	_ "github.com/openshift-pipelines/pipelines-as-code/pkg/llm/providers/openai" // register OpenAI provider via init
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
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
}

var (
	_ pipelinerunreconciler.Interface = (*Reconciler)(nil)
	_ pipelinerunreconciler.Finalizer = (*Reconciler)(nil)
)

// ErrPipelineRunNotStarted means a start has not been confirmed. It is not proof
// that the write failed: the reservation and its retry owner must be retained.
var ErrPipelineRunNotStarted = stderrors.New("pipelineRun start is not confirmed")

var errPipelineRunGone = stderrors.New("pipelineRun is no longer startable")

func copyRepositoryForMerge(repo *v1alpha1.Repository) *v1alpha1.Repository {
	if repo == nil {
		return nil
	}
	// DeepCopy never returns nil for a non-nil receiver (see the generated
	// implementation's nil-guard), so this can't actually happen.
	copied := repo.DeepCopy()
	if copied == nil {
		return nil
	}
	repo = copied
	if repo.Spec.Settings != nil {
		settings := *repo.Spec.Settings
		if settings.Policy != nil {
			policy := *settings.Policy
			policy.OkToTest = append([]string(nil), settings.Policy.OkToTest...)
			policy.PullRequest = append([]string(nil), settings.Policy.PullRequest...)
			settings.Policy = &policy
		}
		if settings.Gitlab != nil {
			gitlab := *settings.Gitlab
			settings.Gitlab = &gitlab
		}
		if settings.Github != nil {
			github := *settings.Github
			settings.Github = &github
		}
		if settings.Forgejo != nil {
			forgejo := *settings.Forgejo
			settings.Forgejo = &forgejo
		}
		if settings.AIAnalysis != nil {
			aiAnalysis := *settings.AIAnalysis
			settings.AIAnalysis = &aiAnalysis
		}
		settings.GithubAppTokenScopeRepos = append([]string(nil), settings.GithubAppTokenScopeRepos...)
		repo.Spec.Settings = &settings
	}
	if repo.Spec.GitProvider != nil {
		gitProvider := *repo.Spec.GitProvider
		if gitProvider.Secret != nil {
			secret := *gitProvider.Secret
			gitProvider.Secret = &secret
		}
		if gitProvider.WebhookSecret != nil {
			webhookSecret := *gitProvider.WebhookSecret
			gitProvider.WebhookSecret = &webhookSecret
		}
		repo.Spec.GitProvider = &gitProvider
	}
	return repo
}

// ReconcileKind is the main entry point for reconciling PipelineRun resources.
func (r *Reconciler) ReconcileKind(ctx context.Context, pr *tektonv1.PipelineRun) pkgreconciler.Event {
	reconcileRun := *r.run
	reconciler := *r
	reconciler.run = &reconcileRun
	return reconciler.reconcileKind(ctx, pr)
}

func controllerInfoForPipelineRun(pr *tektonv1.PipelineRun, fallback *info.ControllerInfo) (*info.ControllerInfo, error) {
	if controllerInfo, ok := pr.GetAnnotations()[keys.ControllerInfo]; ok {
		var parsedControllerInfo *info.ControllerInfo
		if err := json.Unmarshal([]byte(controllerInfo), &parsedControllerInfo); err != nil {
			return nil, fmt.Errorf("failed to parse controllerInfo: %w", err)
		}
		if parsedControllerInfo == nil {
			return nil, fmt.Errorf("failed to parse controllerInfo: value must not be null")
		}
		return parsedControllerInfo, nil
	}
	if fallback != nil {
		controllerInfo := *fallback
		return &controllerInfo, nil
	}
	return info.GetControllerInfoFromEnvOrDefault(), nil
}

func (r *Reconciler) reconcileKind(ctx context.Context, pr *tektonv1.PipelineRun) pkgreconciler.Event {
	ctx = info.StoreNS(ctx, system.Namespace())
	logger := logging.FromContext(ctx).With("namespace", pr.GetNamespace())

	logger.Debugf("reconciling pipelineRun %s/%s", pr.GetNamespace(), pr.GetName())

	// make sure we have the latest pipelinerun to reconcile, since there is something updating at the same time
	lpr, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(pr.GetNamespace()).Get(ctx, pr.GetName(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return r.finalizeKind(ctx, pr)
		}
		return fmt.Errorf("cannot get pipelineRun: %w", err)
	}

	if lpr.GetResourceVersion() != pr.GetResourceVersion() {
		logger.Debugf("Skipping reconciliation, pipelineRun was updated (cached version %s vs fresh version %s)", pr.GetResourceVersion(), lpr.GetResourceVersion())
		return nil
	}

	repoName := pr.GetAnnotations()[keys.Repository]
	state, exist := pr.GetAnnotations()[keys.State]
	done := exist && (state == kubeinteraction.StateCompleted || state == kubeinteraction.StateFailed)
	repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
	if err != nil {
		if errors.IsNotFound(err) {
			// Dropping the repository clears every admission it held, this one
			// included, so a done PipelineRun has nothing left to recover and
			// retrying can never make a deleted Repository reappear.
			r.qm.RemoveRepository(&v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: repoName, Namespace: pr.Namespace}})
			if done {
				return nil
			}
		}
		return fmt.Errorf("failed to get repository CR: %w", err)
	}

	// use same pac opts across the reconciliation
	pacInfo := r.run.Info.GetPacOpts()

	// If we have a controllerInfo annotation, then we need to get the
	// configmap configuration for it
	//
	// The annotation is a json string with a label, the pac controller
	// configmap and the GitHub app secret .
	//
	// We always assume the controller is in the same namespace as the original
	// controller but that may changes
	controllerInfo, err := controllerInfoForPipelineRun(pr, r.run.Info.Controller)
	if err != nil {
		return err
	}
	r.run.Info.Controller = controllerInfo

	// An admission owner can already be running/done while another member of
	// its batch still needs recovery. Resume it before state-based early returns,
	// using this owner's controller configuration for provider reporting.
	if err := r.processQueueAdmission(ctx, logger, repo, pr, nil, queuepkg.AdmissionResume); err != nil {
		return err
	}
	if done {
		r.qm.ForgetAdmission(queuepkg.RepoKey(repo), pr)
		return nil
	}

	if secretCreated, ok := pr.GetAnnotations()[keys.SecretCreated]; ok && secretCreated == "false" && pacInfo.SecretAutoCreation {
		// if secret creation is true then return anyway from createSecretForPipelineRun function
		// because it patches the PipelineRun with the secretCreated annotation so after the
		// patch success we will get another reconciliation call for the same pipelineRun.
		// Note: only return error if the error is not related to secret creation otherwise we would interrupt other operations.
		err := r.createSecretForPipelineRun(ctx, logger, pr, repo)
		if err != nil && strings.Contains(err.Error(), "creating basic auth secret") {
			return fmt.Errorf("failed to create secret for pipelineRun %s/%s: %w", pr.GetNamespace(), pr.GetName(), err)
		} else if err != nil {
			logger.Errorf("failed to create secret for pipelineRun %s/%s: %v", pr.GetNamespace(), pr.GetName(), err)
		}
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
		// A run that is cancelled or gracefully stopped still reports the
		// Running reason until Tekton catches up, and a graceful stop keeps it
		// there until the running tasks drain. It can never be started, so
		// there is nothing to retry: the promotion path treats this as
		// StartGone for the same reason. Returning the error instead would
		// requeue over a condition no retry can change.
		if err := r.updatePipelineRunToInProgress(ctx, logger, repo, pr); err != nil {
			if stderrors.Is(err, errPipelineRunGone) {
				return nil
			}
			return err
		}
		return nil
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

	detectedProvider, event, err := r.detectProvider(ctx, logger, pr)
	if err != nil {
		msg := fmt.Sprintf("detectProvider: %v", err)
		r.eventEmitter.EmitMessage(nil, zap.ErrorLevel, "RepositoryDetectProvider", msg)

		if stderrors.Is(err, ErrProviderNotConfigured) {
			// Permanent: the git-provider annotation is missing or names a
			// provider PAC does not know, so retrying detectProvider can
			// never succeed. Returning the error here would rate-limited
			// retry forever while still holding the concurrency slot; release
			// it and mark the run failed instead.
			return r.abandonDoneWithoutProvider(ctx, logger, repo, pr, err)
		}
		// Transient (e.g. a GitHub App client failed to initialize talking to
		// the API): return the wrapped error so the reconcile is retried
		// instead of swallowing it. Swallowing it here would forget the key,
		// so reportFinalStatus never runs and the finished PipelineRun's
		// concurrency slot is never released.
		return fmt.Errorf("detect provider: %w", err)
	}
	detectedProvider.SetPacInfo(&pacInfo)

	if repo, err := r.reportFinalStatus(ctx, logger, &pacInfo, event, pr, detectedProvider); err != nil {
		msg := fmt.Sprintf("report status: %v", err)
		r.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryReportFinalStatus", msg)
		return err
	}
	return nil
}

// abandonDoneWithoutProvider marks a done PipelineRun failed and releases its
// concurrency slot when its git-provider can never be resolved (missing or
// unknown annotation, see ErrProviderNotConfigured). There is no provider or
// event here to post a final status through, unlike reportFinalStatus, so
// this only performs the two things needed to stop the run wedging its
// repository's queue: releasing/promoting the queue and writing the terminal
// PAC state, in that order.
//
// The manager remembers a successful handoff until the terminal write succeeds.
// An unfinished promotion must return an error before the terminal state makes
// reconcileKind short-circuit.
func (r *Reconciler) abandonDoneWithoutProvider(ctx context.Context, logger *zap.SugaredLogger, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun, cause error) error {
	if err := r.startNextPipelineRunInQueue(ctx, logger, repo, pr); err != nil {
		return err
	}
	if _, err := r.updatePipelineRunState(ctx, logger, pr, kubeinteraction.StateFailed); err != nil {
		return fmt.Errorf("abandon pipelinerun without provider %s/%s (cause: %w): cannot update state: %w", pr.Namespace, pr.Name, cause, err)
	}
	r.qm.ForgetAdmission(queuepkg.RepoKey(repo), pr)
	return nil
}

func (r *Reconciler) createSecretForPipelineRun(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun, repo *v1alpha1.Repository) error {
	var gitAuthSecretName string
	// as GitAuthSecret annotation is added to the PipelineRun in getPipelineRunsFromRepo function
	// we expect the name here otherwise error out
	if annotation, ok := pr.GetAnnotations()[keys.GitAuthSecret]; ok {
		gitAuthSecretName = annotation
		logger.Debugf("using git auth secret from annotation=%s for pipelineRun %s/%s", gitAuthSecretName, pr.GetNamespace(), pr.GetName())
	} else {
		return fmt.Errorf("cannot get annotation %s as set on pipelineRun %s/%s", keys.GitAuthSecret, pr.GetNamespace(), pr.GetName())
	}

	// here we don't need provider but we need to call initGitProviderClient because we need event
	// built with user and token so secret can be built upon user and token
	_, event, err := r.initGitProviderClient(ctx, logger, repo, pr)
	if err != nil {
		return fmt.Errorf("cannot initialize git provider client: %w", err)
	}

	authSecret, err := secrets.MakeBasicAuthSecret(event, gitAuthSecretName)
	if err != nil {
		return fmt.Errorf("making basic auth secret: %s has failed: %w ", gitAuthSecretName, err)
	}

	if err = r.kinteract.CreateSecret(ctx, repo.GetNamespace(), authSecret); err != nil {
		// NOTE: Handle AlreadyExists errors due to etcd/API server timing issues.
		// Investigation found: slow etcd response causes API server retry, resulting in
		// duplicate secret creation attempts for the same PR. This is a workaround, not
		// designed behavior - reuse existing secret to prevent PipelineRun failure.
		if errors.IsAlreadyExists(err) {
			msg := fmt.Sprintf("Secret %s already exists in namespace %s, reusing existing secret",
				authSecret.GetName(), repo.GetNamespace())
			r.eventEmitter.EmitMessage(nil, zap.WarnLevel, "RepositorySecretReused", msg)
		} else {
			return fmt.Errorf("creating basic auth secret: %s has failed: %w ", authSecret.GetName(), err)
		}
	} else {
		logger.Debugf("created git auth secret %s in namespace %s for pipelineRun %s/%s", authSecret.GetName(), pr.GetNamespace(), pr.GetName())
	}

	if err = r.kinteract.UpdateSecretWithOwnerRef(ctx, logger, pr.Namespace, gitAuthSecretName, pr); err != nil {
		return fmt.Errorf("cannot update secret %s with ownerRef to pipelinerun %s: %w", gitAuthSecretName, pr.GetName(), err)
	}
	logger.Debugf("updated secret ownerRef for pipelinerun=%s secret=%s", pr.GetName(), gitAuthSecretName)

	patchAnnotations := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				keys.SecretCreated: "true",
			},
		},
	}

	_, err = action.PatchPipelineRun(ctx, logger, "patching annotations.secretCreated", r.run.Clients.Tekton, pr, patchAnnotations)
	if err != nil {
		return fmt.Errorf("failed to patch pipelinerun %s annotations.secretCreated: %w", pr.GetName(), err)
	}

	logger.Debugf("patched annotations.secretCreated for pipelinerun=%s", pr.GetName())
	return nil
}

func (r *Reconciler) reportFinalStatus(ctx context.Context, logger *zap.SugaredLogger, pacInfo *info.PacOpts, event *info.Event, pr *tektonv1.PipelineRun, provider provider.Interface) (*v1alpha1.Repository, error) {
	repoName := pr.GetAnnotations()[keys.Repository]
	repo, err := r.repoLister.Repositories(pr.Namespace).Get(repoName)
	if err != nil {
		return nil, fmt.Errorf("reportFinalStatus: %w", err)
	}

	secretNS := repo.GetNamespace()
	inheritedGlobalSecret := false
	if globalRepo, gerr := r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); gerr == nil && globalRepo != nil {
		if event.InstallationID <= 0 {
			if secretNS, inheritedGlobalSecret, err = secrets.ResolveInheritedSecret(repo, globalRepo); err != nil {
				return nil, err
			}
		}
		if merged := copyRepositoryForMerge(repo); merged != nil {
			repo = merged
			repo.Spec.Merge(globalRepo.Spec)
		}
	}

	cp := customparams.NewCustomParams(event, repo, r.run, r.kinteract, r.eventEmitter, nil)
	maptemplate, _, err := cp.GetParams(ctx)
	if err != nil {
		r.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "ParamsError",
			fmt.Sprintf("error processing repository CR custom params: %s", err.Error()))
	}
	r.run.Clients.ConsoleUI().SetParams(maptemplate)

	if event.InstallationID > 0 {
		event.Provider.WebhookSecret, _ = secrets.GetCurrentNSWebhookSecret(ctx, r.kinteract, r.run)
	} else {
		secretFromRepo := secrets.SecretFromRepository{
			K8int:                 r.kinteract,
			Config:                provider.GetConfig(),
			Event:                 event,
			Repo:                  repo,
			WebhookType:           pacInfo.WebhookType,
			Logger:                logger,
			Namespace:             secretNS,
			InheritedGlobalSecret: inheritedGlobalSecret,
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
	newPr, trStatus, err := r.postFinalStatus(ctx, logger, pacInfo, provider, event, pr)
	if err != nil {
		logger.Errorf("failed to post final status, moving on: %v", err)
		finalState = kubeinteraction.StateFailed
	}

	// LLM Analysis orchestrator checks if it is enabled and if the CEL condition
	// is matched for the defined roles, defaults to failed PipelineRuns only if
	// no CEL expression is defined.
	if newPr == nil {
		logger.Warn("skipping LLM analysis: no final pipelinerun status available")
	} else if err := r.performLLMAnalysis(ctx, logger, repo, newPr, event, provider); err != nil {
		logger.Warnf("LLM analysis failed (non-blocking): %v", err)
		r.eventEmitter.EmitMessage(repo, zap.WarnLevel, "LLMAnalysisFailed",
			fmt.Sprintf("AI/LLM analysis failed for repository %s/%s and pipeline run %s: %v", repo.Namespace, repo.Name, newPr.Name, err))
	}

	if err := r.startNextPipelineRunInQueue(ctx, logger, repo, pr); err != nil {
		return repo, err
	}
	if _, err := r.updatePipelineRunState(ctx, logger, pr, finalState); err != nil {
		return repo, fmt.Errorf("cannot update state: %w", err)
	}
	r.qm.ForgetAdmission(queuepkg.RepoKey(repo), pr)

	if err := r.emitMetrics(ctx, pr); err != nil {
		logger.Error("failed to emit metrics: ", err)
	}

	emitTimingSpans(logger, pr, &pacInfo.Settings, trStatus)

	if err := r.cleanupPipelineRuns(ctx, logger, pacInfo, repo, pr); err != nil {
		return repo, fmt.Errorf("error cleaning pipelineruns: %w", err)
	}

	return repo, nil
}

// A promotion owns its retries and records a successful handoff, so retrying a
// terminal write cannot consume another waiting candidate.
func (r *Reconciler) startNextPipelineRunInQueue(ctx context.Context, logger *zap.SugaredLogger, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) error {
	if err := r.processQueueAdmission(ctx, logger, repo, pr, nil, queuepkg.AdmissionResume); err != nil {
		return err
	}
	return r.processQueueAdmission(ctx, logger, repo, pr, nil, queuepkg.AdmissionPromotion)
}

func (r *Reconciler) updatePipelineRunToInProgress(ctx context.Context, logger *zap.SugaredLogger, repo *v1alpha1.Repository, pr *tektonv1.PipelineRun) error {
	if noLongerStartable(pr) {
		return errPipelineRunGone
	}
	patched, patchErr := r.updatePipelineRunState(ctx, logger, pr, kubeinteraction.StateStarted)
	if patchErr != nil {
		// Neither the PATCH error nor its input says whether Kubernetes applied
		// it. A pending read also cannot rule out an in-flight conditional write.
		var err error
		patched, err = r.run.Clients.Tekton.TektonV1().PipelineRuns(pr.Namespace).Get(ctx, pr.Name, metav1.GetOptions{})
		if errors.IsNotFound(err) || (err == nil && (patched.UID != pr.UID || noLongerStartable(patched))) {
			return errPipelineRunGone
		}
		if err != nil || !startConfirmed(patched) {
			return fmt.Errorf("%w: cannot update state: %w", ErrPipelineRunNotStarted, stderrors.Join(patchErr, err))
		}
	}
	pr = patched

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

func noLongerStartable(current *tektonv1.PipelineRun) bool {
	return queuepkg.Finishing(current) ||
		current.Annotations[keys.State] == kubeinteraction.StateCompleted ||
		current.Annotations[keys.State] == kubeinteraction.StateFailed
}

// startConfirmed reports whether the start write is visible in the cluster. A
// non-pending PipelineRun is not enough on its own: the patch is metadata-only
// for a run Tekton already started, so a run that is merely running proves
// nothing about the state and reporting annotations this patch carries.
func startConfirmed(current *tektonv1.PipelineRun) bool {
	return current.Spec.Status != tektonv1.PipelineRunSpecStatusPending &&
		current.Annotations[keys.State] == kubeinteraction.StateStarted &&
		current.Annotations[keys.SCMReportingPLRStarted] == "true"
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
		event.Provider.WebhookSecret, _ = secrets.GetCurrentNSWebhookSecret(ctx, r.kinteract, r.run)
	} else {
		// secretNS is needed when git provider is other than Github App.
		secretNS := repo.GetNamespace()
		inheritedGlobalSecret := false
		if globalRepo, gerr := r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository); gerr == nil && globalRepo != nil {
			secretNS, inheritedGlobalSecret, err = secrets.ResolveInheritedSecret(repo, globalRepo)
			if err != nil {
				return nil, nil, err
			}
			if merged := copyRepositoryForMerge(repo); merged != nil {
				repo = merged
				repo.Spec.Merge(globalRepo.Spec)
			}
		}

		secretFromRepo := secrets.SecretFromRepository{
			K8int:                 r.kinteract,
			Config:                detectedProvider.GetConfig(),
			Event:                 event,
			Repo:                  repo,
			WebhookType:           pacInfo.WebhookType,
			Logger:                logger,
			Namespace:             secretNS,
			InheritedGlobalSecret: inheritedGlobalSecret,
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

	metadata := map[string]any{
		"labels": map[string]string{
			keys.State: state,
		},
		"annotations": annotations,
	}
	if state == kubeinteraction.StateStarted {
		// Optimistic concurrency fences delayed starts against cancellation,
		// completion and replacement by a new PipelineRun with the same name.
		metadata["resourceVersion"] = pr.ResourceVersion
		metadata["uid"] = pr.UID
	}
	mergePatch := map[string]any{"metadata": metadata}

	// if state is started and the pipelineRun is still pending then clear the
	// pending status so Tekton can pick it up. If it already isn't pending
	// (e.g. Tekton already started it before we got a chance to report it),
	// skip this patch: the field is already cleared, and some Tekton versions'
	// admission webhook rejects any spec update once a PipelineRun has started,
	// even a no-op one.
	if state == kubeinteraction.StateStarted && pr.Spec.Status == tektonv1.PipelineRunSpecStatusPending {
		mergePatch["spec"] = map[string]any{
			"status": "",
		}
	}
	actionLog := state + " state"
	var (
		patchedPR *tektonv1.PipelineRun
		err       error
	)
	if state == kubeinteraction.StateStarted {
		patch, marshalErr := json.Marshal(mergePatch)
		if marshalErr != nil {
			return pr, fmt.Errorf("error marshaling the pipelinerun patch: %w", marshalErr)
		}
		// A conflict needs fresh readback, not retries with the same resourceVersion.
		patchedPR, err = r.run.Clients.Tekton.TektonV1().PipelineRuns(pr.Namespace).Patch(ctx, pr.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	} else {
		patchedPR, err = action.PatchPipelineRun(ctx, logger, actionLog, r.run.Clients.Tekton, pr, mergePatch)
	}
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
	return llm.ExecuteAnalysis(ctx, r.run, r.kinteract, logger, repo, pr, event, provider)
}
