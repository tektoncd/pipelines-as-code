package pipelineascode

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/action"
	apincoming "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/incoming"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/customparams"
	pacerrors "github.com/openshift-pipelines/pipelines-as-code/pkg/errors"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	prgroup "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerungroup"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/resolve"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// planFollowUps validates the dependency graph of the event's definitions and
// prunes the matched list down to the plain runs to start: follow-ups are not
// created at event time.
func (p *PacRun) planFollowUps(ctx context.Context, repo *v1alpha1.Repository,
	matchedPRs []matcher.Match, unmatchedPRs map[string]*tektonv1.PipelineRun,
	allDefs []*tektonv1.PipelineRun,
) []matcher.Match {
	all := make([]*tektonv1.PipelineRun, 0, len(matchedPRs)+len(unmatchedPRs)+len(allDefs))
	seen := map[string]bool{}
	add := func(pr *tektonv1.PipelineRun) {
		name := prgroup.DefinitionName(pr)
		if seen[name] {
			return
		}
		seen[name] = true
		all = append(all, pr)
	}
	for i := range matchedPRs {
		add(matchedPRs[i].PipelineRun)
	}
	for _, pr := range unmatchedPRs {
		add(pr)
	}
	for _, pr := range allDefs {
		add(pr)
	}

	validations := []*pacerrors.PacYamlValidations{}
	addValidation := func(name string, err error) {
		validations = append(validations, &pacerrors.PacYamlValidations{
			Name: name, Err: err, Schema: tektonv1.SchemeGroupVersion.Group,
		})
	}

	defs, defErrs := prgroup.DefinitionsFromPRs(all)
	if !p.pacInfo.EnableFollowUpRuns {
		// the feature is disabled: the definitions with on-pipelinerun are
		// dropped, like definitions that do not match the event. They cannot
		// run as plain runs: their CEL condition is only evaluated once
		// their dependencies are done, running them now would start a
		// deploy before its build.
		if slices.ContainsFunc(defs, func(def prgroup.Definition) bool { return len(def.DependsOn) > 0 }) {
			p.eventEmitter.EmitMessage(repo, zap.InfoLevel, "RepositoryPipelineRunFollowUpsDisabled",
				"The Pipelinerun follow-ups are disabled on this cluster: set enable-follow-up-runs to \"true\" in the Pipelines-as-Code ConfigMap to use the on-pipelinerun annotation.")
		}
		kept := make([]matcher.Match, 0, len(matchedPRs))
		for i := range matchedPRs {
			if matchedPRs[i].Config[matcher.ConfigFollowUp] == "true" {
				continue
			}
			kept = append(kept, matchedPRs[i])
		}
		return kept
	}
	for _, defErr := range defErrs {
		addValidation(defErr.Name, defErr.Err)
	}
	p.debugf("planFollowUps: definitions=%d validations=%d", len(defs), len(validations))

	if !slices.ContainsFunc(defs, func(def prgroup.Definition) bool { return len(def.DependsOn) > 0 }) {
		if len(validations) > 0 {
			// The annotations of this event are invalid even though no group
			// formed: report them rather than silently ignoring them.
			p.reportValidationErrors(ctx, repo, validations)
			return nil
		}
		// No follow-up group formed for this event: run whatever matched as
		// plain runs, even if the matcher flagged a definition (an empty
		// annotation would otherwise wait forever as a pending run).
		for i := range matchedPRs {
			delete(matchedPRs[i].Config, matcher.ConfigFollowUp)
		}
		return matchedPRs
	}

	// minted once for the whole event: the dependency runs carry it and every
	// follow-up derives its deterministic run name from it
	p.groupID = prgroup.NewGroupID()
	p.followUpEvent = p.captureFollowUpEvent(ctx, repo)

	// compile-check every expression at event time: the watcher's environment
	// has no webhook payload
	customParamNames := []string{}
	if repo.Spec.Params != nil {
		for _, param := range *repo.Spec.Params {
			customParamNames = append(customParamNames, param.Name)
		}
	}
	for _, def := range defs {
		if len(def.DependsOn) == 0 || def.CEL == "" {
			continue
		}
		if err := matcher.ValidateFollowUp(def.CEL, customParamNames); err != nil {
			addValidation(def.Name, err)
		}
	}

	// started is the set of definitions that get a normal run; follow-ups
	// enter the plan through the fixpoint in PlanFollowUps only
	started := map[string]bool{}
	for i := range matchedPRs {
		if matchedPRs[i].Config[matcher.ConfigFollowUp] != "true" {
			started[prgroup.DefinitionName(matchedPRs[i].PipelineRun)] = true
		}
	}
	plan := prgroup.PlanFollowUps(defs, started)
	for _, problem := range plan.Problems {
		addValidation(problem.Name, fmt.Errorf("%s", problem.Msg))
	}
	for name, err := range crossRepositoryFollowUps(repo, matchedPRs, plan.Create, defs) {
		addValidation(name, err)
	}
	if names := incomingParamNames(p.event); len(names) > 0 {
		// the overrides live in the payload, gone once the follow-ups are
		// templated, and their values are never stored: the follow-ups would
		// see other params than their dependencies
		for _, name := range plan.Create {
			addValidation(name, fmt.Errorf("follow-up %q cannot run for an incoming webhook that overrides the params %s: the follow-up would not see them, set them in the Repository params instead",
				name, strings.Join(names, ", ")))
		}
	}
	if len(validations) > 0 {
		// a broken graph is not scheduled at all: some runs would leave the
		// chains half-started
		p.reportValidationErrors(ctx, repo, validations)
		return nil
	}
	p.debugf("planFollowUps: followUps=%d skip=%d", len(plan.Create), len(plan.Skip))

	skipReasons := map[string]string{}
	for _, problem := range plan.Skip {
		skipReasons[problem.Name] = problem.Msg
	}
	create := map[string]bool{}
	for _, name := range plan.Create {
		create[name] = true
	}

	defsByName := map[string]prgroup.Definition{}
	for _, def := range defs {
		defsByName[def.Name] = def
	}

	succeeded := p.alreadySucceededDefinitions(defs, matchedPRs, unmatchedPRs)

	kept := make([]matcher.Match, 0, len(matchedPRs))
	for i := range matchedPRs {
		match := matchedPRs[i]
		name := prgroup.DefinitionName(match.PipelineRun)
		if match.Config[matcher.ConfigFollowUp] == "true" {
			if !create[name] {
				if dependsOnAny(defsByName, name, succeeded) {
					// a /retest does not re-form a group around a past
					// run: the follow-up is not re-created and the checks
					// of its previous run stay on the commit
					p.debugf("planFollowUps: follow-up %s waits on an already succeeded definition, not re-created", name)
					continue
				}
				if !dependsOnAny(defsByName, name, started) {
					// none of its dependencies runs for this event: not
					// matched, like any definition that does not match
					p.debugf("planFollowUps: follow-up %s has no dependency running for this event, not matched", name)
					if unmatchedPRs != nil {
						unmatchedPRs[name] = match.PipelineRun
					}
					continue
				}
				p.reportSkippedFollowUpPR(ctx, repo, match.PipelineRun, skipReasons[name])
				continue
			}
			// the follow-up is not started: its intent becomes a follow-up
			// stamped on one of the dependency runs
			followUp, err := p.buildFollowUp(match)
			if err != nil {
				addValidation(name, err)
				continue
			}
			run := onRunDefinitionFor(defsByName, name)
			if run == "" {
				addValidation(name, fmt.Errorf("follow-up %q has no dependency to carry it", name))
				continue
			}
			p.followUps = append(p.followUps, followUp)
			p.followUpsOn[followUp.ID] = run
			continue
		}
		kept = append(kept, match)
	}

	if len(validations) > 0 {
		// a follow-up that could not be built must not leave its follow-up
		// waiting forever
		p.reportValidationErrors(ctx, repo, validations)
		return nil
	}

	if len(p.followUps) > 0 {
		p.debugf("planFollowUps: group_id=%s followUps=%d", p.groupID, len(p.followUps))
	}
	return kept
}

