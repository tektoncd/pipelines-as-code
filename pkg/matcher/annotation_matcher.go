package matcher

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/customparams"
	pacerrors "github.com/openshift-pipelines/pipelines-as-code/pkg/errors"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"

	"github.com/gobwas/glob"
	"github.com/google/cel-go/common/types"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

const (
	// regex allows array of string or a single string
	// eg. ["foo", "bar"], ["foo"] or "foo".
	reValidateTag = `^\[(.*)\]$|^[^[\]\s]*$`
	// maximum number of characters to display in logs for gitops comments.
	maxCommentLogLength = 160
)

// ErrNoFailedPipelineToRetest is returned when /retest or /ok-to-test is used
// but all matching pipelines have already succeeded for this commit.
var ErrNoFailedPipelineToRetest = errors.New("All PipelineRuns for this commit have already succeeded. Use `/retest <pipeline-name>` to re-run a specific pipeline or `/test` to re-run all pipelines.") // nolint:revive,staticcheck

// prunBranch is value from annotations and baseBranch is event.Base value from event.
func branchMatch(prunBranch, baseBranch string) bool {
	// Helper function to match glob pattern
	matchGlob := func(pattern, branch string) bool {
		g := glob.MustCompile(pattern)
		return g.Match(branch)
	}

	// Case: target is refs/heads/..
	if strings.HasPrefix(prunBranch, "refs/heads/") {
		ref := baseBranch
		if !strings.HasPrefix(baseBranch, "refs/heads/") && !strings.HasPrefix(baseBranch, "refs/tags/") {
			// If base is without refs/heads/.. and not refs/tags/.. prefix, add it
			ref = "refs/heads/" + baseBranch
		}
		return matchGlob(prunBranch, ref)
	}

	// Case: target is not refs/heads/.. and not refs/tags/..
	if !strings.HasPrefix(prunBranch, "refs/heads/") && !strings.HasPrefix(prunBranch, "refs/tags/") {
		prunRef := "refs/heads/" + prunBranch
		ref := baseBranch
		if !strings.HasPrefix(baseBranch, "refs/heads/") && !strings.HasPrefix(baseBranch, "refs/tags/") {
			// If base is without refs/heads/.. and not refs/tags/.. prefix, add it
			ref = "refs/heads/" + baseBranch
		}
		return matchGlob(prunRef, ref)
	}

	// Match the prunRef pattern with the baseBranch
	// this will cover the scenarios of match globs like refs/tags/0.* and any other if any
	return matchGlob(prunBranch, baseBranch)
}

// TODO: move to another file since it's common to all annotations_* files.
func getAnnotationValues(annotation string) ([]string, error) {
	re := regexp.MustCompile(reValidateTag)
	annotation = strings.TrimSpace(annotation)
	match := re.MatchString(annotation)
	if !match {
		return nil, fmt.Errorf("annotations in pipeline are in wrong format: %s", annotation)
	}

	// if it's not an array then it would be a single string
	if !strings.HasPrefix(annotation, "[") {
		// replace &#44; with comma so users can have comma in the annotation
		annot := strings.ReplaceAll(annotation, "&#44;", ",")
		return []string{annot}, nil
	}

	// Split all tasks by comma and make sure to trim spaces in there
	split := strings.Split(re.FindStringSubmatch(annotation)[1], ",")
	for i := range split {
		split[i] = strings.TrimSpace(strings.ReplaceAll(split[i], "&#44;", ","))
	}

	if split[0] == "" {
		return nil, fmt.Errorf("annotation \"%s\" has empty values", annotation)
	}

	return split, nil
}

func getTargetBranch(prun *tektonv1.PipelineRun, event *info.Event) (bool, string, string, error) {
	var targetEvent, targetBranch string
	if key, ok := prun.GetObjectMeta().GetAnnotations()[keys.OnEvent]; ok {
		if key == "[]" {
			return false, "", "", fmt.Errorf("annotation %s is empty", keys.OnEvent)
		}
		targetEvents := []string{event.TriggerTarget.String()}
		if event.EventType == triggertype.Incoming.String() {
			// if we have a incoming event, we want to match pipelineruns on both incoming and push
			targetEvents = []string{triggertype.Incoming.String(), triggertype.Push.String()}
		}
		matched, err := matchOnAnnotation(key, targetEvents, false)
		targetEvent = key
		if err != nil {
			return false, "", "", err
		}
		if !matched {
			return false, "", "", nil
		}
	}
	if key, ok := prun.GetObjectMeta().GetAnnotations()[keys.OnTargetBranch]; ok {
		if key == "[]" {
			return false, "", "", fmt.Errorf("annotation %s is empty", keys.OnTargetBranch)
		}
		targetEvents := []string{event.BaseBranch}
		matched, err := matchOnAnnotation(key, targetEvents, true)
		targetBranch = key
		if err != nil {
			return false, "", "", err
		}
		if !matched {
			return false, "", "", nil
		}
	}

	if targetEvent == "" || targetBranch == "" {
		return false, "", "", nil
	}
	return true, targetEvent, targetBranch, nil
}

