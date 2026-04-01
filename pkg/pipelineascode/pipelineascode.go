package pipelineascode

import (
	"context"
	"fmt"
	"sync"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/customparams"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	tektonDir        = ".tekton"
	CompletedStatus  = "completed"
	inProgressStatus = "in_progress"
	queuedStatus     = "queued"
)

type PacRun struct {
	event        *info.Event
	vcx          provider.Interface
	run          *params.Run
	k8int        kubeinteraction.Interface
	logger       *zap.SugaredLogger
	eventEmitter *events.EventEmitter
	manager      *ConcurrencyManager
	pacInfo      *info.PacOpts
	globalRepo   *v1alpha1.Repository
}

func NewPacs(event *info.Event, vcx provider.Interface, run *params.Run, pacInfo *info.PacOpts, k8int kubeinteraction.Interface, logger *zap.SugaredLogger, globalRepo *v1alpha1.Repository) PacRun {
	return PacRun{
		event: event, run: run, vcx: vcx, k8int: k8int, pacInfo: pacInfo, logger: logger, globalRepo: globalRepo,
		eventEmitter: events.NewEventEmitter(run.Clients.Kube, logger),
		manager:      NewConcurrencyManager(),
	}
}

func (p *PacRun) Run(ctx context.Context) error {
	p.debugf("run start: trigger_target=%s event_type=%s repo_url=%s sha=%s pr=%d has_skip=%t cancel_pipeline_runs=%t",
		p.event.TriggerTarget,
		p.event.EventType,
		p.event.URL,
		p.event.SHA,
		p.event.PullRequestNumber,
		p.event.HasSkipCommand,
		p.event.CancelPipelineRuns,
	)
	// For PullRequestClosed events, skip matching logic and go straight to cancellation
	if p.event.TriggerTarget == triggertype.PullRequestClosed {
		p.debugf("pull request closed: verifying repo and cancelling in-progress pipelineRuns")
		repo, err := p.verifyRepoAndUser(ctx)
		if err != nil {
			return err
		}
		if repo != nil {
			p.debugf("repo verified: name=%s namespace=%s", repo.GetName(), repo.GetNamespace())
			if err := p.cancelAllInProgressBelongingToClosedPullRequest(ctx, repo); err != nil {
				return fmt.Errorf("error cancelling in progress pipelineRuns belonging to pull request %d: %w", p.event.PullRequestNumber, err)
			}
		} else {
			p.debugf("pull request closed: no repo match found for event url=%s", p.event.URL)
		}
		return nil
	}

	matchedPRs, repo, err := p.matchRepoPR(ctx)
	if err != nil {
		createStatusErr := p.vcx.CreateStatus(ctx, p.event, providerstatus.StatusOpts{
			Status:     CompletedStatus,
			Conclusion: providerstatus.ConclusionFailure,
			Text:       fmt.Sprintf("There was an issue validating the commit: %q", err),
			DetailsURL: p.run.Clients.ConsoleUI().URL(),
		})
		p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryCreateStatus", fmt.Sprintf("an error occurred: %s", err))
		if createStatusErr != nil {
			p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryCreateStatus", fmt.Sprintf("cannot create status: %s: %s", err, createStatusErr))
		}
	}
	repoName := "<nil>"
	repoNamespace := "<nil>"
	if repo != nil {
		repoName = repo.GetName()
		repoNamespace = repo.GetNamespace()
	}
	p.debugf("match results: matched=%d repo=%s/%s", len(matchedPRs), repoNamespace, repoName)
	if len(matchedPRs) == 0 {
		p.debugf("no pipelineruns matched; returning without starting any runs")
		return nil
	}
	if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit != 0 {
		p.debugf("enabling concurrency manager with limit=%d", *repo.Spec.ConcurrencyLimit)
		p.manager.Enable()
	}

	// Defensive skip-CI check: this is a safety net in case events bypass the early check in sinker.
	// Primary skip detection happens in sinker.processEvent() for performance, but this ensures
	// nothing slips through (e.g., tests that call Run() directly, or edge cases).
	// Skip only for non-GitOps events (GitOps commands can override skip-CI).
	if p.event.HasSkipCommand && !opscomments.IsAnyOpsEventType(p.event.EventType) {
		p.logger.Infof("CI skipped: commit contains skip command in message (secondary check)")
		return nil
	}

	// set params for the console driver, only used for the custom console ones
	p.debugf("resolving custom params for console UI")
	cp := customparams.NewCustomParams(p.event, repo, p.run, p.k8int, p.eventEmitter, p.vcx)
	maptemplate, _, err := cp.GetParams(ctx)
	if err != nil {
		p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "ParamsError",
			fmt.Sprintf("error processing repository CR custom params: %s", err.Error()))
	} else {
		p.debugf("resolved %d custom params for console UI", len(maptemplate))
	}
	p.run.Clients.ConsoleUI().SetParams(maptemplate)

	var wg sync.WaitGroup
	for i, match := range matchedPRs {
		if match.Repo == nil {
			match.Repo = repo
		}

		// After matchRepo func fetched repo from k8s api repo is updated and
		// need to merge global repo again
		if p.globalRepo != nil {
			match.Repo.Spec.Merge(p.globalRepo.Spec)
		}

		wg.Add(1)

		go func(match matcher.Match, i int) {
			defer wg.Done()
			p.debugf("starting pipelinerun %s (index=%d)", match.PipelineRun.GetGenerateName(), i)
			pr, err := p.startPR(ctx, match)
			if err != nil {
				errMsg := fmt.Sprintf("There was an error starting the PipelineRun %s, %s", match.PipelineRun.GetGenerateName(), err.Error())
				errMsgM := fmt.Sprintf("There was an error creating the PipelineRun: <b>%s</b>\n\n%s", match.PipelineRun.GetGenerateName(), err.Error())
				p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryPipelineRun", errMsg)
				prName := match.PipelineRun.GetName()
				if prName == "" {
					prName = match.PipelineRun.GetGenerateName()
				}
				createStatusErr := p.vcx.CreateStatus(ctx, p.event, providerstatus.StatusOpts{
					PipelineRunName:          prName,
					PipelineRun:              match.PipelineRun,
					OriginalPipelineRunName:  match.PipelineRun.GetAnnotations()[keys.OriginalPRName],
					Status:                   CompletedStatus,
					Title:                    "pipelinerun start failure",
					Conclusion:               providerstatus.ConclusionFailure,
					Text:                     errMsgM,
					DetailsURL:               p.run.Clients.ConsoleUI().URL(),
					InstanceCountForCheckRun: i,
				})
				if createStatusErr != nil {
					p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryCreateStatus", fmt.Sprintf("Cannot create status: %s: %s", err, createStatusErr))
				}
			}
			if pr != nil {
				p.debugf("pipelinerun started: name=%s namespace=%s", pr.GetName(), pr.GetNamespace())
			}
			p.manager.AddPipelineRun(pr)
			if err := p.cancelInProgressMatchingPipelineRun(ctx, pr, repo); err != nil {
				p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryPipelineRun", fmt.Sprintf("error cancelling in progress pipelineRuns: %s", err))
			}
			p.debugf("finished processing pipelinerun start: name=%s", match.PipelineRun.GetGenerateName())
		}(match, i)
	}
	wg.Wait()

	order, prs := p.manager.GetExecutionOrder()
	if order != "" {
		p.debugf("patching execution order for %d pipelineruns: %s", len(prs), order)
		for _, pr := range prs {
			wg.Add(1)

			go func(order string, pr tektonv1.PipelineRun) {
				defer wg.Done()
				if _, err := action.PatchPipelineRun(ctx, p.logger, "execution order", p.run.Clients.Tekton, &pr, getExecutionOrderPatch(order)); err != nil {
					errMsg := fmt.Sprintf("Failed to patch pipelineruns %s execution order: %s", pr.GetGenerateName(), err.Error())
					p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryPipelineRun", errMsg)
					return
				}
			}(order, *pr)
		}
	}
	wg.Wait()
	return nil
}

