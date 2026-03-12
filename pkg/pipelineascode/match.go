package pipelineascode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pacerrors "github.com/openshift-pipelines/pipelines-as-code/pkg/errors"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/resolve"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/templates"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
)

func (p *PacRun) matchRepoPR(ctx context.Context) ([]matcher.Match, *v1alpha1.Repository, error) {
	p.debugf("matchRepoPR: starting repo verification for url=%s", p.event.URL)
	repo, err := p.verifyRepoAndUser(ctx)
	if err != nil {
		return nil, nil, err
	}
	if repo == nil {
		p.debugf("matchRepoPR: no repository match for url=%s", p.event.URL)
		return nil, nil, nil
	}

	if p.event.CancelPipelineRuns {
		p.debugf("matchRepoPR: cancel pipeline runs requested, skipping match")
		return nil, repo, p.cancelPipelineRunsOpsComment(ctx, repo)
	}

	p.debugf("matchRepoPR: fetching pipelineruns from repo=%s/%s", repo.GetNamespace(), repo.GetName())
	matchedPRs, err := p.getPipelineRunsFromRepo(ctx, repo)
	if err != nil {
		return nil, repo, err
	}

	p.debugf("matchRepoPR: matched=%d repo=%s/%s", len(matchedPRs), repo.GetNamespace(), repo.GetName())
	return matchedPRs, repo, nil
}

// verifyRepoAndUser verifies if the Repo CR exists for the Git Repository,
// if the user has permission to run CI  and also initialise provider client.
func (p *PacRun) verifyRepoAndUser(ctx context.Context) (*v1alpha1.Repository, error) {
	// Match the Event URL to a Repository URL,
	p.debugf("verifyRepoAndUser: matching repository for url=%s", p.event.URL)
	repo, err := matcher.MatchEventURLRepo(ctx, p.run, p.event, "")
	if err != nil {
		return nil, fmt.Errorf("error matching Repository for event: %w", err)
	}

	if repo == nil {
		msg := fmt.Sprintf("cannot find a repository match for %s", p.event.URL)
		p.eventEmitter.EmitMessage(nil, zap.WarnLevel, "RepositoryNamespaceMatch", msg)
		return nil, nil
	}
	p.debugf("verifyRepoAndUser: matched repo=%s/%s", repo.GetNamespace(), repo.GetName())

	p.logger = p.logger.With("namespace", repo.Namespace)
	p.vcx.SetLogger(p.logger)
	p.eventEmitter.SetLogger(p.logger)

	// Set up authenticated client with proper token scoping
	// NOTE: This is typically already done in sinker.processEvent() for all event types,
	// but we call it here as a safety net for edge cases (e.g., tests calling Run() directly,
	// or if the early setup in sinker failed/was skipped). The call is idempotent.
	// SetupAuthenticatedClient will merge global repo settings after determining secret namespace.
	err = SetupAuthenticatedClient(ctx, p.vcx, p.k8int, p.run, p.event, repo, p.globalRepo, p.pacInfo, p.logger)
	if err != nil {
		return repo, err
	}
	p.debugf("verifyRepoAndUser: authenticated client setup complete")

	// Get the SHA commit info, we want to get the URL and commit title
	if p.event.SHA == "" || p.event.SHATitle == "" || p.event.SHAURL == "" {
		p.debugf("verifyRepoAndUser: fetching commit info")
		if err = p.vcx.GetCommitInfo(ctx, p.event); err != nil {
			return repo, fmt.Errorf("could not find commit info: %w", err)
		}
		p.debugf("verifyRepoAndUser: commit info loaded sha=%s title=%s", p.event.SHA, p.event.SHATitle)
	}

	// Verify whether the sender of the GitOps command (e.g., /test) has the appropriate permissions to
	// trigger CI on the repository, as any user is able to comment on a pushed commit in open-source repositories.
	if p.event.TriggerTarget == triggertype.Push && opscomments.IsAnyOpsEventType(p.event.EventType) {
		p.debugf("verifyRepoAndUser: checking access for gitops comment on push")
		status := providerstatus.StatusOpts{
			Status:       CompletedStatus,
			Title:        "Permission denied",
			Conclusion:   providerstatus.ConclusionFailure,
			DetailsURL:   p.event.URL,
			AccessDenied: true,
		}
		if allowed, err := p.checkAccessOrError(ctx, repo, status, "by GitOps comment on push commit"); !allowed {
			return nil, err
		}
	}

	// Check if the submitter is allowed to run this.
	// on push we don't need to check the policy since the user has pushed to the repo so it has access to it.
	// on comment we skip it for now, we are going to check later on
	if p.event.TriggerTarget != triggertype.Push && p.event.EventType != opscomments.NoOpsCommentEventType.String() {
		p.debugf("verifyRepoAndUser: checking access for trigger target=%s event_type=%s", p.event.TriggerTarget, p.event.EventType)
		status := providerstatus.StatusOpts{
			Status:       queuedStatus,
			Title:        "Pending approval, waiting for an /ok-to-test",
			Conclusion:   providerstatus.ConclusionPending,
			DetailsURL:   p.event.URL,
			AccessDenied: true,
		}
		if allowed, err := p.checkAccessOrError(ctx, repo, status, "via "+p.event.TriggerTarget.String()); !allowed {
			return nil, err
		}
		// When /ok-to-test is approved, update the parent "Pipelines as Code CI" status to success
		// to indicate the approval was successful before pipelines start running.
		if p.event.EventType == opscomments.OkToTestCommentEventType.String() && p.vcx.GetConfig().Name == "gitea" {
			approvalStatus := providerstatus.StatusOpts{
				Status:     CompletedStatus,
				Title:      "Approved",
				Conclusion: providerstatus.ConclusionSuccess,
				DetailsURL: p.event.URL,
			}
			if err := p.vcx.CreateStatus(ctx, p.event, approvalStatus); err != nil {
				p.logger.Warnf("failed to update parent status on /ok-to-test approval: %v", err)
			}
		}
	}
	return repo, nil
}

