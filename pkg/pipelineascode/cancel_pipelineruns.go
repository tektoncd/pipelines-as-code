package pipelineascode

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/resolve"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
)

type matchingCond func(pr tektonv1.PipelineRun) bool

var cancelMergePatch = map[string]any{
	"spec": map[string]any{
		"status": tektonv1.PipelineRunSpecStatusCancelledRunFinally,
	},
}

// cancelAllInProgressBelongingToClosedPullRequest cancels all in-progress PipelineRuns
// that belong to a specific pull request in the given repository.
func (p *PacRun) cancelAllInProgressBelongingToClosedPullRequest(ctx context.Context, repo *v1alpha1.Repository) error {
	labelsMap := map[string]string{
		keys.URLRepository: formatting.CleanValueKubernetes(p.event.Repository),
		keys.PullRequest:   strconv.Itoa(p.event.PullRequestNumber),
	}
	operator := selection.Equals
	cancelInProgress := fmt.Sprintf("%t", p.pacInfo.EnableCancelInProgressOnPullRequests)
	p.debugf("cancelAllInProgress: repo=%s/%s pr=%d cancel_in_progress=%s", repo.GetNamespace(), repo.GetName(), p.event.PullRequestNumber, cancelInProgress)

	// First, build the label selector based on the URLRepository and PullRequest fields,
	// followed by applying filtering logic for the 'cancel-in-progress' annotation.
	labelSelector := getLabelSelector(labelsMap, operator)
	labelSelector += fmt.Sprintf(",%s in (pull_request, Merge_Request, %s)", keys.EventType, opscomments.AnyOpsKubeLabelInSelector())

	if cancelInProgress == "true" {
		// When the 'cancel-in-progress' setting is enabled globally via the Pipelines-as-Code ConfigMap,
		// then exclude those PipelineRuns that explicitly override this setting by having the
		// 'cancel-in-progress' annotation set to 'false' and continue cancelling others which are having
		// 'cancel-in-progress' annotation set to 'true' or with no annotation specified at all and are
		// subjected to cancellation under the global setting.
		//
		// Note: The 'selection.NotIn' operator is used to exclude PipelineRuns that have the
		// 'cancel-in-progress' annotation explicitly set to 'false', effectively opting them out of cancellation.
		labelsMap = map[string]string{keys.CancelInProgress: "false"}
		operator = selection.NotIn //codespell:ignore 'NotIn'
	} else {
		// When the 'cancel-in-progress' setting is disabled globally via the Pipelines-as-Code ConfigMap,
		// filter and list only those PipelineRuns that explicitly override the global setting by having the
		// 'cancel-in-progress' annotation set to 'true'.
		labelsMap = map[string]string{keys.CancelInProgress: "true"}
	}
	// Append the label selector filter for the 'cancel-in-progress' annotation.
	labelSelector += fmt.Sprintf(",%s", getLabelSelector(labelsMap, operator))
	p.debugf("cancelAllInProgress: labelSelector=%s", labelSelector)

	prs, err := p.run.Clients.Tekton.TektonV1().PipelineRuns(repo.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to list pipelineRuns : %w", err)
	}

	if len(prs.Items) == 0 {
		msg := fmt.Sprintf("no pipelinerun found for repository: %v and pullRequest %v",
			p.event.Repository, p.event.PullRequestNumber)
		p.eventEmitter.EmitMessage(repo, zap.InfoLevel, "CancelInProgress", msg)
		return nil
	}
	p.debugf("cancelAllInProgress: found %d pipelineruns to consider", len(prs.Items))

	p.cancelPipelineRuns(
		ctx,
		prs,
		repo,
		"The pull request was closed, and this PipelineRun matched the cancel-in-progress criteria.",
		func(_ tektonv1.PipelineRun) bool {
			return true
		})

	return nil
}