// incomingParamNames returns the sorted names of the params an incoming
// webhook overrides, read from the payload like the custom params do.
func incomingParamNames(event *info.Event) []string {
	if event.Request == nil {
		return nil
	}
	payload, err := apincoming.ParseIncomingPayload(event.Request.Payload)
	if err != nil {
		return nil
	}
	return slices.Sorted(maps.Keys(payload.Params))
}

// crossRepositoryFollowUps rejects the follow-ups that would not run with the
// Repository of their dependencies: the watcher creates a follow-up next to
// its dependency runs, with their Repository, and only looks for the runs of
// its group in their namespace. A target-namespace annotation must therefore
// resolve to the same Repository for a follow-up and its dependencies.
func crossRepositoryFollowUps(repo *v1alpha1.Repository, matchedPRs []matcher.Match,
	create []string, defs []prgroup.Definition,
) map[string]error {
	repoOf := map[string]string{}
	for i := range matchedPRs {
		matchRepo := matchedPRs[i].Repo
		if matchRepo == nil {
			matchRepo = repo
		}
		repoOf[prgroup.DefinitionName(matchedPRs[i].PipelineRun)] = matchRepo.GetNamespace() + "/" + matchRepo.GetName()
	}
	depsOf := map[string][]string{}
	for _, def := range defs {
		depsOf[def.Name] = def.DependsOn
	}
	errs := map[string]error{}
	for _, name := range create {
		own, ok := repoOf[name]
		if !ok {
			continue
		}
		for _, dep := range depsOf[name] {
			depRepo, ok := repoOf[dep]
			if !ok || depRepo == own {
				continue
			}
			errs[name] = fmt.Errorf("follow-up %q runs with Repository %s but its dependency %q runs with Repository %s: a follow-up and its dependencies must run with the same Repository, check their %s annotations",
				name, own, dep, depRepo, keys.TargetNamespace)
			break
		}
	}
	return errs
}

