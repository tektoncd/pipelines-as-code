package reconciler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"slices"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/pipelineascode"
	prgroup "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerungroup"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
)

// evaluateFollowUpsOf re-evaluates every pending follow-up of the group of a
// finished member. Runs on the reconcile of an already done run, which is
// what recovers the follow-ups after a restart or a missed event, and what
// picks up the dependents of a follow-up consumed outside the watcher (a
// /cancel, an event time skip): consuming it patches the run storing it, which
// re-enqueues that run. A follow-up still waiting returns before any API call.
func (r *Reconciler) evaluateFollowUpsOf(ctx context.Context, logger *zap.SugaredLogger, dep *tektonv1.PipelineRun) error {
	return r.evaluateDependentsOf(ctx, logger, "", dep)
}

// evaluateGroup evaluates the pending follow-ups of a group depending on depName,
// or all of them when depName is empty. Any group member carries the same
// event annotations; eventSource supplies them.
func (r *Reconciler) evaluateGroup(ctx context.Context, logger *zap.SugaredLogger,
	namespace, groupID, depName string, eventSource *tektonv1.PipelineRun,
) error {
	group, err := r.listGroup(ctx, namespace, groupID)
	if err != nil {
		return fmt.Errorf("cannot list the runs of group %s: %w", groupID, err)
	}
	collected, err := prgroup.CollectFollowUps(group)
	if err != nil {
		return fmt.Errorf("cannot collect the follow-ups of group %s: %w", groupID, err)
	}
	for _, fu := range collected {
		if !fu.FollowUp.Pending() {
			continue
		}
		if depName != "" && !slices.Contains(fu.FollowUp.Deps, depName) {
			continue
		}
		if err := r.evaluateFollowUp(ctx, logger, fu, group, collected, eventSource); err != nil {
			// one broken follow-up must not block its siblings
			logger.Errorf("cannot evaluate follow-up %s: %v", fu.FollowUp.Name, err)
		}
	}
	return nil
}

// evaluateFollowUp decides the fate of a pending follow-up: keep waiting, create its
// run, or skip it. An existing run of the follow-up's deterministic name means
// another reconcile already created it. While the follow-up waits nothing is
// fetched; once every dependency is done the definition is re-resolved at the
// event's pinned revision and its condition decides.
func (r *Reconciler) evaluateFollowUp(ctx context.Context, logger *zap.SugaredLogger,
	fu prgroup.FollowUpOnRun, group []*tektonv1.PipelineRun, collected []prgroup.FollowUpOnRun,
	eventSource *tektonv1.PipelineRun,
) error {
	followUp := fu.FollowUp
	namespace := eventSource.GetNamespace()

	decision, resolveErr := prgroup.ResolveFollowUp(followUp, group, collected)
	if resolveErr == nil && decision.Waiting {
		// its run cannot exist before every dependency is done
		logger.Debugf("follow-up %s is still waiting on its dependencies", followUp.Name)
		return nil
	}

	if _, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(namespace).Get(ctx, followUp.ID, metav1.GetOptions{}); err == nil {
		return r.consumeFollowUp(ctx, fu, prgroup.StateCreated)
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("cannot get the follow-up pipelinerun %s: %w", followUp.ID, err)
	}

	if resolveErr != nil {
		// a follow-up that cannot resolve can never resolve on retry: skip it
		return r.skipFollowUp(ctx, logger, fu, eventSource,
			fmt.Sprintf("cannot resolve the follow-up: %v", resolveErr))
	}

	repo, vcx, rebuilt, err := r.followUpClient(ctx, logger, eventSource)
	if err != nil {
		return r.failFollowUp(ctx, logger, fu, eventSource, nil, nil, nil, err)
	}
	// the event as it was templated and matched, restored over the one
	// rebuilt from the annotations: the definition, its CEL condition and
	// the created run all see what the event time saw
	event := followUp.Event.Apply(rebuilt)
	paramFilters := followUp.Event.ParamFilterOutcomes()

	run, err := pipelineascode.ResolveFollowUpDefinition(ctx, r.run, r.kinteract, r.eventEmitter,
		logger, vcx, event, repo, followUp.Name, paramFilters)
	if err != nil {
		if stderrors.Is(err, pipelineascode.ErrFollowUpDefinitionNotFound) {
			// the definition is gone at the event's revision: permanent
			return r.skipFollowUp(ctx, logger, fu, eventSource,
				fmt.Sprintf("cannot resolve the follow-up: %v", err))
		}
		// a provider error left after the configured API retries, or a
		// remote task that cannot be fetched: reported like a run that
		// cannot start, nothing retries it
		return r.failFollowUp(ctx, logger, fu, eventSource, repo, vcx, event, err)
	}

	cel := run.GetAnnotations()[keys.OnCelExpression]
	if cel == "" {
		return r.skipFollowUp(ctx, logger, fu, eventSource,
			"its definition has no on-cel-expression at the event revision")
	}
	// the same variables the matcher computes
	customParams := matcher.ResolveCustomParamsForCEL(ctx, repo, event, r.run, vcx, r.eventEmitter, logger, paramFilters)
	ok, err := matcher.EvaluateFollowUp(ctx, cel, event, vcx, customParams, r.eventEmitter, repo,
		prgroup.BuildRunsMap(followUp.Deps, decision.Runs))
	if err != nil {
		// an evaluation error can never resolve on retry: the expression is
		// pinned by the revision the definition was fetched at
		return r.skipFollowUp(ctx, logger, fu, eventSource,
			fmt.Sprintf("CEL expression evaluation error: %v", err))
	}
	if !ok {
		// like a definition that does not match the event: not reported
		return r.skipFollowUpQuietly(ctx, logger, fu, eventSource,
			fmt.Sprintf("CEL condition evaluated to false (dependencies: %s)",
				prgroup.SummarizeStatuses(followUp.Deps, decision.Runs)))
	}
	return r.createFollowUp(ctx, logger, fu, collected, eventSource, repo, vcx, event, run, decision)
}