// cancelInProgressMatchingPR cancels all PipelineRuns associated with a given repository and pull request,
// except for the one that triggered the cancellation. It first checks if the cancellation is in progress
// and if the repository has a concurrency limit. If a concurrency limit is set, it returns an error as
// cancellation is not supported with concurrency limits. It then retrieves the original pull request name
// from the annotations and lists all PipelineRuns with matching labels. For each PipelineRun that is not
// already done, cancelled, or gracefully stopped, it patches the PipelineRun to cancel it.
func (p *PacRun) cancelInProgressMatchingPipelineRun(ctx context.Context, matchPR *tektonv1.PipelineRun, repo *v1alpha1.Repository) error {
	if matchPR == nil {
		return nil
	}

	cancelInProgress := ""
	cancellingVia := "globally via Pipelines-as-Code ConfigMap"
	// First, check value from Pipelines-as-Code ConfigMap for the event type
	if p.event.TriggerTarget == triggertype.PullRequest { //nolint: staticcheck
		cancelInProgress = fmt.Sprintf("%t", p.pacInfo.EnableCancelInProgressOnPullRequests)
	} else if p.event.TriggerTarget == triggertype.Push {
		cancelInProgress = fmt.Sprintf("%t", p.pacInfo.EnableCancelInProgressOnPush)
	}

	// As per feature behavior, PipelineRun annotation should override setting of Pipelines-as-Code ConfigMap
	if value, ok := matchPR.GetAnnotations()[keys.CancelInProgress]; ok {
		cancelInProgress = value
		cancellingVia = "via PipelineRun annotation"
	}

	if cancelInProgress != "true" {
		p.debugf("cancelInProgress: disabled for pipelinerun=%s via=%s", matchPR.GetName(), cancellingVia)
		return nil
	}

	p.run.Clients.Log.Infof("cancel-in-progress for event %s is enabled %s", string(p.event.TriggerTarget), cancellingVia)
	p.debugf("cancelInProgress: enabled for pipelinerun=%s", matchPR.GetName())

	// As PipelineRuns are filtered by name, OriginalPRName should be taken from
	// labels instead of annotations because of constraints imposed by kube API.
	prName, ok := matchPR.GetLabels()[keys.OriginalPRName]
	if !ok {
		p.debugf("cancelInProgress: missing original PR name label for pipelinerun=%s", matchPR.GetName())
		return nil
	}

	if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit > 0 {
		return fmt.Errorf("cancel in progress is not supported with concurrency limit")
	}

	labelMap := map[string]string{
		keys.URLRepository:  formatting.CleanValueKubernetes(p.event.Repository),
		keys.OriginalPRName: prName,
	}
	if p.event.TriggerTarget == triggertype.PullRequest {
		labelMap[keys.PullRequest] = strconv.Itoa(p.event.PullRequestNumber)
	}
	labelSelector := getLabelSelector(labelMap, selection.Equals)
	if p.event.TriggerTarget == triggertype.PullRequest {
		// "Merge_Request" included since EventType is not normalized to "Pull Request" like TriggerTarget
		labelSelector += fmt.Sprintf(",%s in (pull_request, Merge_Request, %s)", keys.EventType, opscomments.AnyOpsKubeLabelInSelector())
	}
	p.run.Clients.Log.Infof("cancel-in-progress: selecting pipelineRuns to cancel with labels: %v", labelSelector)
	p.debugf("cancelInProgress: labelSelector=%s", labelSelector)
	prs, err := p.run.Clients.Tekton.TektonV1().PipelineRuns(matchPR.GetNamespace()).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to list pipelineRuns : %w", err)
	}

	p.cancelPipelineRuns(
		ctx,
		prs,
		repo,
		fmt.Sprintf(
			"A newer %s event matched the same PipelineRun definition, and cancel-in-progress is enabled.",
			p.event.TriggerTarget,
		),
		func(pr tektonv1.PipelineRun) bool {
			// skip our own for cancellation
			if sourceBranch, ok := pr.GetAnnotations()[keys.SourceBranch]; ok {
				// NOTE(chmouel): Every PR has their own branch and so is every push to different branch
				// it means we only cancel pipelinerun of the same name that runs to
				// the unique branch. Note: HeadBranch is the branch from where the PR
				// comes from in git jargon.
				if strings.TrimPrefix(sourceBranch, "refs/heads/") != strings.TrimPrefix(p.event.HeadBranch, "refs/heads/") {
					p.logger.Infof("cancel-in-progress: skipping pipelinerun %v/%v as it is not from the same branch, annotation source-branch: %s event headbranch: %s", pr.GetNamespace(), pr.GetName(), sourceBranch, p.event.HeadBranch)
					return false
				}
			}

			return pr.GetName() != matchPR.GetName()
		})
	return nil
}

// cancelPipelineRunsOpsComment cancels all PipelineRuns associated with a given repository and pull request.
// when the user issue a cancel comment.
func (p *PacRun) cancelPipelineRunsOpsComment(ctx context.Context, repo *v1alpha1.Repository) error {
	repo = p.resolveRepoForTargetCancelPipelineRun(ctx, repo)

	labelSelector := getLabelSelector(map[string]string{
		keys.URLRepository: formatting.CleanValueKubernetes(p.event.Repository),
		keys.SHA:           formatting.CleanValueKubernetes(p.event.SHA),
	}, selection.Equals)

	if p.event.TriggerTarget == triggertype.PullRequest {
		labelSelector = getLabelSelector(map[string]string{
			keys.URLRepository: formatting.CleanValueKubernetes(p.event.Repository),
			keys.PullRequest:   strconv.Itoa(p.event.PullRequestNumber),
		}, selection.Equals)
	}

	prs, err := p.run.Clients.Tekton.TektonV1().PipelineRuns(repo.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to list pipelineRuns : %w", err)
	}

	if len(prs.Items) == 0 {
		msg := fmt.Sprintf("no pipelinerun found for repository: %v , sha: %v and pulRequest %v",
			p.event.Repository, p.event.SHA, p.event.PullRequestNumber)
		p.eventEmitter.EmitMessage(repo, zap.InfoLevel, "CancelInProgress", msg)
		return nil
	}
	p.debugf("cancelPipelineRunsOpsComment: found %d pipelineruns to consider", len(prs.Items))

	p.cancelPipelineRuns(
		ctx,
		prs,
		repo,
		"Cancellation was requested for this PipelineRun via a /cancel command.",
		func(pr tektonv1.PipelineRun) bool {
			if p.event.TargetCancelPipelineRun != "" {
				if prName, ok := pr.GetAnnotations()[keys.OriginalPRName]; !ok || prName != p.event.TargetCancelPipelineRun {
					return false
				}
			}
			return true
		})

	return nil
}