// alreadySucceededDefinitions returns the plain definitions a /retest or an
// /ok-to-test left out because their previous run succeeded: the matcher drops
// them from both the matched and the unmatched lists.
func (p *PacRun) alreadySucceededDefinitions(defs []prgroup.Definition,
	matchedPRs []matcher.Match, unmatchedPRs map[string]*tektonv1.PipelineRun,
) map[string]bool {
	if p.event.EventType != opscomments.RetestAllCommentEventType.String() &&
		p.event.EventType != opscomments.OkToTestCommentEventType.String() {
		return nil
	}
	seen := map[string]bool{}
	for i := range matchedPRs {
		seen[prgroup.DefinitionName(matchedPRs[i].PipelineRun)] = true
	}
	for _, pr := range unmatchedPRs {
		seen[prgroup.DefinitionName(pr)] = true
	}
	succeeded := map[string]bool{}
	for _, def := range defs {
		if len(def.DependsOn) == 0 && !seen[def.Name] {
			succeeded[def.Name] = true
		}
	}
	return succeeded
}

// dependsOnAny reports whether a definition depends, directly or through a
// chain of follow-ups, on one of the given definitions.
func dependsOnAny(defsByName map[string]prgroup.Definition, name string, targets map[string]bool) bool {
	if len(targets) == 0 {
		return false
	}
	visited := map[string]bool{}
	var visit func(string) bool
	visit = func(current string) bool {
		if visited[current] {
			return false
		}
		visited[current] = true
		for _, dep := range defsByName[current].DependsOn {
			if targets[dep] || visit(dep) {
				return true
			}
		}
		return false
	}
	return visit(name)
}