type Match struct {
	PipelineRun *tektonv1.PipelineRun
	Repo        *apipac.Repository
	Config      map[string]string
}

// getName returns the name of the PipelineRun, if GenerateName is not set, it
// returns the name generateName takes precedence over name since it will be
// generated when applying the PipelineRun by the tekton controller.
func getName(prun *tektonv1.PipelineRun) string {
	name := prun.GetGenerateName()
	if name == "" {
		name = prun.GetName()
	}
	return name
}

// checkPipelineRunAnnotation checks if the Pipelinerun has
// `on-event`/`on-target-branch annotations` with `on-cel-expression`
// and if present then warns the user that `on-cel-expression` will take precedence.
func checkPipelineRunAnnotation(prun *tektonv1.PipelineRun, eventEmitter *events.EventEmitter, repo *apipac.Repository) {
	// Define the annotations to check in a slice for easy iteration
	checks := []struct {
		key   string
		value string
	}{
		{"on-event", prun.GetObjectMeta().GetAnnotations()[keys.OnEvent]},
		{"on-target-branch", prun.GetObjectMeta().GetAnnotations()[keys.OnTargetBranch]},
	}

	// Preallocate the annotations slice with the exact capacity needed
	annotations := make([]string, 0, len(checks))

	// Iterate through each check and append the key if the value is non-empty
	for _, check := range checks {
		if check.value != "" {
			annotations = append(annotations, check.key)
		}
	}

	prName := getName(prun)
	if len(annotations) > 0 {
		ignoredAnnotations := strings.Join(annotations, ", ")
		msg := fmt.Sprintf(
			"Warning: The PipelineRun '%s' has 'on-cel-expression' defined along with [%s] annotation(s). The 'on-cel-expression' will take precedence and these annotations will be ignored",
			prName,
			ignoredAnnotations,
		)
		eventEmitter.EmitMessage(repo, zap.WarnLevel, "RepositoryTakesOnCelExpressionPrecedence", msg)
	}
}