// getPipelineRunsFromRepo fetches pipelineruns from git repository and prepare them for creation.
func (p *PacRun) getPipelineRunsFromRepo(ctx context.Context, repo *v1alpha1.Repository) ([]matcher.Match, error) {
	provenance := "source"
	if repo.Spec.Settings != nil && repo.Spec.Settings.PipelineRunProvenance != "" {
		provenance = repo.Spec.Settings.PipelineRunProvenance
	}
	p.debugf("getPipelineRunsFromRepo: repo=%s/%s provenance=%s", repo.GetNamespace(), repo.GetName(), provenance)
	rawTemplates, err := p.vcx.GetTektonDir(ctx, p.event, tektonDir, provenance)
	if err != nil && p.event.TriggerTarget == triggertype.PullRequest && strings.Contains(err.Error(), "error unmarshalling yaml file") {
		// make the error a bit more friendly for users who don't know what marshalling or intricacies of the yaml parser works
		// format is "error unmarshalling yaml file pr-bad-format.yaml: yaml: line 3: could not find expected ':'"
		// get the filename with a regexp
		reg := regexp.MustCompile(`error unmarshalling yaml file\s([^:]*):\s*(yaml:\s*)?(.*)`)
		matches := reg.FindStringSubmatch(err.Error())
		if len(matches) == 4 {
			p.reportValidationErrors(ctx, repo,
				[]*pacerrors.PacYamlValidations{
					{
						Name:   matches[1],
						Err:    fmt.Errorf("yaml validation error: %s", matches[3]),
						Schema: pacerrors.GenericBadYAMLValidation,
					},
				},
			)
			return nil, nil
		}

		return nil, err
	}

	if err != nil {
		p.debugf("getPipelineRunsFromRepo: GetTektonDir returned error: %v", err)
	} else {
		p.debugf("getPipelineRunsFromRepo: fetched templates length=%d", len(rawTemplates))
	}

	if rawTemplates == "" && p.event.EventType == opscomments.OkToTestCommentEventType.String() {
		err = p.createNeutralStatus(ctx, ".tekton directory not found", tektonDirMissingError)
		if err != nil {
			p.eventEmitter.EmitMessage(nil, zap.ErrorLevel, "RepositoryCreateStatus", err.Error())
		}
	}

	// This is for push event error logging because we can't create comment for yaml validation errors on push
	if err != nil || rawTemplates == "" {
		msg := ""
		reason := "RepositoryPipelineRunNotFound"
		logLevel := zap.InfoLevel
		if err != nil {
			reason = "RepositoryInvalidPipelineRunTemplate"
			logLevel = zap.ErrorLevel
			if strings.Contains(err.Error(), "error unmarshalling yaml file") {
				msg = "PipelineRun YAML validation"
			}
			msg += fmt.Sprintf(" err: %s", err.Error())
		} else {
			msg = fmt.Sprintf("cannot locate templates in %s/ directory for this repository in %s", tektonDir, p.event.HeadBranch)
		}
		p.eventEmitter.EmitMessage(nil, logLevel, reason, msg)
		return nil, nil
	}

	// check for condition if need update the pipelinerun with regexp from the
	// "raw" pipelinerun string
	if msg, needUpdate := p.checkNeedUpdate(rawTemplates); needUpdate {
		p.eventEmitter.EmitMessage(repo, zap.InfoLevel, "RepositoryNeedUpdate", msg)
		return nil, fmt.Errorf("%s", msg)
	}

	// This is for bitbucket
	if p.event.CloneURL == "" {
		p.event.AccountID = ""
	}

	// NOTE(chmouel): Initially, matching is performed here to accurately
	// expand dynamic matching in events. This expansion is crucial for
	// applying dynamic variables, such as setting the `event_type` to
	// `on-comment` when matching a git provider's issue comment event with a
	// comment in an annotation. Although matching occurs three times within
	// this loop, which might seem inefficient, it's essential to maintain
	// current functionality without introducing potential errors or behavior
	// changes. Refactoring for optimization could lead to significant
	// challenges in tracking down issues. Despite the repetition, the
	// performance impact is minimal, involving only a loop and a few
	// conditions.
	if p.event.TargetTestPipelineRun == "" {
		rtypes, err := resolve.ReadTektonTypes(ctx, p.logger, rawTemplates)
		if err != nil {
			return nil, err
		}
		p.debugf("getPipelineRunsFromRepo: pre-parse types: pipelineruns=%d pipelines=%d tasks=%d", len(rtypes.PipelineRuns), len(rtypes.Pipelines), len(rtypes.Tasks))
		// Don't fail or do anything if we don't have a match yet, we will do it properly later in this function
		_, _ = matcher.MatchPipelinerunByAnnotation(ctx, p.logger, rtypes.PipelineRuns, p.run, p.event, p.vcx, p.eventEmitter, repo, false)
	}
	// Replace those {{var}} placeholders user has in her template to the run.Info variable
	allTemplates := p.makeTemplate(ctx, repo, rawTemplates)
	p.debugf("getPipelineRunsFromRepo: templated data length=%d", len(allTemplates))

	types, err := resolve.ReadTektonTypes(ctx, p.logger, allTemplates)
	if err != nil {
		return nil, err
	}
	p.debugf("getPipelineRunsFromRepo: parsed types: pipelineruns=%d pipelines=%d tasks=%d validation_errors=%d", len(types.PipelineRuns), len(types.Pipelines), len(types.Tasks), len(types.ValidationErrors))

	if len(types.ValidationErrors) > 0 && p.event.TriggerTarget == triggertype.PullRequest {
		p.reportValidationErrors(ctx, repo, types.ValidationErrors)
	}
	pipelineRuns := types.PipelineRuns
	if len(pipelineRuns) == 0 {
		msg := fmt.Sprintf("cannot locate valid templates in %s/ directory for this repository in %s", tektonDir, p.event.HeadBranch)
		p.eventEmitter.EmitMessage(nil, zap.InfoLevel, "RepositoryCannotLocatePipelineRun", msg)
		return nil, nil
	}
	p.debugf("getPipelineRunsFromRepo: pipelineRuns count=%d", len(pipelineRuns))
	pipelineRuns, err = resolve.MetadataResolve(pipelineRuns)
	if err != nil && len(pipelineRuns) == 0 {
		// Don't report errors for no-ops comment events to avoid a webhook feedback loop:
		// reporting creates a comment, which triggers another webhook, which hits the same error.
		if p.event.EventType == opscomments.NoOpsCommentEventType.String() {
			p.logger.Infof("skipping MetadataResolve error for no-ops comment event: %s", err)
			return nil, nil
		}
		p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "FailedToResolvePipelineRunMetadata", err.Error())
		return nil, err
	}
	p.debugf("getPipelineRunsFromRepo: metadata resolved for pipelineRuns count=%d", len(pipelineRuns))

	// Match the PipelineRun with annotation
	var matchedPRs []matcher.Match
	if p.event.TargetTestPipelineRun == "" {
		if matchedPRs, err = matcher.MatchPipelinerunByAnnotation(ctx, p.logger, pipelineRuns, p.run, p.event, p.vcx, p.eventEmitter, repo, true); err != nil {
			// Check if all pipelines have already succeeded - post comment so user gets feedback
			if errors.Is(err, matcher.ErrNoFailedPipelineToRetest) {
				p.logger.Infof("RepositoryAllPipelinesSucceeded: %s", err.Error())
				p.eventEmitter.EmitMessage(nil, zap.InfoLevel, "RepositoryAllPipelinesSucceeded", err.Error())
				if commentErr := p.vcx.CreateComment(ctx, p.event, err.Error(), ""); commentErr != nil {
					return nil, fmt.Errorf("error adding no pipelineruns to rerun comment: %w", commentErr)
				}
				return nil, nil
			}
			// Don't fail when you don't have a match between pipeline and annotations
			p.eventEmitter.EmitMessage(nil, zap.WarnLevel, "RepositoryNoMatch", err.Error())
			// In a scenario where an external user submits a pull request and the repository owner uses the
			// GitOps command `/ok-to-test` to trigger CI, but no matching pull request is found,
			// a neutral check-run will be created on the pull request to indicate that no PipelineRun was triggered
			if p.event.EventType == opscomments.OkToTestCommentEventType.String() && len(matchedPRs) == 0 {
				text := fmt.Sprintf("No matching PipelineRun found for the '%s' event in .tekton/ directory. Please ensure that PipelineRun is configured for '%s' event.", p.event.TriggerTarget.String(), p.event.TriggerTarget.String())
				err = p.createNeutralStatus(ctx, "No PipelineRun matched", text)
				if err != nil {
					p.eventEmitter.EmitMessage(nil, zap.WarnLevel, "RepositoryCreateStatus", err.Error())
				}
				p.eventEmitter.EmitMessage(nil, zap.InfoLevel, "RepositoryNoMatch", text)
			}
			return nil, nil
		}
		p.debugf("getPipelineRunsFromRepo: initial match count=%d", len(matchedPRs))
	}

	// if the event is a comment event, but we don't have any match from the keys.OnComment then do the ACL checks again
	// we skipped previously so we can get the match from the event to the pipelineruns
	if p.event.EventType == opscomments.NoOpsCommentEventType.String() || p.event.EventType == opscomments.OnCommentEventType.String() {
		status := providerstatus.StatusOpts{
			Status:       queuedStatus,
			Title:        "Pending approval, waiting for an /ok-to-test",
			Conclusion:   providerstatus.ConclusionPending,
			DetailsURL:   p.event.URL,
			AccessDenied: true,
		}
		if allowed, err := p.checkAccessOrError(ctx, repo, status, "by GitOps comment on push commit"); !allowed {
			return nil, err
		}
	}

	// if event type is incoming then filter out the pipelineruns related to incoming event
	pipelineRuns = matcher.MatchRunningPipelineRunForIncomingWebhook(p.event.EventType, p.event.TargetPipelineRun, pipelineRuns)
	if pipelineRuns == nil {
		msg := fmt.Sprintf("cannot find pipelinerun %s for matching an incoming event in this repository", p.event.TargetPipelineRun)
		p.eventEmitter.EmitMessage(repo, zap.InfoLevel, "RepositoryCannotLocatePipelineRunForIncomingEvent", msg)
		return nil, nil
	}
	p.debugf("getPipelineRunsFromRepo: incoming filter result count=%d", len(pipelineRuns))

	// if /test command is used then filter out the pipelinerun
	if p.event.TargetTestPipelineRun != "" {
		targetPR := filterRunningPipelineRunOnTargetTest(p.event.TargetTestPipelineRun, pipelineRuns)
		if targetPR == nil {
			msg := fmt.Sprintf("cannot find the targeted pipelinerun %s in this repository", p.event.TargetTestPipelineRun)
			p.eventEmitter.EmitMessage(repo, zap.InfoLevel, "RepositoryCannotLocatePipelineRun", msg)
			return nil, nil
		}
		pipelineRuns = []*tektonv1.PipelineRun{targetPR}
		p.debugf("getPipelineRunsFromRepo: filtered to target pipelinerun=%s", p.event.TargetTestPipelineRun)
	}

	// finally resolve with fetching the remote tasks (if enabled)
	if p.pacInfo.RemoteTasks {
		// only resolve on the matched pipelineruns if we don't do explicit /test of unmatched pipelineruns
		if p.event.TargetTestPipelineRun == "" {
			types.PipelineRuns = nil
			for _, match := range matchedPRs {
				for pr := range pipelineRuns {
					if match.PipelineRun.GetName() == "" && match.PipelineRun.GetGenerateName() == pipelineRuns[pr].GenerateName ||
						match.PipelineRun.GetName() != "" && match.PipelineRun.GetName() == pipelineRuns[pr].Name {
						types.PipelineRuns = append(types.PipelineRuns, pipelineRuns[pr])
					}
				}
			}
		}
		p.debugf("getPipelineRunsFromRepo: resolving remote tasks for pipelineRuns=%d", len(types.PipelineRuns))
		pipelineRuns, err = resolve.Resolve(ctx, p.run, p.logger, p.vcx, types, p.event, &resolve.Opts{
			GenerateName: true,
			RemoteTasks:  true,
		})
		if err != nil {
			p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryFailedToMatch", fmt.Sprintf("failed to match pipelineRuns: %s", err.Error()))
			return nil, err
		}
	}

	err = p.changePipelineRun(ctx, repo, pipelineRuns)
	if err != nil {
		return nil, err
	}
	p.debugf("getPipelineRunsFromRepo: updated pipelineRuns count=%d", len(pipelineRuns))
	// if we are doing explicit /test command then we only want to run the one that has matched the /test
	if p.event.TargetTestPipelineRun != "" {
		p.eventEmitter.EmitMessage(repo, zap.InfoLevel, "RepositoryMatchedPipelineRun", fmt.Sprintf("explicit testing via /test of PipelineRun %s", p.event.TargetTestPipelineRun))
		selectedPr := filterRunningPipelineRunOnTargetTest(p.event.TargetTestPipelineRun, pipelineRuns)
		if selectedPr == nil {
			msg := fmt.Sprintf("cannot find the targeted pipelinerun %s in this repository", p.event.TargetTestPipelineRun)
			p.eventEmitter.EmitMessage(repo, zap.InfoLevel, "RepositoryCannotLocatePipelineRun", msg)
			return nil, nil
		}
		selectedRepo := p.resolveTargetNamespaceRepo(ctx, repo, selectedPr)
		if selectedRepo == nil {
			msg := fmt.Sprintf("skipping pipelinerun %s: target-namespace repo not found", pipelineRunIdentifier(selectedPr))
			p.eventEmitter.EmitMessage(repo, zap.InfoLevel, "RepositoryTargetNamespaceNotFound", msg)
			return nil, nil
		}
		p.debugf("getPipelineRunsFromRepo: explicit /test using repo=%s/%s for pipelinerun=%s", selectedRepo.GetNamespace(), selectedRepo.GetName(), pipelineRunIdentifier(selectedPr))
		return []matcher.Match{{
			PipelineRun: selectedPr,
			Repo:        selectedRepo,
		}}, nil
	}

	matchedPRs, err = matcher.MatchPipelinerunByAnnotation(ctx, p.logger, pipelineRuns, p.run, p.event, p.vcx, p.eventEmitter, repo, false)
	if err != nil {
		// Don't fail when you don't have a match between pipeline and annotations
		p.eventEmitter.EmitMessage(nil, zap.WarnLevel, "RepositoryNoMatch", err.Error())
		return nil, nil
	}
	p.debugf("getPipelineRunsFromRepo: final match count=%d", len(matchedPRs))

	return matchedPRs, nil
}

