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
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

// standardParams are the variables a custom parameter cannot override.
var standardParams = map[string]bool{
	"event": true, "event_type": true, "headers": true, "body": true,
	"event_title": true, "target_branch": true, "source_branch": true,
	"target_url": true, "source_url": true, "files": true,
}

// followUpParams are the follow-up only variables a custom parameter cannot
// override either.
var followUpParams = map[string]bool{"runs": true}

func isStandardParam(k string, followUp bool) bool {
	return standardParams[k] || (followUp && followUpParams[k])
}

// celEnv declares the CEL variables of an on-cel-expression. A follow-up's
// expression is evaluated once the webhook payload is gone: it has no body,
// headers or files and no pathChanged function, and has the runs map of its
// dependencies instead.
func celEnv(lib celPac, customParams map[string]string, followUp bool) (*cel.Env, error) {
	vars := []*decls.VariableDecl{
		decls.NewVariable("event", types.StringType),
		decls.NewVariable("event_type", types.StringType),
		decls.NewVariable("event_title", types.StringType),
		decls.NewVariable("target_branch", types.StringType),
		decls.NewVariable("source_branch", types.StringType),
		decls.NewVariable("target_url", types.StringType),
		decls.NewVariable("source_url", types.StringType),
	}
	if followUp {
		vars = append(vars, decls.NewVariable("runs", types.NewMapType(types.StringType, types.DynType)))
	} else {
		vars = append(vars,
			decls.NewVariable("headers", types.NewMapType(types.StringType, types.DynType)),
			decls.NewVariable("body", types.NewMapType(types.StringType, types.DynType)),
			decls.NewVariable("files", types.NewMapType(types.StringType, types.DynType)),
		)
	}
	varDecls := []cel.EnvOption{cel.VariableDecls(vars...)}
	if !followUp {
		// the changed files of the event cannot be read reliably once the
		// payload is gone: some providers take them from the payload, others
		// from the live pull request diff
		varDecls = append(varDecls, cel.Lib(lib))
	}
	for k := range customParams {
		if !isStandardParam(k, followUp) {
			varDecls = append(varDecls, cel.VariableDecls(decls.NewVariable(k, types.StringType)))
		}
	}
	return cel.NewEnv(varDecls...)
}

func celCompile(env *cel.Env, expr string) (*cel.Ast, error) {
	parsed, issues := env.Parse(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("failed to parse expression %#v: %w", expr, issues.Err())
	}

	checked, issues := env.Check(parsed)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("expression %#v check failed: %w", expr, issues.Err())
	}
	return checked, nil
}

func celEvaluate(ctx context.Context, expr string, event *info.Event, vcx provider.Interface, customParams map[string]string, eventEmitter *events.EventEmitter, repo *apipac.Repository) (ref.Val, error) {
	return evaluateCEL(ctx, expr, event, vcx, customParams, eventEmitter, repo, false, nil)
}

// EvaluateFollowUp evaluates the on-cel-expression of a follow-up exactly like
// the matcher evaluates an on-cel-expression: same variables, same
// refs/heads/ handling, same custom parameters. The webhook payload variables
// (body, headers, files) and the pathChanged function are not available, the
// runs map of the dependencies is. The event is copied, never modified.
func EvaluateFollowUp(ctx context.Context, expr string, event *info.Event, vcx provider.Interface,
	customParams map[string]string, eventEmitter *events.EventEmitter, repo *apipac.Repository, runs map[string]any,
) (bool, error) {
	eventCopy := *event
	out, err := evaluateCEL(ctx, expr, &eventCopy, vcx, customParams, eventEmitter, repo, true, runs)
	if err != nil {
		return false, err
	}
	return out == types.True, nil
}

// ValidateFollowUp compile-checks the on-cel-expression of a follow-up at
// event time, with the custom parameters of the repository declared.
func ValidateFollowUp(expr string, customParamNames []string) error {
	customParams := make(map[string]string, len(customParamNames))
	for _, name := range customParamNames {
		customParams[name] = ""
	}
	env, err := celEnv(celPac{}, customParams, true)
	if err != nil {
		return err
	}
	_, err = celCompile(env, expr)
	if err != nil && callsPathChanged(env, expr) {
		return fmt.Errorf("expression %#v calls pathChanged(), which a follow-up cannot use: its changed files are not available once the dependencies finished, put the path condition on the dependencies instead", expr)
	}
	return err
}

// callsPathChanged reports whether an expression calls pathChanged().
func callsPathChanged(env *cel.Env, expr string) bool {
	parsed, issues := env.Parse(expr)
	if issues != nil && issues.Err() != nil {
		return false
	}
	parsedExpr, err := cel.AstToParsedExpr(parsed)
	if err != nil {
		return false
	}
	return walkExprAST(parsedExpr.GetExpr(), func(e *exprpb.Expr) bool {
		return e.GetCallExpr().GetFunction() == "pathChanged"
	})
}

func evaluateCEL(ctx context.Context, expr string, event *info.Event, vcx provider.Interface,
	customParams map[string]string, eventEmitter *events.EventEmitter, repo *apipac.Repository,
	followUp bool, runs map[string]any,
) (ref.Val, error) {
	eventTitle := event.PullRequestTitle
	if event.TriggerTarget == triggertype.Push {
		eventTitle = event.SHATitle
	}

	env, err := celEnv(celPac{vcx, ctx, event}, customParams, followUp)
	if err != nil {
		return nil, err
	}

	checked, err := celCompile(env, expr)
	if err != nil {
		return nil, err
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

	data := map[string]any{
		"event":         event.TriggerTarget.String(),
		"event_type":    event.EventType,
		"event_title":   eventTitle,
		"target_branch": event.BaseBranch,
		"source_branch": event.HeadBranch,
		"target_url":    event.BaseURL,
		"source_url":    event.HeadURL,
	}

	if followUp {
		data["runs"] = runs
	} else {
		nbody, err := json.Marshal(event.Event)
		if err != nil {
			return nil, err
		}
		var jsonMap map[string]any
		if err := json.Unmarshal(nbody, &jsonMap); err != nil {
			return nil, err
		}
		headerMap := make(map[string]string)
		for k, v := range event.Request.Header {
			headerMap[strings.ToLower(k)] = v[0]
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

		data["body"] = jsonMap
		data["headers"] = headerMap
		data["files"] = map[string]any{
			"all":      changedFiles.All,
			"added":    changedFiles.Added,
			"deleted":  changedFiles.Deleted,
			"modified": changedFiles.Modified,
			"renamed":  changedFiles.Renamed,
		}
	}

	for k, v := range customParams {
		if !isStandardParam(k, followUp) {
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