// createFollowUp creates the run of a follow-up whose dependencies are done and
// whose condition is true, with its final params already injected. The
// deterministic name makes a concurrent creation by another reconcile lose
// cleanly on the AlreadyExists error. From there the run behaves like any run
// of the repository.
func (r *Reconciler) createFollowUp(ctx context.Context, logger *zap.SugaredLogger,
	fu prgroup.FollowUpOnRun, collected []prgroup.FollowUpOnRun,
	eventSource *tektonv1.PipelineRun, repo *v1alpha1.Repository,
	vcx provider.Interface, event *info.Event, run *tektonv1.PipelineRun, decision prgroup.Decision,
) error {
	followUp := fu.FollowUp
	namespace := eventSource.GetNamespace()

	// decorate like a run about to be created: labels, annotations, group
	// decorate like startPR does: labels, annotations, group
	if err := kubeinteraction.AddLabelsAndAnnotations(ctx, event, run, repo, vcx.GetConfig(), r.run); err != nil {
		logger.Errorf("Error adding labels/annotations to PipelineRun '%s' in namespace '%s': %v", followUp.ID, namespace, err)
	}
	if err := prgroup.Decorate(run, prgroup.GroupIDOf(eventSource)); err != nil {
		return r.failFollowUp(ctx, logger, fu, eventSource, repo, vcx, event,
			fmt.Errorf("cannot decorate pipelinerun %s with its group: %w", followUp.ID, err))
	}
	run.Name = followUp.ID
	run.GenerateName = ""
	run.Namespace = namespace
	// the creation decides: pending under a concurrency limit, running otherwise
	run.Spec.Status = ""

	// like startPR, record the console log url for non GitHub App runs
	if _, ok := run.GetAnnotations()[keys.InstallationID]; !ok {
		if run.GetAnnotations() == nil {
			run.Annotations = map[string]string{}
		}
		run.Annotations[keys.LogURL] = r.detailURL(run)
	}

	if unresolved := prgroup.InjectRunParams(run, decision.Runs); len(unresolved) > 0 {
		return r.skipFollowUp(ctx, logger, fu, eventSource,
			fmt.Sprintf("cannot resolve %s", strings.Join(unresolved, ", ")))
	}

	// minted from a token acquired now: GitHub App installation tokens are
	// fresh instead of hours old
	pacInfo := r.run.Info.GetPacOpts()
	secretName, hasSecretName := run.GetAnnotations()[keys.GitAuthSecret]
	autoSecret := pacInfo.SecretAutoCreation
	queued := repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit != 0
	if autoSecret {
		if !hasSecretName {
			return r.failFollowUp(ctx, logger, fu, eventSource, repo, vcx, event,
				fmt.Errorf("cannot get annotation %s as set on PR", keys.GitAuthSecret))
		}
		authSecret, err := secrets.MakeBasicAuthSecret(event, secretName)
		if err != nil {
			return r.failFollowUp(ctx, logger, fu, eventSource, repo, vcx, event,
				fmt.Errorf("making basic auth secret: %s has failed: %w ", secretName, err))
		}
		if err := r.kinteract.CreateSecret(ctx, namespace, authSecret); err != nil {
			if !errors.IsAlreadyExists(err) {
				return r.failFollowUp(ctx, logger, fu, eventSource, repo, vcx, event,
					fmt.Errorf("creating basic auth secret: %s has failed: %w ", authSecret.GetName(), err))
			}
			r.eventEmitter.EmitMessage(repo, zap.WarnLevel, "RepositorySecretReused",
				fmt.Sprintf("Secret %s already exists in namespace %s, reusing existing secret", authSecret.GetName(), namespace))
		}
	}

	// the state annotation is set at creation: the watcher only reconciles
	// runs carrying it, so a lost start patch below cannot orphan the run.
	// A started run whose start is not reported yet is retried by its own
	// reconcile.
	if run.GetLabels() == nil {
		run.Labels = map[string]string{}
	}
	if run.GetAnnotations() == nil {
		run.Annotations = map[string]string{}
	}
	if queued {
		run.Spec.Status = tektonv1.PipelineRunSpecStatusPending
		run.Labels[keys.State] = kubeinteraction.StateQueued
		run.Annotations[keys.State] = kubeinteraction.StateQueued
		run.Annotations[keys.ExecutionOrder] = namespace + "/" + run.GetName()
	} else {
		run.Labels[keys.State] = kubeinteraction.StateStarted
		run.Annotations[keys.State] = kubeinteraction.StateStarted
	}

	created, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(namespace).Create(ctx, run, metav1.CreateOptions{})
	if err != nil {
		if autoSecret {
			// the ownerRef is not set yet, clean the secret up like startPR
			// does on any creation error: its name is generated per
			// resolution, so a concurrent reconcile that won the creation
			// below uses a secret of its own
			if errDel := r.kinteract.DeleteSecret(ctx, logger, namespace, secretName); errDel != nil {
				logger.Errorf("cannot remove the auto created secret %s: %v", secretName, errDel)
			}
		}
		if errors.IsAlreadyExists(err) {
			// another reconcile created this follow-up first
			return r.consumeFollowUp(ctx, fu, prgroup.StateCreated)
		}
		return r.failFollowUp(ctx, logger, fu, eventSource, repo, vcx, event,
			fmt.Errorf("creating pipelinerun %s in namespace %s has failed.\n\nTekton Controller has reported this error: ```%w``` ",
				run.GetName(), namespace, err))
	}

	if autoSecret {
		if err := r.kinteract.UpdateSecretWithOwnerRef(ctx, logger, namespace, secretName, created); err != nil {
			// the run may fail to clone, but it must not fail the creation
			logger.Errorf("cannot set the ownerRef of the git auth secret %s: %v", secretName, err)
		}
	}

	// A cancel consumes the follow-up before it looks for its run: if it
	// did so while this run was being created, it may have missed the run,
	// so the run is cancelled here and the cancelled state is kept.
	if r.followUpCancelled(ctx, fu) {
		logger.Infof("follow-up %s was cancelled while its run was created, cancelling pipelinerun %s", followUp.ID, created.GetName())
		if _, err := action.PatchPipelineRun(ctx, logger, "cancel patch", r.run.Clients.Tekton, created, map[string]any{
			"spec": map[string]any{"status": tektonv1.PipelineRunSpecStatusCancelledRunFinally},
		}); err != nil {
			return fmt.Errorf("cannot cancel the pipelinerun %s of the cancelled follow-up %s: %w", created.GetName(), followUp.ID, err)
		}
		return nil
	}

	// report the started (or queued) status; on GitHub App runs this also
	// patches the check-run id the reconcile of the run waits for
	// The run exists from here on: a failed report must not stop the
	// follow-up from being consumed and its dependents from migrating.
	// Like startPR, the created run decides, not the repository settings: it
	// is pending under the concurrency limit or because an external mutating
	// webhook (a Kueue-like queue) pended it, and is then reported queued and
	// left pending for whoever pended it to start it.
	if created.Spec.Status == tektonv1.PipelineRunSpecStatusPending {
		if err := r.reportQueuedFollowUp(ctx, logger, repo, vcx, event, created); err != nil {
			logger.Errorf("cannot report the queued status of pipelinerun %s: %v", created.GetName(), err)
		}
	} else if err := r.updatePipelineRunToInProgress(ctx, logger, repo, created); err != nil && !stderrors.Is(err, errPipelineRunGone) {
		logger.Errorf("cannot report the started status of pipelinerun %s, its reconcile retries: %v", created.GetName(), err)
	}

	if err := r.consumeFollowUp(ctx, fu, prgroup.StateCreated); err != nil {
		logger.Errorf("cannot consume the created followUp %s: %v", followUp.ID, err)
	}
	// follow-ups waiting on this follow-up migrate to their new direct
	// dependency, so the runs they were first stamped on can be pruned
	r.migrateDependentFollowUps(ctx, logger, created, collected)

	logger.Infof("created follow-up pipelinerun %s", created.GetName())
	return nil
}