func filterRunningPipelineRunOnTargetTest(testPipeline string, prs []*tektonv1.PipelineRun) *tektonv1.PipelineRun {
	for _, pr := range prs {
		if prName, ok := pr.GetAnnotations()[apipac.OriginalPRName]; ok {
			if prName == testPipeline {
				return pr
			}
		}
	}
	return nil
}

func pipelineRunIdentifier(pr *tektonv1.PipelineRun) string {
	if pr == nil {
		return "<nil>"
	}
	if pr.GetName() != "" {
		return pr.GetName()
	}
	if pr.GetGenerateName() != "" {
		return pr.GetGenerateName()
	}
	return "<unnamed>"
}

func (p *PacRun) resolveTargetNamespaceRepo(ctx context.Context, fallbackRepo *v1alpha1.Repository, pr *tektonv1.PipelineRun) *v1alpha1.Repository {
	if fallbackRepo == nil || pr == nil {
		return fallbackRepo
	}

	targetNS, ok := pr.GetAnnotations()[apipac.TargetNamespace]
	if !ok || targetNS == "" {
		return fallbackRepo
	}
	if targetNS == fallbackRepo.GetNamespace() {
		return fallbackRepo
	}

	targetRepo, err := matcher.MatchEventURLRepo(ctx, p.run, p.event, targetNS)
	if err != nil {
		p.logger.Warnf("resolveTargetNamespaceRepo: failed to lookup target namespace=%s for pipelinerun=%s: %v", targetNS, pipelineRunIdentifier(pr), err)
		return nil
	}
	if targetRepo == nil {
		p.logger.Warnf("resolveTargetNamespaceRepo: no repository found in target namespace=%s for pipelinerun=%s", targetNS, pipelineRunIdentifier(pr))
		return nil
	}

	p.debugf("resolveTargetNamespaceRepo: resolved pipelinerun=%s to repo=%s/%s via target-namespace=%s", pipelineRunIdentifier(pr), targetRepo.GetNamespace(), targetRepo.GetName(), targetNS)
	return targetRepo
}