func MatchPipelinerunByAnnotation(ctx context.Context, logger *zap.SugaredLogger, pruns []*tektonv1.PipelineRun, cs *params.Run, event *info.Event, vcx provider.Interface, eventEmitter *events.EventEmitter, repo *apipac.Repository, reportErrors bool) ([]Match, error) {
	matchedPRs := []Match{}
	logger.Debugf("MatchPipelinerunByAnnotation: pipelineruns=%d event_type=%s trigger_target=%s report_errors=%t", len(pruns), event.EventType, event.TriggerTarget, reportErrors)
	infomsg := fmt.Sprintf("matching pipelineruns to event: URL=%s, target-branch=%s, source-branch=%s, target-event=%s",
		event.URL,
		event.BaseBranch,
		event.HeadBranch,
		event.TriggerTarget,
	)

	if len(event.PullRequestLabel) > 0 {
		infomsg += fmt.Sprintf(", labels=%s", strings.Join(event.PullRequestLabel, "|"))
	}

	if event.EventType == triggertype.Incoming.String() {
		infomsg = fmt.Sprintf("%s, target-pipelinerun=%s", infomsg, event.TargetPipelineRun)
	} else if event.EventType == triggertype.PullRequest.String() {
		infomsg = fmt.Sprintf("%s, pull-request=%d", infomsg, event.PullRequestNumber)
	}
	logger.Info(infomsg)

	// Resolve custom params once for all PipelineRuns (for use in CEL expressions)
	// Track original event type to detect changes (when on-comment matches)
	originalEventType := event.EventType
	customParams := resolveCustomParamsForCEL(ctx, repo, event, cs, vcx, eventEmitter, logger)
	if len(customParams) > 0 {
		logger.Debugf("resolved %d custom params from repo for CEL", len(customParams))
	}

	celValidationErrors := []*pacerrors.PacYamlValidations{}
	for _, prun := range pruns {
		logger.Debugf("MatchPipelinerunByAnnotation: evaluating pipelinerun=%s annotations=%d", getName(prun), len(prun.GetObjectMeta().GetAnnotations()))
		prMatch := Match{
			PipelineRun: prun,
			Config:      map[string]string{},
		}

		prName := getName(prun)
		if event.TargetPipelineRun != "" && event.TargetPipelineRun == strings.TrimSuffix(prName, "-") {
			logger.Infof("matched target pipelinerun with name: %s, target pipelinerun: %s", prName, event.TargetPipelineRun)
			matchedPRs = append(matchedPRs, prMatch)
			continue
		}

		if prun.GetObjectMeta().GetAnnotations() == nil {
			logger.Debugf("PipelineRun %s does not have any annotations", prName)
			continue
		}

		if maxPrNumber, ok := prun.GetObjectMeta().GetAnnotations()[keys.MaxKeepRuns]; ok {
			prMatch.Config["max-keep-runs"] = maxPrNumber
			logger.Debugf("PipelineRun %s: max-keep-runs=%s", prName, maxPrNumber)
		}

		if targetNS, ok := prun.GetObjectMeta().GetAnnotations()[keys.TargetNamespace]; ok {
			prMatch.Config["target-namespace"] = targetNS
			prMatch.Repo, _ = MatchEventURLRepo(ctx, cs, event, targetNS)
			if prMatch.Repo == nil {
				logger.Warnf("could not find Repository CRD in branch %s, the pipelineRun %s has a label that explicitly targets it", targetNS, prName)
				continue
			}
			logger.Debugf("PipelineRun %s: matched target namespace repo=%s/%s", prName, prMatch.Repo.GetNamespace(), prMatch.Repo.GetName())
		}

		if targetComment, ok := prun.GetObjectMeta().GetAnnotations()[keys.OnComment]; ok {
			re, err := regexp.Compile(targetComment)
			if err != nil {
				logger.Warnf("could not compile regexp %s from pipelineRun %s", targetComment, prName)
				continue
			}

			strippedComment := strings.TrimSpace(
				strings.TrimPrefix(strings.TrimSuffix(event.TriggerComment, "\r\n"), "\r\n"))
			if re.MatchString(strippedComment) {
				event.EventType = opscomments.OnCommentEventType.String()

				// Re-resolve custom params if event type changed, so filters based on event_type stay accurate
				if event.EventType != originalEventType {
					customParams = resolveCustomParamsForCEL(ctx, repo, event, cs, vcx, eventEmitter, logger)
					originalEventType = event.EventType
					logger.Debugf("PipelineRun %s: event_type switched to %s due to comment match", prName, event.EventType)
				}

				comment := event.TriggerComment
				if len(comment) > maxCommentLogLength {
					comment = comment[:maxCommentLogLength] + "..."
				}
				logger.Infof("matched pipelinerun with name: %s on gitops comment: %q", prName, comment)

				matchedPRs = append(matchedPRs, prMatch)
				continue
			}
		}
		// if the event is a comment event, but we don't have any match from the keys.OnComment then skip the other evaluations
		if event.EventType == opscomments.NoOpsCommentEventType.String() || event.EventType == opscomments.OnCommentEventType.String() {
			continue
		}

		if celExpr, ok := prun.GetObjectMeta().GetAnnotations()[keys.OnCelExpression]; ok {
			checkPipelineRunAnnotation(prun, eventEmitter, repo)

			logger.Debugf("PipelineRun %s: evaluating CEL expression", prName)
			out, err := celEvaluate(ctx, celExpr, event, vcx, customParams, eventEmitter, repo)
			if err != nil {
				logger.Errorf("there was an error evaluating the CEL expression, skipping: %v", err)
				if checkIfCELEvaluateError(err) {
					if provider.IsCommentStrategyUpdate(repo) {
						// Using the same logic for getting OriginalPipelineRun as the one used for setting
						// the annotation which is used in the provider's "update" comment strategy.
						originPipelineRunName := prun.GetName()
						if originPipelineRunName == "" && prun.GenerateName != "" {
							originPipelineRunName = prun.GetGenerateName()
						}

						if reportErrors {
							reportCELValidationErrors(ctx, repo, []*pacerrors.PacYamlValidations{{
								Name: originPipelineRunName,
								Err:  fmt.Errorf("CEL expression evaluation error: %s", sanitizeErrorAsMarkdown(err)),
							}}, eventEmitter, vcx, event, fmt.Sprintf(provider.PlrStatusCommentPrefixTemplate, originPipelineRunName))
						}
					} else {
						celValidationErrors = append(celValidationErrors, &pacerrors.PacYamlValidations{
							Name: prName,
							Err:  fmt.Errorf("CEL expression evaluation error: %s", sanitizeErrorAsMarkdown(err)),
						})
					}
				}
				continue
			}
			logger.Debugf("PipelineRun %s: CEL result=%v", prName, out)
			if out != types.True {
				logger.Infof("CEL expression for PipelineRun %s is not matching, skipping", prName)
				continue
			}
			logger.Infof("CEL expression has been evaluated and matched")
		} else {
			// If the event is a pull_request and the event type is label_update, but the PipelineRun
			// does not contain an 'on-label' annotation, do not match this PipelineRun, as it is not intended for this event.
			// Check this early to avoid any unnecessary processing.
			_, hasOnLabel := prun.GetObjectMeta().GetAnnotations()[keys.OnLabel]
			if event.TriggerTarget == triggertype.PullRequest && event.EventType == string(triggertype.PullRequestLabeled) && !hasOnLabel {
				logger.Infof("label update event, PipelineRun %s does not have a on-label for any of those labels: %s", prName, strings.Join(event.PullRequestLabel, "|"))
				continue
			}

			matched, targetEvent, targetBranch, err := getTargetBranch(prun, event)
			if err != nil {
				return matchedPRs, err
			}
			if !matched {
				logger.Debugf("PipelineRun %s: target branch/event did not match", prName)
				continue
			}
			prMatch.Config["target-branch"] = targetBranch
			prMatch.Config["target-event"] = targetEvent
			logger.Debugf("PipelineRun %s: matched target event=%s target branch=%s", prName, targetEvent, targetBranch)

			if key, ok := prun.GetObjectMeta().GetAnnotations()[keys.OnPathChange]; ok {
				changedFiles, err := vcx.GetFiles(ctx, event)
				if err != nil {
					logger.Errorf("error getting changed files: %v", err)
					continue
				}
				// // TODO(chmou): we use the matchOnAnnotation function, it's
				// really made to match git branches but we can still use it for
				// our own path changes. we may split up if needed to refine.
				matched, err := matchOnAnnotation(key, changedFiles.All, true)
				if err != nil {
					return matchedPRs, err
				}
				if !matched {
					logger.Debugf("PipelineRun %s: path-change annotation did not match", prName)
					continue
				}
				logger.Infof("matched PipelineRun with name: %s, annotation PathChange: %q", prName, key)
				prMatch.Config["path-change"] = key
			}

			if key, ok := prun.GetObjectMeta().GetAnnotations()[keys.OnLabel]; ok {
				matched, err := matchOnAnnotation(key, event.PullRequestLabel, false)
				if err != nil {
					return matchedPRs, err
				}
				if !matched {
					logger.Debugf("PipelineRun %s: label annotation did not match", prName)
					continue
				}
				logger.Infof("matched PipelineRun with name: %s, annotation Label: %q", prName, key)
				prMatch.Config["label"] = key
			}

			if key, ok := prun.GetObjectMeta().GetAnnotations()[keys.OnPathChangeIgnore]; ok {
				changedFiles, err := vcx.GetFiles(ctx, event)
				if err != nil {
					logger.Errorf("error getting changed files: %v", err)
					continue
				}
				// // TODO(chmou): we use the matchOnAnnotation function, it's
				// really made to match git branches but we can still use it for
				// our own path changes. we may split up if needed to refine.
				matched, err := matchOnAnnotation(key, changedFiles.All, true)
				if err != nil {
					return matchedPRs, err
				}
				if matched {
					logger.Infof("Skipping pipelinerun with name: %s, annotation PathChangeIgnore: %q", prName, key)
					continue
				}
				prMatch.Config["path-change-ignore"] = key
				logger.Debugf("PipelineRun %s: path-change-ignore did not match, continuing", prName)
			}
		}

		logger.Infof("matched pipelinerun with name: %s, annotation Config: %q", prName, prMatch.Config)
		matchedPRs = append(matchedPRs, prMatch)
	}

	if len(celValidationErrors) > 0 && reportErrors {
		logger.Debugf("MatchPipelinerunByAnnotation: reporting %d CEL validation errors", len(celValidationErrors))
		reportCELValidationErrors(ctx, repo, celValidationErrors, eventEmitter, vcx, event, "")
	}

	if len(matchedPRs) > 0 {
		// Filter out templates that already have successful PipelineRuns for /retest and /ok-to-test
		if event.EventType == opscomments.RetestAllCommentEventType.String() ||
			event.EventType == opscomments.OkToTestCommentEventType.String() {
			logger.Debugf("MatchPipelinerunByAnnotation: filtering successful templates for event_type=%s", event.EventType)
			filtered := filterSuccessfulTemplates(ctx, logger, cs, event, repo, vcx, matchedPRs)
			if len(filtered) == 0 {
				return nil, ErrNoFailedPipelineToRetest
			}
			return filtered, nil
		}
		return matchedPRs, nil
	}

	return nil, fmt.Errorf("%s", buildAvailableMatchingAnnotationErr(event, pruns))
}

