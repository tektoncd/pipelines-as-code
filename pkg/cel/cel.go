package cel

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/decls"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/ext"
)

func evaluate(expr string, env *cel.Env, data map[string]any) (ref.Val, error) {
	parsed, issues := env.Parse(expr)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("failed to parse expression %#v: %w", expr, issues.Err())
	}

	checked, issues := env.Check(parsed)
	if issues != nil && issues.Err() != nil {
		return nil, fmt.Errorf("expression %#v check failed: %w", expr, issues.Err())
	}

	prg, err := env.Program(checked, cel.EvalOptions(cel.OptOptimize))
	if err != nil {
		return nil, fmt.Errorf("expression %#v failed to create a Program: %w", expr, err)
	}

	out, _, err := prg.Eval(data)
	if err != nil {
		return nil, fmt.Errorf("expression %#v failed to evaluate: %w", expr, err)
	}

	return out, nil
}

// Value evaluates a CEL expression with the given body, headers and
// / pacParams, it will output a Cel value or an error if selectedjm.
func Value(query string, body any, headers, pacParams map[string]string, changedFiles map[string]any) (ref.Val, error) {
	// Marshal/Unmarshal the body to a map[string]any so we can access it from the CEL
	var jsonMap map[string]any
	switch b := body.(type) {
	case nil:
		jsonMap = map[string]any{}
	case map[string]any:
		jsonMap = b
	default:
		nbody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		trimmed := bytes.TrimSpace(nbody)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			jsonMap = map[string]any{}
		} else {
			err = json.Unmarshal(nbody, &jsonMap)
			if err != nil {
				return nil, err
			}
			if jsonMap == nil {
				jsonMap = map[string]any{}
			}
		}
	}

	mapStrDyn := types.NewMapType(types.StringType, types.DynType)
	celDec, _ := cel.NewEnv(
		cel.VariableDecls(
			decls.NewVariable("body", mapStrDyn),
			decls.NewVariable("headers", mapStrDyn),
			decls.NewVariable("pac", mapStrDyn),
			decls.NewVariable("files", mapStrDyn),
			// Direct variables as per documentation
			decls.NewVariable("event", types.StringType),
			decls.NewVariable("event_type", types.StringType),
			decls.NewVariable("target_branch", types.StringType),
			decls.NewVariable("source_branch", types.StringType),
			decls.NewVariable("target_url", types.StringType),
			decls.NewVariable("source_url", types.StringType),
			decls.NewVariable("event_title", types.StringType),
			decls.NewVariable("revision", types.StringType),
			decls.NewVariable("repo_owner", types.StringType),
			decls.NewVariable("repo_name", types.StringType),
			decls.NewVariable("sender", types.StringType),
			decls.NewVariable("repo_url", types.StringType),
			decls.NewVariable("git_tag", types.StringType),
			decls.NewVariable("target_namespace", types.StringType),
			decls.NewVariable("trigger_comment", types.StringType),
			decls.NewVariable("pull_request_labels", types.StringType),
			decls.NewVariable("pull_request_number", types.StringType),
			decls.NewVariable("git_auth_secret", types.StringType),
		),
		ext.Strings(),
		ext.Lists(),
	)
	val, err := evaluate(query, celDec, map[string]any{
		"body":    jsonMap,
		"pac":     pacParams,
		"headers": headers,
		"files":   changedFiles,
		// Direct variables - all from pacParams
		"event":               pacParams["event"],
		"event_type":          pacParams["event_type"],
		"target_branch":       pacParams["target_branch"],
		"source_branch":       pacParams["source_branch"],
		"target_url":          pacParams["target_url"],
		"source_url":          pacParams["source_url"],
		"event_title":         pacParams["event_title"],
		"revision":            pacParams["revision"],
		"repo_owner":          pacParams["repo_owner"],
		"repo_name":           pacParams["repo_name"],
		"sender":              pacParams["sender"],
		"repo_url":            pacParams["repo_url"],
		"git_tag":             pacParams["git_tag"],
		"target_namespace":    pacParams["target_namespace"],
		"trigger_comment":     pacParams["trigger_comment"],
		"pull_request_labels": pacParams["pull_request_labels"],
		"pull_request_number": pacParams["pull_request_number"],
		"git_auth_secret":     pacParams["git_auth_secret"],
	})
	if err != nil {
		return nil, err
	}
	return val, nil
}