// reportQueuedFollowUp reports a follow-up created pending exactly like
// startPR reports a pending run: the queued status from the provider's
// queueing template, then the queued state patched on the run, so a run an
// external webhook pended is not taken for started.
func (r *Reconciler) reportQueuedFollowUp(ctx context.Context, logger *zap.SugaredLogger,
	repo *v1alpha1.Repository, vcx provider.Interface, event *info.Event, pr *tektonv1.PipelineRun,
) error {
	consoleURL := r.detailURL(pr)
	mt := formatting.MessageTemplate{
		PipelineRunName: pr.GetName(),
		Namespace:       repo.GetNamespace(),
		ConsoleName:     r.run.Clients.ConsoleUI().GetName(),
		ConsoleURL:      consoleURL,
		TknBinary:       settings.TknBinaryName,
		TknBinaryURL:    settings.TknBinaryURL,
	}
	text, err := mt.MakeTemplate(vcx.GetTemplate(provider.QueueingPipelineType))
	if err != nil {
		return fmt.Errorf("cannot create message template: %w", err)
	}
	if err := vcx.CreateStatus(ctx, event, providerstatus.StatusOpts{
		Status:                  "queued",
		Conclusion:              providerstatus.ConclusionPending,
		Text:                    text,
		DetailsURL:              consoleURL,
		PipelineRunName:         pr.GetName(),
		PipelineRun:             pr,
		OriginalPipelineRunName: pr.GetAnnotations()[keys.OriginalPRName],
	}); err != nil {
		return fmt.Errorf("cannot use the API on the provider platform to create a in_progress status: %w", err)
	}
	if pr.GetAnnotations()[keys.State] == kubeinteraction.StateQueued {
		return nil
	}
	// pended by an external webhook: the run was created as started
	_, err = action.PatchPipelineRun(ctx, logger, "annotations.state and labels.state", r.run.Clients.Tekton, pr,
		map[string]any{"metadata": map[string]any{
			"annotations": map[string]string{keys.State: kubeinteraction.StateQueued},
			"labels":      map[string]string{keys.State: kubeinteraction.StateQueued},
		}})
	return err
}