// filterSuccessfulTemplates filters out templates that already have successful PipelineRuns
// when executing /ok-to-test or /retest gitops commands, implementing per-template checking.
func filterSuccessfulTemplates(ctx context.Context, logger *zap.SugaredLogger, cs *params.Run, event *info.Event, repo *apipac.Repository, vcx provider.Interface, matchedPRs []Match) []Match {
	if event.SHA == "" {
		return matchedPRs
	}

	// Get all existing PipelineRuns for this SHA
	labelSelector := fmt.Sprintf("%s=%s", keys.SHA, formatting.CleanValueKubernetes(event.SHA))
	existingPRs, err := cs.Clients.Tekton.TektonV1().PipelineRuns(repo.GetNamespace()).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		logger.Errorf("failed to list existing PipelineRuns for SHA %s: %v", event.SHA, err)
		return matchedPRs // Return all templates if we can't check
	}
	logger.Debugf("filterSuccessfulTemplates: existing pipelineruns=%d for sha=%s", len(existingPRs.Items), event.SHA)

	// Create a map of template names to their most recent successful run
	successfulTemplates := make(map[string]*tektonv1.PipelineRun)

	for i := range existingPRs.Items {
		pr := &existingPRs.Items[i]

		// Get the original template name this PipelineRun came from
		originalPRName, ok := pr.GetAnnotations()[keys.OriginalPRName]
		if !ok {
			originalPRName, ok = pr.GetLabels()[keys.OriginalPRName]
		}
		if !ok {
			continue // Skip PipelineRuns without template identification
		}

		// Only completed successful PipelineRuns should suppress a /retest.
		if pr.Status.GetCondition(apis.ConditionSucceeded).IsTrue() {
			// Keep the most recent successful run for each template
			if existing, exists := successfulTemplates[originalPRName]; !exists ||
				pr.CreationTimestamp.After(existing.CreationTimestamp.Time) {
				successfulTemplates[originalPRName] = pr
			}
		}
	}

	// Also check provider commit statuses (covers pruned PipelineRuns)
	commitStatuses, err := vcx.GetCommitStatuses(ctx, event)
	if err != nil {
		logger.Warnf("failed to get commit statuses from provider for SHA %s: %v", event.SHA, err)
	} else if len(commitStatuses) > 0 {
		appName := cs.Info.GetPacOpts().ApplicationName
		for _, cs := range commitStatuses {
			originalName := parseOriginalPRName(cs.Name, appName)
			if originalName != "" && isSuccessStatus(cs.Status) {
				successfulTemplates[originalName] = &tektonv1.PipelineRun{} // placeholder
			}
		}
	}

	// Filter out templates that have successful runs
	var filteredPRs []Match

	for _, match := range matchedPRs {
		templateName := getName(match.PipelineRun)

		if successfulPR, hasSuccessfulRun := successfulTemplates[templateName]; hasSuccessfulRun {
			logger.Infof("skipping template '%s' for sha %s as it already has a successful pipelinerun '%s'",
				templateName, event.SHA, successfulPR.Name)
		} else {
			filteredPRs = append(filteredPRs, match)
		}
	}

	// Return the filtered list (which may be empty if all templates were skipped)
	return filteredPRs
}