// changePipelineRun go over each pipelineruns and modify things into it.
//
// - the secret template variable with a random one as generated from GetBasicAuthSecretName
// - the template variable with the one from the event (this includes the remote pipeline that has template variables).
func (p *PacRun) changePipelineRun(ctx context.Context, repo *v1alpha1.Repository, prs []*tektonv1.PipelineRun) error {
	p.debugf("changePipelineRun: processing %d pipelineruns", len(prs))
	for k, pr := range prs {
		prName := pr.GetName()
		if prName == "" {
			prName = pr.GetGenerateName()
		}
		p.debugf("changePipelineRun: processing pipelinerun=%s", prName)

		b, err := json.Marshal(pr)
		if err != nil {
			return fmt.Errorf("failed to marshal PipelineRun %s: %w", prName, err)
		}

		name := secrets.GenerateBasicAuthSecretName()
		processed := templates.ReplacePlaceHoldersVariables(string(b), map[string]string{
			"git_auth_secret": name,
		}, nil, nil, map[string]any{})
		processed = p.makeTemplate(ctx, repo, processed)

		var np *tektonv1.PipelineRun
		err = json.Unmarshal([]byte(processed), &np)
		if err != nil {
			return fmt.Errorf("failed to unmarshal PipelineRun %s: %w", prName, err)
		}
		// don't crash when we don't have any annotations
		if np.Annotations == nil {
			np.Annotations = map[string]string{}
		}
		np.Annotations[apipac.GitAuthSecret] = name

		prs[k] = np
		p.debugf("changePipelineRun: updated pipelinerun=%s with git_auth_secret annotation", prName)
	}
	return nil
}

// checkNeedUpdate checks if the template needs an update form the user, try to
// match some patterns for some issues in a template to let the user know they need to
// update.
//
// We otherwise fail with a descriptive error message to the user (check run
// interface on as comment for other providers) on how to update.
//
// Checks are deprecated/removed to n+2 release of OSP.
func (p *PacRun) checkNeedUpdate(_ string) (string, bool) {
	return "", false
}

func (p *PacRun) createNeutralStatus(ctx context.Context, title, text string) error {
	p.debugf("createNeutralStatus: title=%s", title)
	status := providerstatus.StatusOpts{
		Status:     CompletedStatus,
		Title:      title,
		Text:       text,
		Conclusion: providerstatus.ConclusionNeutral,
		DetailsURL: p.event.URL,
	}
	if err := p.vcx.CreateStatus(ctx, p.event, status); err != nil {
		return fmt.Errorf("failed to run create status, user is not allowed to run the CI:: %w", err)
	}

	return nil
}