// migrateDependentFollowUps moves the pending follow-ups that directly depend on
// the created run over to it. Added before removed, and removed only once the
// addition is stored: a failure in between leaves two copies, which the
// collection dedupes, never zero.
func (r *Reconciler) migrateDependentFollowUps(ctx context.Context, logger *zap.SugaredLogger,
	created *tektonv1.PipelineRun, collected []prgroup.FollowUpOnRun,
) {
	createdName := prgroup.DefinitionName(created)
	var moving []prgroup.FollowUp
	idsBySource := map[string][]string{}
	for _, fu := range collected {
		if !fu.FollowUp.Pending() || !slices.Contains(fu.FollowUp.Deps, createdName) {
			continue
		}
		if fu.Run.GetName() == created.GetName() {
			continue
		}
		moving = append(moving, fu.FollowUp)
		idsBySource[fu.Run.GetName()] = append(idsBySource[fu.Run.GetName()], fu.FollowUp.ID)
	}
	if len(moving) == 0 {
		return
	}

	namespace := created.GetNamespace()
	if err := r.updateFollowUpsFenced(ctx, namespace, created.GetName(), func(pr *tektonv1.PipelineRun) error {
		return prgroup.AddFollowUps(pr, moving...)
	}); err != nil {
		// the follow-ups stay on the runs they were stamped on
		logger.Errorf("cannot migrate the follow-ups onto pipelinerun %s: %v", created.GetName(), err)
		return
	}
	for source, ids := range idsBySource {
		if err := r.updateFollowUpsFenced(ctx, namespace, source, func(pr *tektonv1.PipelineRun) error {
			return prgroup.RemoveFollowUps(pr, ids...)
		}); err != nil {
			// two copies remain, the collection dedupes them
			logger.Debugf("cannot drop the migrated follow-ups %v from pipelinerun %s: %v", ids, source, err)
		}
	}
}