// captureFollowUpEvent captures the event the .tekton directory was templated
// with, the Repository it was templated from, and which of that Repository's
// custom params its filters selected, for the watcher to template and evaluate
// the follow-ups the same way once the webhook payload is gone. Only the
// filters are recorded, never the values.
func (p *PacRun) captureFollowUpEvent(ctx context.Context, repo *v1alpha1.Repository) *prgroup.FollowUpEvent {
	event := p.event
	if p.templatedEvent != nil {
		event = p.templatedEvent
	}
	captured := prgroup.NewFollowUpEvent(event)
	cp := customparams.NewCustomParams(event, repo, p.run, p.k8int, p.eventEmitter, p.vcx)
	filters, err := cp.MatchedFilters(ctx)
	if err != nil {
		// templating hit the same error and could not resolve the params
		// either: the follow-ups select none
		p.debugf("captureFollowUpEvent: cannot evaluate the param filters: %v", err)
		filters = map[string]string{}
	}
	captured.ParamFilters = filters
	captured.SourceRepoNamespace = repo.GetNamespace()
	captured.SourceRepoName = repo.GetName()
	return captured
}

// onRunDefinitionFor returns the definition name of the run a follow-up is
// stamped on: the lexicographically smallest root of the follow-up's
// dependency closure. Chained follow-ups live on a root until their direct
// dependency exists; the watcher migrates them as soon as it does.
func onRunDefinitionFor(defsByName map[string]prgroup.Definition, name string) string {
	roots := map[string]bool{}
	var visit func(string)
	visit = func(current string) {
		def, ok := defsByName[current]
		if !ok {
			return
		}
		for _, dep := range def.DependsOn {
			depDef, ok := defsByName[dep]
			if !ok {
				continue
			}
			if len(depDef.DependsOn) > 0 {
				visit(dep)
				continue
			}
			roots[dep] = true
		}
	}
	visit(name)
	if len(roots) == 0 {
		return ""
	}
	return slices.Min(slices.Collect(maps.Keys(roots)))
}

// buildFollowUp composes the stored intent of a planned follow-up: its
// deterministic run name, its dependencies and the pending state. The
// definition itself is not stored; the watcher re-resolves it at the event's
// pinned revision when it creates the run.
func (p *PacRun) buildFollowUp(match matcher.Match) (prgroup.FollowUp, error) {
	definition := prgroup.DefinitionName(match.PipelineRun)

	name, err := prgroup.FollowUpName(definition, p.groupID)
	if err != nil {
		return prgroup.FollowUp{}, err
	}
	deps, err := prgroup.DepsOf(match.PipelineRun)
	if err != nil {
		return prgroup.FollowUp{}, err
	}

	return prgroup.FollowUp{
		ID:    name,
		Name:  definition,
		Deps:  deps,
		State: prgroup.StatePending,
		Event: p.followUpEvent,
	}, nil
}

// documentName extracts the metadata name of a raw .tekton document, for
// reporting validation errors against their definition.
var documentNameRe = regexp.MustCompile(`(?m)^\s*name:\s*"?([^\s"]+)"?`)

func documentName(doc string) string {
	if m := documentNameRe.FindStringSubmatch(doc); len(m) == 2 {
		return m[1]
	}
	return "unknown"
}

// forbiddenFollowUpVariable reports whether a template variable cannot be
// expanded at creation time: body, headers and files come from the webhook
// payload, which is gone by then.
func forbiddenFollowUpVariable(key string) bool {
	if strings.HasPrefix(key, "body") || strings.HasPrefix(key, "headers") || strings.HasPrefix(key, "files") {
		return true
	}
	if expr, ok := strings.CutPrefix(key, "cel:"); ok {
		for _, gone := range []string{"body.", "headers.", "files."} {
			if strings.Contains(expr, gone) {
				return true
			}
		}
	}
	return false
}

// validateFollowUpTemplates reports the follow-up definitions referencing
// template variables the watcher cannot expand at creation. It runs on the
// raw documents, before the event-time templating expands them, so payload
// variables fail here instead of leaving an unreplaced placeholder in the
// created run.
func validateFollowUpTemplates(rawTemplates string) []*pacerrors.PacYamlValidations {
	var validations []*pacerrors.PacYamlValidations
	for _, doc := range resolve.SplitDocuments(rawTemplates) {
		if !strings.Contains(doc, keys.OnPipelineRun) {
			continue
		}
		for _, match := range keys.ParamsRe.FindAllStringSubmatch(doc, -1) {
			key := strings.TrimSpace(match[1])
			if !forbiddenFollowUpVariable(key) {
				continue
			}
			validations = append(validations, &pacerrors.PacYamlValidations{
				Name:   documentName(doc),
				Err:    fmt.Errorf("the follow-up references the %q template variable, which comes from the webhook payload and is gone by the time the follow-up is resolved at creation", key),
				Schema: tektonv1.SchemeGroupVersion.Group,
			})
			break
		}
	}
	return validations
}