func (p *PacRun) resolveRepoForTargetCancelPipelineRun(ctx context.Context, repo *v1alpha1.Repository) *v1alpha1.Repository {
	if repo == nil || p.event.TargetCancelPipelineRun == "" || p.vcx == nil {
		return repo
	}

	provenance := "source"
	if repo.Spec.Settings != nil && repo.Spec.Settings.PipelineRunProvenance != "" {
		provenance = repo.Spec.Settings.PipelineRunProvenance
	}

	rawTemplates, err := p.vcx.GetTektonDir(ctx, p.event, tektonDir, provenance)
	if err != nil || rawTemplates == "" {
		return repo
	}

	allTemplates := p.makeTemplate(ctx, repo, rawTemplates)
	types, err := resolve.ReadTektonTypes(ctx, p.logger, allTemplates)
	if err != nil {
		return repo
	}

	pipelineRuns, err := resolve.MetadataResolve(types.PipelineRuns)
	if err != nil || len(pipelineRuns) == 0 {
		return repo
	}

	targetPR := filterRunningPipelineRunOnTargetTest(p.event.TargetCancelPipelineRun, pipelineRuns)
	if targetPR == nil {
		for _, pr := range pipelineRuns {
			prName := strings.TrimSuffix(pipelineRunIdentifier(pr), "-")
			if prName == p.event.TargetCancelPipelineRun {
				targetPR = pr
				break
			}
		}
	}
	if targetPR == nil {
		return repo
	}

	targetRepo := p.resolveTargetNamespaceRepo(ctx, repo, targetPR)
	if targetRepo == nil {
		p.logger.Warnf(
			"resolveRepoForTargetCancelPipelineRun: target repo not found for pipelinerun=%s, falling back to repo=%s/%s",
			pipelineRunIdentifier(targetPR),
			repo.GetNamespace(),
			repo.GetName(),
		)
		return repo
	}
	return targetRepo
}

func (p *PacRun) cancelPipelineRuns(
	ctx context.Context,
	prs *tektonv1.PipelineRunList,
	repo *v1alpha1.Repository,
	reason string,
	condition matchingCond,
) {
	var wg sync.WaitGroup
	p.debugf("cancelPipelineRuns: evaluating %d pipelineruns", len(prs.Items))
	for _, pr := range prs.Items {
		if !condition(pr) {
			continue
		}

		if pr.IsCancelled() || pr.IsGracefullyCancelled() || pr.IsGracefullyStopped() {
			p.logger.Infof("cancel-in-progress: skipping cancelling pipelinerun %v/%v, already in %v state", pr.GetNamespace(), pr.GetName(), pr.Spec.Status)
			continue
		}

		if pr.IsDone() {
			p.logger.Infof("cancel-in-progress: skipping cancelling pipelinerun %v/%v, already done", pr.GetNamespace(), pr.GetName())
			continue
		}

		p.logger.Infof("cancel-in-progress: cancelling pipelinerun %v/%v: %s", pr.GetNamespace(), pr.GetName(), reason)
		wg.Add(1)
		go func(ctx context.Context, pr tektonv1.PipelineRun) {
			defer wg.Done()
			if _, err := action.PatchPipelineRun(ctx, p.logger, "cancel patch", p.run.Clients.Tekton, &pr, cancelMergePatch); err != nil {
				errMsg := fmt.Sprintf("failed to cancel pipelineRun %s/%s: %s", pr.GetNamespace(), pr.GetName(), err.Error())
				p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "CancelInProgress", errMsg)
			}
		}(ctx, pr)
	}
	wg.Wait()
}

func getLabelSelector(labelsMap map[string]string, operator selection.Operator) string {
	labelSelector := labels.NewSelector()
	for k, v := range labelsMap {
		req, _ := labels.NewRequirement(k, operator, []string{v})
		if req != nil {
			labelSelector = labelSelector.Add(*req)
		}
	}
	return labelSelector.String()
}