// updateFollowUpsFenced applies mutate to the follow-ups stored on a run and
// patches them, fenced on the UID and resourceVersion, re-reading the run on a
// conflict. Unlike patchFenced, a lost fence is retried and an exhausted
// retry or a gone run is an error: the caller relies on the update landing.
func (r *Reconciler) updateFollowUpsFenced(ctx context.Context, namespace, name string,
	mutate func(*tektonv1.PipelineRun) error,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if err := mutate(fresh); err != nil {
			return err
		}
		return r.patchFencedStrict(ctx, fresh, prgroup.FollowUpsPatch(fresh))
	})
}

// skipFollowUp consumes a pending follow-up as skipped and reports the skipped check.
func (r *Reconciler) skipFollowUp(ctx context.Context, logger *zap.SugaredLogger,
	fu prgroup.FollowUpOnRun, eventSource *tektonv1.PipelineRun, reason string,
) error {
	followUp := fu.FollowUp
	repo, err := r.followUpRepo(eventSource)
	if err != nil {
		return err
	}

	vcx, event, err := r.initGitProviderClient(ctx, logger, repo, eventSource)
	if err != nil {
		return fmt.Errorf("cannot initialize git provider client: %w", err)
	}
	detailsURL := r.detailURL(eventSource)
	if err := createStatusWithRetry(ctx, logger, vcx, event, providerstatus.StatusOpts{
		Status:                  pipelineascode.CompletedStatus,
		Conclusion:              providerstatus.ConclusionSkipped,
		Text:                    fmt.Sprintf("PipelineRun is skipped: %s", reason),
		DetailsURL:              detailsURL,
		PipelineRunName:         followUp.ID,
		OriginalPipelineRunName: followUp.Name,
	}); err != nil {
		// the follow-up is consumed regardless: a skipped follow-up must not come back
		logger.Errorf("cannot report the skipped status of follow-up %s: %v", followUp.Name, err)
	}

	return r.consumeSkippedFollowUp(ctx, logger, repo, fu, eventSource, reason)
}

