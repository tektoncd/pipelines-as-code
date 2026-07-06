package matcher

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/gobwas/glob"
	"github.com/google/cel-go/common/types"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
)

const (
	// regex allows array of string or a single string
	// eg. ["foo", "bar"], ["foo"] or "foo".
	reValidateTag = `^\[(.*)\]$|^[^[\]\s]*$`
)

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
		return []string{annotation}, nil
	}

	// Split all tasks by comma and make sure to trim spaces in there
	split := strings.Split(re.FindStringSubmatch(annotation)[1], ",")
	for i := range split {
		split[i] = strings.TrimSpace(split[i])
	}

	if split[0] == "" {
		return nil, fmt.Errorf("annotation \"%s\" has empty values", annotation)
	}

	return split, nil
}

func getTargetBranch(prun *tektonv1.PipelineRun, event *info.Event) (bool, string, string, error) {
	var targetEvent, targetBranch string
	if key, ok := prun.GetObjectMeta().GetAnnotations()[keys.OnEvent]; ok {
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

func MatchPipelinerunByAnnotation(ctx context.Context, logger *zap.SugaredLogger, pruns []*tektonv1.PipelineRun, cs *params.Run, event *info.Event, vcx provider.Interface) ([]Match, error) {
	matchedPRs := []Match{}
	infomsg := fmt.Sprintf("matching pipelineruns to event: URL=%s, target-branch=%s, source-branch=%s, target-event=%s",
		event.URL,
		event.BaseBranch,
		event.HeadBranch,
		event.TriggerTarget)

	if event.EventType == triggertype.Incoming.String() {
		infomsg = fmt.Sprintf("%s, target-pipelinerun=%s", infomsg, event.TargetPipelineRun)
	} else if event.EventType == triggertype.PullRequest.String() {
		infomsg = fmt.Sprintf("%s, pull-request=%d", infomsg, event.PullRequestNumber)
	}
	logger.Info(infomsg)

	for _, prun := range pruns {
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
		}

		if targetNS, ok := prun.GetObjectMeta().GetAnnotations()[keys.TargetNamespace]; ok {
			prMatch.Config["target-namespace"] = targetNS
			prMatch.Repo, _ = MatchEventURLRepo(ctx, cs, event, targetNS)
			if prMatch.Repo == nil {
				logger.Warnf("could not find Repository CRD in branch %s, the pipelineRun %s has a label that explicitly targets it", targetNS, prName)
				continue
			}
		}

		if targetComment, ok := prun.GetObjectMeta().GetAnnotations()[keys.OnComment]; ok {
			re, err := regexp.Compile(targetComment)
			if err != nil {
				logger.Warnf("could not compile regexp %s from pipelineRun %s", targetComment, prName)
				continue
			}
			if re.MatchString(event.TriggerComment) {
				event.EventType = opscomments.OnCommentEventType.String()
				logger.Infof("matched pipelinerun with name: %s on gitops comment: %q", prName, event.TriggerComment)
				matchedPRs = append(matchedPRs, prMatch)
				continue
			}
		}
		// if the event is a comment event, but we don't have any match from the keys.OnComment then skip the other evaluations
		if event.EventType == opscomments.NoOpsCommentEventType.String() || event.EventType == opscomments.OnCommentEventType.String() {
			continue
		}
		if celExpr, ok := prun.GetObjectMeta().GetAnnotations()[keys.OnCelExpression]; ok {
			out, err := celEvaluate(ctx, celExpr, event, vcx)
			if err != nil {
				logger.Errorf("there was an error evaluating the CEL expression, skipping: %v", err)
				continue
			}
			if out != types.True {
				logger.Infof("CEL expression for PipelineRun %s is not matching, skipping", prName)
				continue
			}
			logger.Infof("CEL expression has been evaluated and matched")
		} else {
			matched, targetEvent, targetBranch, err := getTargetBranch(prun, event)
			if err != nil {
				return matchedPRs, err
			}
			if !matched {
				continue
			}
			prMatch.Config["target-branch"] = targetBranch
			prMatch.Config["target-event"] = targetEvent
		}

		logger.Infof("matched pipelinerun with name: %s, annotation Config: %q", prName, prMatch.Config)
		matchedPRs = append(matchedPRs, prMatch)
	}

	if len(matchedPRs) > 0 {
		return matchedPRs, nil
	}

	return nil, fmt.Errorf("%s", buildAvailableMatchingAnnotationErr(event, pruns))
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
		// check incomingPipelineRun with pr name
		if incomingPipelineRun == pr.GetName() {
			return []*tektonv1.PipelineRun{pr}
		}
		// check incomingPipelineRun with pr generateName
		if incomingPipelineRun == strings.TrimSuffix(pr.GetGenerateName(), "-") {
			return []*tektonv1.PipelineRun{pr}
		}
	}
	return nil
}