// stampFollowUps stamps the planned follow-ups onto the run runs
// before those are created, so the follow-ups reach the cluster as annotations
// with no extra patch.
func (p *PacRun) stampFollowUps(normal []matcher.Match) error {
	byName := make(map[string]*matcher.Match, len(normal))
	for i := range normal {
		byName[prgroup.DefinitionName(normal[i].PipelineRun)] = &normal[i]
	}
	for _, followUp := range p.followUps {
		run, ok := byName[p.followUpsOn[followUp.ID]]
		if !ok {
			// the run did not match for this event: the orphan reporting
			// takes over and skips the follow-up
			continue
		}
		if err := prgroup.AddFollowUps(run.PipelineRun, followUp); err != nil {
			return fmt.Errorf("cannot stamp the follow-up %s: %w", followUp.ID, err)
		}
	}
	return nil
}

// creationPhases splits the matched runs, by index, into the phases they are
// created in, each phase waiting for the previous one: the plain runs first,
// all at once, then the runs carrying follow-ups one at a time, by descending
// definition name. The watcher finds a follow-up only through the run it is
// stored on, the smallest root of its dependencies: every other root it
// depends on sorts after it, and is created or failed to start by the time
// the follow-up can be evaluated. A missing dependency is never one still
// being created.
func creationPhases(matches []matcher.Match) [][]int {
	plain, carriers := []int{}, []int{}
	for i := range matches {
		if _, ok := matches[i].PipelineRun.GetAnnotations()[keys.FollowUps]; ok {
			carriers = append(carriers, i)
		} else {
			plain = append(plain, i)
		}
	}
	slices.SortStableFunc(carriers, func(a, b int) int {
		return strings.Compare(prgroup.DefinitionName(matches[b].PipelineRun), prgroup.DefinitionName(matches[a].PipelineRun))
	})
	phases := make([][]int, 0, 1+len(carriers))
	phases = append(phases, plain)
	for _, i := range carriers {
		phases = append(phases, []int{i})
	}
	return phases
}

// reportFollowUps closes the event time for the follow-ups: a satisfiable
// follow-up waits silently, while each follow-up whose dependency or run did not
// run is skipped right away with a check. A follow-up whose run the watcher
// already created is left to it.
func (p *PacRun) reportFollowUps(ctx context.Context, repo *v1alpha1.Repository,
	createdRuns map[string]*tektonv1.PipelineRun,
) {
	entryByName := map[string]prgroup.FollowUp{}
	for _, followUp := range p.followUps {
		entryByName[followUp.Name] = followUp
	}
	for _, followUp := range p.followUps {
		onRun := createdRuns[p.followUpsOn[followUp.ID]]

		missing := []string{}
		for _, dep := range followUp.Deps {
			// a chained dependency is a follow-up of its own, not a run (yet)
			if _, chained := entryByName[dep]; createdRuns[dep] == nil && !chained {
				missing = append(missing, dep)
			}
		}
		sort.Strings(missing)

		switch {
		case len(missing) > 0:
			created, err := p.followUpRunCreated(ctx, onRun, repo, followUp.ID)
			if err != nil {
				// cannot determine if the watcher created the run: leave the
				// follow-up pending, reconcile of a done member of its
				// group resolves it.
				p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryPipelineRun",
					fmt.Sprintf("cannot determine whether the follow-up pipelinerun %s exists: %s", followUp.ID, err.Error()))
				continue
			}
			if created {
				// the watcher created the follow-up, mark it as created, the
				// run reports for itself.
				p.consumeFollowUpOnRun(ctx, onRun, followUp.ID, prgroup.StateCreated)
				continue
			}
			p.reportSkippedFollowUp(ctx, repo, followUp.ID, followUp.Name,
				fmt.Sprintf("dependency %s did not run for this event", strings.Join(missing, ", ")))
			p.consumeFollowUpOnRun(ctx, onRun, followUp.ID, prgroup.StateSkipped)
		case onRun == nil:
			// the follow-up was never stamped anywhere: nothing to consume
			p.reportSkippedFollowUp(ctx, repo, followUp.ID, followUp.Name,
				fmt.Sprintf("the run of its dependency %s did not run for this event", p.followUpsOn[followUp.ID]))
		}
	}
}