// failFollowUp handles a follow-up whose run cannot be created, exactly like
// the event time handles a run that cannot start: no retry, a failure check
// "pipelinerun start failure" with the error, and the follow-up consumed as
// failed. Without a provider client (it could not be initialized) only the
// Kubernetes event is emitted. The follow-ups waiting on it are re-evaluated
// and see a dependency that never ran.
func (r *Reconciler) failFollowUp(ctx context.Context, logger *zap.SugaredLogger,
	fu prgroup.FollowUpOnRun, eventSource *tektonv1.PipelineRun,
	repo *v1alpha1.Repository, vcx provider.Interface, event *info.Event, err error,
) error {
	followUp := fu.FollowUp
	r.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryPipelineRun",
		fmt.Sprintf("There was an error starting the PipelineRun %s, %s", followUp.Name, err.Error()))
	// without a provider client the event is nil too: only the Kubernetes
	// event is emitted, no status check can be posted
	if vcx != nil && event != nil {
		if statusErr := vcx.CreateStatus(ctx, event, providerstatus.StatusOpts{
			PipelineRunName:         followUp.ID,
			OriginalPipelineRunName: followUp.Name,
			Status:                  pipelineascode.CompletedStatus,
			Title:                   "pipelinerun start failure",
			Conclusion:              providerstatus.ConclusionFailure,
			Text:                    fmt.Sprintf("There was an error creating the PipelineRun: <b>%s</b>\n\n%s", followUp.Name, err.Error()),
			DetailsURL:              r.run.Clients.ConsoleUI().URL(),
		}); statusErr != nil {
			r.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryCreateStatus",
				fmt.Sprintf("Cannot create status: %s: %s", err, statusErr))
		}
	}
	if consumeErr := r.consumeFollowUp(ctx, fu, prgroup.StateFailed); consumeErr != nil {
		logger.Errorf("cannot consume the failed followUp %s: %v", followUp.ID, consumeErr)
	}
	return r.evaluateDependentsOf(ctx, logger, followUp.Name, eventSource)
}

// skipFollowUpQuietly skips without a provider check, for outcomes that are the
// pipeline author's, like a false CEL condition.
func (r *Reconciler) skipFollowUpQuietly(ctx context.Context, logger *zap.SugaredLogger,
	fu prgroup.FollowUpOnRun, eventSource *tektonv1.PipelineRun, reason string,
) error {
	repo, err := r.followUpRepo(eventSource)
	if err != nil {
		return err
	}
	return r.consumeSkippedFollowUp(ctx, logger, repo, fu, eventSource, reason)
}

// consumeSkippedFollowUp consumes the follow-up and re-evaluates the follow-ups waiting
// on it.
func (r *Reconciler) consumeSkippedFollowUp(ctx context.Context, logger *zap.SugaredLogger,
	repo *v1alpha1.Repository, fu prgroup.FollowUpOnRun,
	eventSource *tektonv1.PipelineRun, reason string,
) error {
	followUp := fu.FollowUp
	r.eventEmitter.EmitMessage(repo, zap.InfoLevel, "RepositoryPipelineRunSkipped",
		fmt.Sprintf("PipelineRun %s is skipped: %s", followUp.Name, reason))

	if err := r.consumeFollowUp(ctx, fu, prgroup.StateSkipped); err != nil {
		logger.Errorf("cannot consume the skipped followUp %s: %v", followUp.ID, err)
	}
	return r.evaluateDependentsOf(ctx, logger, followUp.Name, eventSource)
}

// evaluateDependentsOf re-evaluates the pending follow-ups waiting on a
// definition whose follow-up was skipped or cancelled.
func (r *Reconciler) evaluateDependentsOf(ctx context.Context, logger *zap.SugaredLogger,
	name string, eventSource *tektonv1.PipelineRun,
) error {
	groupID := prgroup.GroupIDOf(eventSource)
	if groupID == "" {
		return nil
	}
	return r.evaluateGroup(ctx, logger, eventSource.GetNamespace(), groupID, name, eventSource)
}