// parseOriginalPRName extracts the original PipelineRun name from a commit status name.
// The commit status name format is "ApplicationName / OriginalPRName".
func parseOriginalPRName(checkName, appName string) string {
	prefix := appName + " / "
	if strings.HasPrefix(checkName, prefix) {
		return strings.TrimPrefix(checkName, prefix)
	}
	return ""
}

// isSuccessStatus returns true if the status string represents a successful state
// across different git providers.
func isSuccessStatus(status string) bool {
	switch strings.ToLower(status) {
	case "success", "successful", "completed":
		return true
	}
	return false
}

func buildAvailableMatchingAnnotationErr(event *info.Event, pruns []*tektonv1.PipelineRun) string {
	errmsg := "available annotations of the PipelineRuns annotations in .tekton/ dir:"
	for _, prun := range pruns {
		name := getName(prun)
		errmsg += fmt.Sprintf(" [PipelineRun: %s, annotations:", name)
		for annotation, value := range prun.GetAnnotations() {
			if !strings.HasPrefix(annotation, pipelinesascode.GroupName+"/on-") {
				continue
			}
			errmsg += fmt.Sprintf(" %s: ", strings.Replace(annotation, pipelinesascode.GroupName+"/", "", 1))
			if annotation == keys.OnCelExpression {
				errmsg += "celexpression"
			} else {
				errmsg += value
			}
			errmsg += ", "
		}
		errmsg = strings.TrimSuffix(errmsg, ", ")
		errmsg += "],"
	}
	errmsg = strings.TrimSpace(errmsg)
	errmsg = strings.TrimSuffix(errmsg, ",")
	nopsevent := ""
	if event.EventType != opscomments.NoOpsCommentEventType.String() {
		nopsevent = fmt.Sprintf(" payload target event is %s with", event.EventType)
	}
	errmsg = fmt.Sprintf("cannot match the event to any pipelineruns in the .tekton/ directory,%s source branch %s and target branch %s. %s", nopsevent, event.HeadBranch, event.BaseBranch, errmsg)
	return errmsg
}