// followUpRunCreated reports whether the watcher already created the run of a
// follow-up, by its deterministic name. The namespace is the one of the run the
// follow-up is stored on, falling back to the repository's: a follow-up and its
// dependencies are validated at planning time to run with the same repository.
func (p *PacRun) followUpRunCreated(ctx context.Context, onRun *tektonv1.PipelineRun,
	repo *v1alpha1.Repository, id string,
) (bool, error) {
	namespace := repo.GetNamespace()
	if onRun != nil {
		namespace = onRun.GetNamespace()
	}
	_, err := p.run.Clients.Tekton.TektonV1().PipelineRuns(namespace).Get(ctx, id, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// reportSkippedFollowUp posts the skipped check of a follow-up that will not
// run for this event.
func (p *PacRun) reportSkippedFollowUp(ctx context.Context, repo *v1alpha1.Repository,
	runName, definitionName, reason string,
) {
	p.eventEmitter.EmitMessage(repo, zap.InfoLevel, "RepositoryPipelineRunSkipped",
		fmt.Sprintf("PipelineRun %s is skipped: %s", definitionName, reason))
	p.reportFollowUpConclusion(ctx, repo, p.event, runName, definitionName,
		providerstatus.ConclusionSkipped, fmt.Sprintf("PipelineRun is skipped: %s", reason))
}

// reportFollowUpConclusion posts the completed check of a follow-up that never
// got a run.
func (p *PacRun) reportFollowUpConclusion(ctx context.Context, repo *v1alpha1.Repository, event *info.Event,
	runName, definitionName string, conclusion providerstatus.Conclusion, text string,
) {
	if err := p.vcx.CreateStatus(ctx, event, providerstatus.StatusOpts{
		PipelineRunName:         runName,
		OriginalPipelineRunName: definitionName,
		Status:                  CompletedStatus,
		Conclusion:              conclusion,
		Text:                    text,
		DetailsURL:              p.consoleUI().URL(),
	}); err != nil {
		p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryCreateStatus",
			fmt.Sprintf("cannot create %s status for follow-up %s: %s", conclusion, definitionName, err.Error()))
	}
}

// consumeFollowUpOnRun moves a follow-up stored on a created run to a terminal
// state, so it is not re-evaluated on every reconcile of its run.
func (p *PacRun) consumeFollowUpOnRun(ctx context.Context, run *tektonv1.PipelineRun, id, state string) {
	if run == nil {
		return
	}
	fresh, err := p.run.Clients.Tekton.TektonV1().PipelineRuns(run.GetNamespace()).Get(ctx, run.GetName(), metav1.GetOptions{})
	if err != nil {
		p.eventEmitter.EmitMessage(nil, zap.ErrorLevel, "RepositoryPipelineRun",
			fmt.Sprintf("cannot get pipelinerun %s to consume a follow-up: %s", run.GetName(), err.Error()))
		return
	}
	if err := prgroup.ConsumeFollowUp(fresh, id, state); err != nil {
		p.eventEmitter.EmitMessage(nil, zap.ErrorLevel, "RepositoryPipelineRun", err.Error())
		return
	}
	if _, err := action.PatchPipelineRun(ctx, p.logger, "follow-up state", p.run.Clients.Tekton, fresh, prgroup.FollowUpsPatch(fresh)); err != nil {
		p.eventEmitter.EmitMessage(nil, zap.ErrorLevel, "RepositoryPipelineRun",
			fmt.Sprintf("cannot consume the follow-up %s on pipelinerun %s: %s", id, fresh.GetName(), err.Error()))
	}
}

// reportSkippedFollowUpPR adapts a not-yet-created follow-up template to
// reportSkippedFollowUp.
func (p *PacRun) reportSkippedFollowUpPR(ctx context.Context, repo *v1alpha1.Repository,
	pr *tektonv1.PipelineRun, reason string,
) {
	runName := pr.GetName()
	if runName == "" {
		runName = pr.GetGenerateName()
	}
	p.reportSkippedFollowUp(ctx, repo, runName, prgroup.DefinitionName(pr), reason)
}

// eventForRun returns the event to report a check of the given run on: the
// current event, pointed at the commit the run was created for.
func eventForRun(event *info.Event, run *tektonv1.PipelineRun) *info.Event {
	sha := run.GetAnnotations()[keys.SHA]
	if sha == "" || sha == event.SHA {
		return event
	}
	runEvent := *event
	runEvent.SHA = sha
	return &runEvent
}

// cancelFollowUps consumes the pending follow-ups stored on the listed
// runs, so a cancel, a closed pull request or cancel-in-progress cannot leave
// a follow-up to be created afterwards. A cancelled check is posted, the only
// trace of a follow-up that waited without one.
func (p *PacRun) cancelFollowUps(ctx context.Context, repo *v1alpha1.Repository,
	prs []tektonv1.PipelineRun, reason string,
) {
	for i := range prs {
		run := &prs[i]
		// A run of the event doing the cancelling carries this event's group:
		// its pending follow-ups are this event's own and must keep waiting,
		// only the superseded event's follow-ups are consumed. The cancel
		// comment and the closed pull request paths run before any group is
		// planned, so the guard never applies to them.
		if p.groupID != "" && run.GetLabels()[keys.GroupID] == p.groupID {
			continue
		}
		followUps, err := prgroup.FollowUpsOf(run)
		if err != nil {
			p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "RepositoryPipelineRun",
				fmt.Sprintf("cannot read the follow-ups of pipelinerun %s: %s", run.GetName(), err.Error()))
			continue
		}
		for _, followUp := range followUps {
			if !followUp.Pending() {
				continue
			}
			if p.event.TargetCancelPipelineRun != "" && followUp.Name != p.event.TargetCancelPipelineRun {
				continue
			}
			p.eventEmitter.EmitMessage(repo, zap.InfoLevel, "RepositoryPipelineRunSkipped",
				fmt.Sprintf("PipelineRun %s is cancelled while waiting: %s", followUp.Name, reason))
			// the check goes on the commit of the superseded event, not on
			// the one of the event doing the cancelling
			p.reportFollowUpConclusion(ctx, repo, eventForRun(p.event, run), followUp.ID, followUp.Name,
				providerstatus.ConclusionCancelled, fmt.Sprintf("PipelineRun is cancelled: %s", reason))
			p.consumeFollowUpOnRun(ctx, run, followUp.ID, prgroup.StateCancelled)
			// the watcher may have been creating the run meanwhile: look it
			// up by its deterministic name once the follow-up is consumed
			p.cancelFollowUpRun(ctx, repo, run.GetNamespace(), followUp.ID, reason)
		}
	}
}

// cancelFollowUpRun cancels the run of a follow-up, found by its
// deterministic name, when it exists and has not finished.
func (p *PacRun) cancelFollowUpRun(ctx context.Context, repo *v1alpha1.Repository, namespace, name, reason string) {
	run, err := p.run.Clients.Tekton.TektonV1().PipelineRuns(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return
	}
	if run.IsDone() || run.IsCancelled() || run.IsGracefullyCancelled() || run.IsGracefullyStopped() {
		return
	}
	p.logger.Infof("cancel-in-progress: cancelling follow-up pipelinerun %v/%v: %s", namespace, name, reason)
	if _, err := action.PatchPipelineRun(ctx, p.logger, "cancel patch", p.run.Clients.Tekton, run, cancelMergePatch); err != nil {
		p.eventEmitter.EmitMessage(repo, zap.ErrorLevel, "CancelInProgress",
			fmt.Sprintf("failed to cancel pipelineRun %s/%s: %s", namespace, name, err.Error()))
	}
}