// finalizeFollowUps handles the follow-ups of a run being deleted: the
// pending follow-ups stored on it are evaluated one last time, and the follow-ups
// waiting on its definition are re-evaluated with the run excluded from the
// group, so they see the dependency as gone.
func (r *Reconciler) finalizeFollowUps(ctx context.Context, logger *zap.SugaredLogger, pr *tektonv1.PipelineRun) {
	groupID := prgroup.GroupIDOf(pr)
	if groupID == "" {
		return
	}
	namespace := pr.GetNamespace()
	groupAll, err := r.listGroup(ctx, namespace, groupID)
	if err != nil {
		logger.Errorf("cannot list the runs of group %s: %v", groupID, err)
		return
	}
	// exclude the dying run: the follow-ups waiting on it must see it gone
	group := make([]*tektonv1.PipelineRun, 0, len(groupAll))
	for _, run := range groupAll {
		if run.GetName() != pr.GetName() {
			group = append(group, run)
		}
	}
	collected, err := prgroup.CollectFollowUps(group)
	if err != nil {
		logger.Errorf("cannot collect the follow-ups of group %s: %v", groupID, err)
		return
	}
	// the follow-ups stored on the dying run join the evaluation with their
	// run, so a skip can still report and a creation can migrate them
	followUpsOnPR, err := prgroup.FollowUpsOf(pr)
	if err != nil {
		logger.Errorf("cannot read the follow-ups of pipelinerun %s: %v", pr.GetName(), err)
		return
	}
	depName := prgroup.DefinitionName(pr)
	for _, followUp := range followUpsOnPR {
		if !followUp.Pending() {
			continue
		}
		fu := prgroup.FollowUpOnRun{FollowUp: followUp, Run: pr}
		collected = append(collected, fu)
		if slices.Contains(followUp.Deps, depName) {
			// the dying run is one of its dependencies: it can never complete
			if err := r.skipFollowUp(ctx, logger, fu, pr,
				fmt.Sprintf("dependency %s was deleted", depName)); err != nil {
				logger.Errorf("cannot skip the follow-up %s: %v", followUp.Name, err)
			}
			continue
		}
		if err := r.evaluateFollowUp(ctx, logger, fu, group, collected, pr); err != nil {
			logger.Errorf("cannot evaluate the follow-up %s: %v", followUp.Name, err)
		}
	}
	// the follow-ups on other runs that wait on the dying run's definition
	for _, fu := range collected {
		if !fu.FollowUp.Pending() || !slices.Contains(fu.FollowUp.Deps, depName) {
			continue
		}
		if fu.Run.GetName() == pr.GetName() {
			continue
		}
		if err := r.evaluateFollowUp(ctx, logger, fu, group, collected, pr); err != nil {
			logger.Errorf("cannot evaluate the follow-up %s: %v", fu.FollowUp.Name, err)
		}
	}
}

// consumeFollowUp moves a follow-up to a terminal state, fenced on the run's
// UID and resourceVersion. A run that is gone stores nothing anymore.
func (r *Reconciler) consumeFollowUp(ctx context.Context,
	fu prgroup.FollowUpOnRun, state string,
) error {
	fresh, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(fu.Run.GetNamespace()).Get(ctx,
		fu.Run.GetName(), metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("cannot get the pipelinerun %s: %w", fu.Run.GetName(), err)
	}
	if err := prgroup.ConsumeFollowUp(fresh, fu.FollowUp.ID, state); err != nil {
		return err
	}
	if err := r.patchFenced(ctx, fresh, prgroup.FollowUpsPatch(fresh)); err != nil {
		return fmt.Errorf("cannot consume the follow-up %s on pipelinerun %s: %w",
			fu.FollowUp.ID, fresh.GetName(), err)
	}
	return nil
}

// followUpCancelled reports whether a follow-up is cancelled, read fresh from
// the run it is stored on.
func (r *Reconciler) followUpCancelled(ctx context.Context, fu prgroup.FollowUpOnRun) bool {
	fresh, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(fu.Run.GetNamespace()).Get(ctx,
		fu.Run.GetName(), metav1.GetOptions{})
	if err != nil {
		return false
	}
	followUps, err := prgroup.FollowUpsOf(fresh)
	if err != nil {
		return false
	}
	for _, followUp := range followUps {
		if followUp.ID == fu.FollowUp.ID {
			return followUp.State == prgroup.StateCancelled
		}
	}
	return false
}