func (p *PacRun) startPR(ctx context.Context, match matcher.Match) (*tektonv1.PipelineRun, error) {
	prName := match.PipelineRun.GetName()
	if prName == "" {
		prName = match.PipelineRun.GetGenerateName()
	}
	p.debugf("startPR: pipelinerun=%s namespace=%s event_sha=%s target_branch=%s",
		prName,
		match.Repo.GetNamespace(),
		p.event.SHA,
		p.event.BaseBranch,
	)

	// Add labels and annotations to pipelinerun
	err := kubeinteraction.AddLabelsAndAnnotations(p.event, match.PipelineRun, match.Repo, p.vcx.GetConfig(), p.run)
	if err != nil {
		p.logger.Errorf("Error adding labels/annotations to PipelineRun '%s' in namespace '%s': %v", match.PipelineRun.GetName(), match.Repo.GetNamespace(), err)
	} else {
		p.debugf("startPR: added labels/annotations to pipelinerun=%s", prName)
	}

	// if concurrency is defined then start the pipelineRun in pending state
	if match.Repo.Spec.ConcurrencyLimit != nil && *match.Repo.Spec.ConcurrencyLimit != 0 {
		// pending status
		match.PipelineRun.Spec.Status = tektonv1.PipelineRunSpecStatusPending
		p.debugf("startPR: marking pipelinerun=%s as pending due to concurrency limit", prName)
	}

	// Create the actual pipelineRun
	p.debugf("startPR: creating pipelinerun=%s in namespace=%s", prName, match.Repo.GetNamespace())
	pr, err := p.run.Clients.Tekton.TektonV1().PipelineRuns(match.Repo.GetNamespace()).Create(ctx,
		match.PipelineRun, metav1.CreateOptions{})
	if err != nil {
		// we need to make difference between markdown error and normal error that goes to namespace/controller stream
		return nil, fmt.Errorf("creating pipelinerun %s in namespace %s has failed.\n\nTekton Controller has reported this error: ```%w``` ", match.PipelineRun.GetGenerateName(),
			match.Repo.GetNamespace(), err)
	}

	// Create status with the log url
	p.logger.Infof("PipelineRun %s has been created in namespace %s with status %s for SHA: %s Target Branch: %s",
		pr.GetName(), match.Repo.GetNamespace(), pr.Spec.Status, p.event.SHA, p.event.BaseBranch)

	consoleURL := p.run.Clients.ConsoleUI().DetailURL(pr)
	mt := formatting.MessageTemplate{
		PipelineRunName: pr.GetName(),
		Namespace:       match.Repo.GetNamespace(),
		ConsoleName:     p.run.Clients.ConsoleUI().GetName(),
		ConsoleURL:      consoleURL,
		TknBinary:       settings.TknBinaryName,
		TknBinaryURL:    settings.TknBinaryURL,
	}

	msg, err := mt.MakeTemplate(p.vcx.GetTemplate(provider.StartingPipelineType))
	if err != nil {
		return nil, fmt.Errorf("cannot create message template: %w", err)
	}
	status := providerstatus.StatusOpts{
		Status:                  inProgressStatus,
		Conclusion:              providerstatus.ConclusionPending,
		Text:                    msg,
		DetailsURL:              consoleURL,
		PipelineRunName:         pr.GetName(),
		PipelineRun:             pr,
		OriginalPipelineRunName: pr.GetAnnotations()[keys.OriginalPRName],
	}

	// Patch the pipelineRun with the appropriate annotations and labels.
	// Set the state so the watcher will continue with reconciling the pipelineRun
	// The watcher reconciles only pipelineRuns that has the state annotation.
	patchAnnotations := map[string]string{}
	patchLabels := map[string]string{}
	whatPatching := ""
	// if pipelineRun is in pending state then report status as queued
	// The pipelineRun can be pending because of PAC's concurrency limit or because of an external mutatingwebhook
	if pr.Spec.Status == tektonv1.PipelineRunSpecStatusPending {
		status.Status = queuedStatus
		if status.Text, err = mt.MakeTemplate(p.vcx.GetTemplate(provider.QueueingPipelineType)); err != nil {
			return nil, fmt.Errorf("cannot create message template: %w", err)
		}
		whatPatching = "annotations.state and labels.state"
		patchAnnotations[keys.State] = kubeinteraction.StateQueued
		patchLabels[keys.State] = kubeinteraction.StateQueued
	} else {
		// Mark that the start will be reported to the Git provider
		patchAnnotations[keys.SCMReportingPLRStarted] = "true"
		patchAnnotations[keys.State] = kubeinteraction.StateStarted
		patchLabels[keys.State] = kubeinteraction.StateStarted
		whatPatching = fmt.Sprintf(
			"annotation.%s and annotations.state and labels.state",
			keys.SCMReportingPLRStarted,
		)
	}

	if err := p.vcx.CreateStatus(ctx, p.event, status); err != nil {
		// we still return the created PR with error, and allow caller to decide what to do with the PR, and avoid
		// unneeded SIGSEGV's
		return pr, fmt.Errorf("cannot use the API on the provider platform to create a in_progress status: %w", err)
	}
	p.debugf("startPR: created status for pipelinerun=%s status=%s conclusion=%s", pr.GetName(), status.Status, status.Conclusion)

	// Patch pipelineRun with logURL annotation, skips for GitHub App as we patch logURL while patching CheckrunID
	if _, ok := pr.Annotations[keys.InstallationID]; !ok {
		patchAnnotations[keys.LogURL] = p.run.Clients.ConsoleUI().DetailURL(pr)
		whatPatching = "annotations.logURL, " + whatPatching
	}

	if len(patchAnnotations) > 0 || len(patchLabels) > 0 {
		p.debugf("startPR: patching pipelinerun=%s patches=%s annotations=%d labels=%d", pr.GetName(), whatPatching, len(patchAnnotations), len(patchLabels))
		pr, err = action.PatchPipelineRun(ctx, p.logger, whatPatching, p.run.Clients.Tekton, pr, getMergePatch(patchAnnotations, patchLabels))
		if err != nil {
			// if PipelineRun patch is failed then do not return error, just log the error
			// because its a false negative and on startPR return a failed check is being created
			// due to this.
			p.logger.Errorf("cannot patch pipelinerun %s: %w", pr.GetGenerateName(), err)
			return pr, nil
		}
		currentReason := ""
		if len(pr.Status.GetConditions()) > 0 {
			currentReason = pr.Status.GetConditions()[0].GetReason()
		}

		p.logger.Infof("PipelineRun %s/%s patched successfully - Spec.Status: %s, State annotation: '%s', SCMReportingPLRStarted annotation: '%s', Status reason: '%s', Git provider status: '%s', Patched: %s",
			pr.GetNamespace(),
			pr.GetName(),
			pr.Spec.Status,
			pr.GetAnnotations()[keys.State],
			pr.GetAnnotations()[keys.SCMReportingPLRStarted],
			currentReason,
			status.Status,
			whatPatching)
	}

	return pr, nil
}

func getMergePatch(annotations, labels map[string]string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"annotations": annotations,
			"labels":      labels,
		},
	}
}

func getExecutionOrderPatch(order string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				keys.ExecutionOrder: order,
			},
		},
	}
}