func matchOnAnnotation(annotations string, eventType []string, branchMatching bool) (bool, error) {
	targets, err := getAnnotationValues(annotations)
	if err != nil {
		return false, err
	}

	var gotit string
	for _, v := range targets {
		for _, e := range eventType {
			if v == e {
				gotit = v
			}

			if branchMatching && branchMatch(v, e) {
				gotit = v
			}
		}
	}
	if gotit == "" {
		return false, nil
	}
	return true, nil
}

func MatchRunningPipelineRunForIncomingWebhook(eventType, incomingPipelineRun string, prs []*tektonv1.PipelineRun) []*tektonv1.PipelineRun {
	// return all pipelineruns if EventType is not incoming or TargetPipelineRun is ""
	if eventType != "incoming" || incomingPipelineRun == "" {
		return prs
	}

	for _, pr := range prs {
		// check incomingPipelineRun with pr name or generateName
		if incomingPipelineRun == pr.GetName() || incomingPipelineRun == pr.GetGenerateName() {
			return []*tektonv1.PipelineRun{pr}
		}
	}
	return nil
}

// resolveCustomParamsForCEL resolves custom parameters from the Repository CR for use in CEL expressions.
// It returns a map of parameter names to values, excluding reserved keywords.
// All parameters are returned as strings, including those from secret_ref.
func resolveCustomParamsForCEL(ctx context.Context, repo *apipac.Repository, event *info.Event, cs *params.Run, vcx provider.Interface, eventEmitter *events.EventEmitter, logger *zap.SugaredLogger) map[string]string {
	if repo == nil || repo.Spec.Params == nil {
		return map[string]string{}
	}
	logger.Debugf("resolveCustomParamsForCEL: repo=%s/%s params=%d", repo.GetNamespace(), repo.GetName(), len(*repo.Spec.Params))

	// Create kubeinteraction interface
	kinteract, err := kubeinteraction.NewKubernetesInteraction(cs)
	if err != nil {
		logger.Warnf("failed to create kubernetes interaction for custom params: %s", err.Error())
		return map[string]string{}
	}

	// Use existing customparams package to resolve all params
	cp := customparams.NewCustomParams(event, repo, cs, kinteract, eventEmitter, vcx)
	allParams, _, err := cp.GetParams(ctx)
	if err != nil {
		eventEmitter.EmitMessage(repo, zap.WarnLevel, "CustomParamsCELError",
			fmt.Sprintf("failed to resolve custom params for CEL: %s", err.Error()))
		return map[string]string{}
	}
	logger.Debugf("resolveCustomParamsForCEL: resolved %d total params", len(allParams))

	// Filter to only include params defined in repo.Spec.Params (not standard PAC params)
	result := make(map[string]string)
	for _, param := range *repo.Spec.Params {
		if value, ok := allParams[param.Name]; ok {
			result[param.Name] = value
		}
	}
	logger.Debugf("resolveCustomParamsForCEL: returned %d custom params", len(result))

	return result
}