// patchFenced applies a merge patch fenced on the UID and resourceVersion; a
// lost fence is not an error. It deliberately bypasses action.PatchPipelineRun,
// which retries on conflict: here a conflict means another reconcile already
// moved the run.
func (r *Reconciler) patchFenced(ctx context.Context, pr *tektonv1.PipelineRun, mergePatch map[string]any) error {
	if err := r.patchFencedStrict(ctx, pr, mergePatch); err != nil {
		if errors.IsConflict(err) || errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}

// patchFencedStrict applies a merge patch fenced on the UID and
// resourceVersion and returns every error, a lost fence included.
func (r *Reconciler) patchFencedStrict(ctx context.Context, pr *tektonv1.PipelineRun, mergePatch map[string]any) error {
	metadata, _ := mergePatch["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		mergePatch["metadata"] = metadata
	}
	metadata["resourceVersion"] = pr.GetResourceVersion()
	metadata["uid"] = pr.GetUID()

	patch, err := json.Marshal(mergePatch)
	if err != nil {
		return fmt.Errorf("cannot marshal the patch of pipelinerun %s: %w", pr.GetName(), err)
	}
	_, err = r.run.Clients.Tekton.TektonV1().PipelineRuns(pr.GetNamespace()).Patch(ctx,
		pr.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("cannot patch pipelinerun %s: %w", pr.GetName(), err)
	}
	return nil
}

// listGroup lists the PipelineRuns of a follow-up group.
func (r *Reconciler) listGroup(ctx context.Context, namespace, groupID string) ([]*tektonv1.PipelineRun, error) {
	list, err := r.run.Clients.Tekton.TektonV1().PipelineRuns(namespace).List(ctx,
		metav1.ListOptions{LabelSelector: prgroup.GroupSelector(groupID)})
	if err != nil {
		return nil, err
	}
	prs := make([]*tektonv1.PipelineRun, 0, len(list.Items))
	for i := range list.Items {
		prs = append(prs, &list.Items[i])
	}
	return prs, nil
}

// followUpRepo fetches the repository CR of a follow-up, from the annotation
// stamped at event time.
func (r *Reconciler) followUpRepo(pr *tektonv1.PipelineRun) (*v1alpha1.Repository, error) {
	repo, err := r.repoLister.Repositories(pr.GetNamespace()).Get(pr.GetAnnotations()[keys.Repository])
	if err != nil {
		return nil, fmt.Errorf("cannot get repository CR: %w", err)
	}
	return repo, nil
}

// followUpClient returns the repository a follow-up is created for, merged
// with the global one, and the git provider client to create it with. The
// client is initialized from the local repository, which decides whether the
// git provider secret is inherited from the global one; the creation honors
// the global settings, like the queue admission does.
func (r *Reconciler) followUpClient(ctx context.Context, logger *zap.SugaredLogger,
	eventSource *tektonv1.PipelineRun,
) (*v1alpha1.Repository, provider.Interface, *info.Event, error) {
	repo, err := r.followUpRepo(eventSource)
	if err != nil {
		return nil, nil, nil, err
	}
	vcx, event, err := r.initGitProviderClient(ctx, logger, repo, eventSource)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("cannot initialize git provider client: %w", err)
	}
	return r.mergedRepo(repo), vcx, event, nil
}

// mergedRepo merges the global repository settings in, like the queue
// admission does, so an inherited concurrency limit is honored.
func (r *Reconciler) mergedRepo(repo *v1alpha1.Repository) *v1alpha1.Repository {
	globalRepo, err := r.repoLister.Repositories(r.run.Info.Kube.Namespace).Get(r.run.Info.Controller.GlobalRepository)
	if err == nil && globalRepo != nil {
		if merged := copyRepositoryForMerge(repo); merged != nil {
			merged.Spec.Merge(globalRepo.Spec)
			return merged
		}
	}
	return repo
}
