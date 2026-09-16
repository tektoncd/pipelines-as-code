package matcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/decls"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"github.com/gobwas/glob"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/changedfiles"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"go.uber.org/zap"
)

func celEvaluate(ctx context.Context, expr string, event *info.Event, vcx provider.Interface, customParams map[string]string, eventEmitter *events.EventEmitter, repo *apipac.Repository) (ref.Val, error) {
	eventTitle := event.PullRequestTitle
	if event.TriggerTarget == triggertype.Push {
		eventTitle = event.SHATitle
	}

	nbody, err := json.Marshal(event.Event)
	if err != nil {
		return nil, err
	}
	var jsonMap map[string]any
	err = json.Unmarshal(nbody, &jsonMap)
	if err != nil {
		return nil, err
	}
	headerMap := make(map[string]string)
	for k, v := range event.Request.Header {
		headerMap[strings.ToLower(k)] = v[0]
	}

	standardParams := map[string]bool{
		"event": true, "event_type": true, "headers": true, "body": true,
		"event_title": true, "target_branch": true, "source_branch": true,
		"target_url": true, "source_url": true, "files": true,
	}

	varDecls := []cel.EnvOption{
		cel.Lib(celPac{vcx, ctx, event}),
		cel.VariableDecls(
			decls.NewVariable("event", types.StringType),
			decls.NewVariable("event_type", types.StringType),
			decls.NewVariable("headers", types.NewMapType(types.StringType, types.DynType)),
			decls.NewVariable("body", types.NewMapType(types.StringType, types.DynType)),
			decls.NewVariable("event_title", types.StringType),
			decls.NewVariable("target_branch", types.StringType),
			decls.NewVariable("source_branch", types.StringType),
			decls.NewVariable("target_url", types.StringType),
			decls.NewVariable("source_url", types.StringType),
			decls.NewVariable("files", types.NewMapType(types.StringType, types.DynType)),
		),
	}

	for k := range customParams {
		if !standardParams[k] {
			varDecls = append(varDecls, cel.VariableDecls(decls.NewVariable(k, types.StringType)))
		}
	}

	env, err := cel.NewEnv(varDecls...)
	if err != nil {
		return nil, err
	}

	parsed, issues := env.Parse(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("failed to parse expression %#v: %w", expr, issues.Err())
	}

	checked, issues := env.Check(parsed)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("expression %#v check failed: %w", expr, issues.Err())
	}

	// Convert AST for inspection
	checkedExpr, err := cel.AstToCheckedExpr(checked)
	if err != nil {
		return nil, fmt.Errorf("failed to convert AST: %w", err)
	}
	astRoot := checkedExpr.GetExpr()

	// For push events, handle refs/heads/ prefix stripping for target_branch and source_branch.
	// We use AST inspection to detect if these variables are referenced, rather than string parsing.
	if event.TriggerTarget == triggertype.Push {
		// Check if expression uses target_branch and doesn't contain refs/heads/ literal
		if walkExprAST(astRoot, matchIdentifier("target_branch")) {
			if !containsRefsHeadsLiteral(astRoot) {
				event.BaseBranch = strings.TrimPrefix(event.BaseBranch, "refs/heads/")
			}
		}
		// Check if expression uses source_branch and doesn't contain refs/heads/ literal
		if walkExprAST(astRoot, matchIdentifier("source_branch")) {
			if !containsRefsHeadsLiteral(astRoot) {
				event.HeadBranch = strings.TrimPrefix(event.HeadBranch, "refs/heads/")
			}
		}
	}

	// Fetch changed files only if the expression references the "files" variable.
	// This avoids unnecessary API calls when files aren't used in the expression.
	changedFiles := changedfiles.ChangedFiles{}
	if walkExprAST(astRoot, matchIdentifier("files")) {
		changedFiles, err = vcx.GetFiles(ctx, event)
		if err != nil {
			return nil, err
		}
	}

	// For label events, check if the expression references labels or event_type.
	// If not, return False to skip matching - this prevents generic "event == pull_request"
	// expressions from unintentionally matching on label add/remove events.
	if event.TriggerTarget == triggertype.PullRequest && event.EventType == string(triggertype.PullRequestLabeled) {
		labelMatcher := combinedMatcher(
			matchIdentifier("event_type"),
			matchFieldAccess("labels", "pull_request_labels"),
			matchBracketAccess("labels", "pull_request_labels"),
		)
		if !walkExprAST(astRoot, labelMatcher) {
			return types.False, nil
		}
	}

	data := map[string]any{
		"event":         event.TriggerTarget.String(),
		"event_type":    event.EventType,
		"event_title":   eventTitle,
		"target_branch": event.BaseBranch,
		"source_branch": event.HeadBranch,
		"target_url":    event.BaseURL,
		"source_url":    event.HeadURL,
		"body":          jsonMap,
		"headers":       headerMap,
		"files": map[string]any{
			"all":      changedFiles.All,
			"added":    changedFiles.Added,
			"deleted":  changedFiles.Deleted,
			"modified": changedFiles.Modified,
			"renamed":  changedFiles.Renamed,
		},
	}

	for k, v := range customParams {
		if !standardParams[k] {
			data[k] = v
		} else if eventEmitter != nil && repo != nil {
			eventEmitter.EmitMessage(repo, zap.WarnLevel, "CELParamConflict",
				fmt.Sprintf("custom parameter '%s' conflicts with standard CEL variable and was ignored", k))
		}
	}

	prg, err := env.Program(checked)
	if err != nil {
		return nil, fmt.Errorf("expression %#v failed to create a Program: %w", expr, err)
	}

	out, _, err := prg.Eval(data)
	if err != nil {
		return nil, fmt.Errorf("expression %#v failed to evaluate: %w", expr, err)
	}
	return out, nil
}

type celPac struct {
	vcx   provider.Interface
	ctx   context.Context
	event *info.Event
}

func (t celPac) ProgramOptions() []cel.ProgramOption {
	return []cel.ProgramOption{}
}

func (t celPac) pathChanged(vals ref.Val) ref.Val {
	var match types.Bool
	changedFiles, err := t.vcx.GetFiles(t.ctx, t.event)
	if err != nil {
		return types.Bool(false)
	}
	for i := range changedFiles.All {
		if v, ok := vals.Value().(string); ok {
			g := glob.MustCompile(v)
			if g.Match(changedFiles.All[i]) {
				return types.Bool(true)
			}
		}
		match = types.Bool(false)
	}

	return match
}

func (t celPac) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("pathChanged",
			cel.MemberOverload("pathChanged", []*cel.Type{cel.StringType}, cel.BoolType,
				cel.UnaryBinding(t.pathChanged))),
	}
}
