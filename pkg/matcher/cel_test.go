package matcher

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/decls"
	"cel.dev/cel-go/common/types"
	"github.com/tektoncd/pipelines-as-code/pkg/changedfiles"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	"github.com/tektoncd/pipelines-as-code/pkg/params/triggertype"
	pacprovider "github.com/tektoncd/pipelines-as-code/pkg/provider"
	testprovider "github.com/tektoncd/pipelines-as-code/pkg/test/provider"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
	"gotest.tools/v3/assert"
)

type failingFilesProvider struct {
	testprovider.TestProviderImp
}

func (v failingFilesProvider) GetFiles(context.Context, *info.Event) (changedfiles.ChangedFiles, error) {
	return changedfiles.ChangedFiles{}, fmt.Errorf("failed to get files")
}

// parseAndCheckForLabelReferences is a test helper that parses a CEL expression
// and checks if it references labels or event_type using the AST walker.
func parseAndCheckForLabelReferences(expr string) bool {
	env, err := cel.NewEnv(
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
	)
	if err != nil {
		return false
	}

	parsed, issues := env.Parse(expr)
	if issues != nil && issues.Err() != nil {
		return false
	}

	checked, issues := env.Check(parsed)
	if issues != nil && issues.Err() != nil {
		return false
	}

	checkedExpr, err := cel.AstToCheckedExpr(checked)
	if err != nil {
		return false
	}

	// Use the generic walker with combined matchers for label references
	labelMatcher := combinedMatcher(
		matchIdentifier("event_type"),
		matchFieldAccess("labels", "pull_request_labels"),
		matchBracketAccess("labels", "pull_request_labels"),
	)
	return walkExprAST(checkedExpr.GetExpr(), labelMatcher)
}

func TestWalkExprForLabelReferences(t *testing.T) {
	tests := []struct {
		name     string
		expr     string
		expected bool
	}{
		{
			name:     "references event_type directly",
			expr:     `event_type == "pull_request_labeled"`,
			expected: true,
		},
		{
			name:     "references event_type in complex expression",
			expr:     `event == "pull_request" && event_type == "pull_request_labeled"`,
			expected: true,
		},
		{
			name:     "references body.pull_request.labels (GitHub/Gitea style)",
			expr:     `body.pull_request.labels.exists(x, x.name == "bug")`,
			expected: true,
		},
		{
			name:     "references body.labels (GitLab style)",
			expr:     `body.labels.exists(x, x.title == "bug")`,
			expected: true,
		},
		{
			name:     "references labels with size check",
			expr:     `body.pull_request.labels.size() > 0`,
			expected: true,
		},
		{
			name:     "simple pull_request event check - no labels",
			expr:     `event == "pull_request"`,
			expected: false,
		},
		{
			name:     "pull_request with branch check - no labels",
			expr:     `event == "pull_request" && target_branch == "main"`,
			expected: false,
		},
		{
			name:     "title contains label word - should NOT match (string literal)",
			expr:     `event == "pull_request" && event_title == "fix-label-issue"`,
			expected: false,
		},
		{
			name:     "body.pull_request.title with label in value - should NOT match",
			expr:     `body.pull_request.title == "Add labels support"`,
			expected: false,
		},
		{
			name:     "PR title equals 'labels' literally - should NOT match (string literal)",
			expr:     `event_title == "labels"`,
			expected: false,
		},
		{
			name:     "PR title contains 'labels' - should NOT match (string method on literal)",
			expr:     `event_title.contains("labels")`,
			expected: false,
		},
		{
			name:     "head.label (branch label) - should NOT match",
			expr:     `body.pull_request.head.label == "feature-branch"`,
			expected: false,
		},
		{
			name:     "push event - no labels",
			expr:     `event == "push" && target_branch == "main"`,
			expected: false,
		},
		{
			name:     "path changed - no labels",
			expr:     `event == "pull_request" && files.all.exists(x, x.matches("docs/"))`,
			expected: false,
		},
		{
			name:     "labels in comprehension filter",
			expr:     `body.pull_request.labels.filter(x, x.name.startsWith("kind/")).size() > 0`,
			expected: true,
		},
		// Bracket notation tests - CEL index operator _[_]
		{
			name:     "bracket notation - body[\"labels\"] (GitLab style)",
			expr:     `body["labels"].size() > 0`,
			expected: true,
		},
		{
			name:     "bracket notation - body[\"pull_request\"][\"labels\"]",
			expr:     `body["pull_request"]["labels"].size() > 0`,
			expected: true,
		},
		{
			name:     "mixed notation - body.pull_request[\"labels\"]",
			expr:     `body.pull_request["labels"].size() > 0`,
			expected: true,
		},
		{
			name:     "deeply nested bracket notation",
			expr:     `body["object"]["pull_request"]["labels"].size() > 0`,
			expected: true,
		},
		{
			name:     "ternary with labels in condition",
			expr:     `body.labels.size() > 0 ? true : false`,
			expected: true,
		},
		{
			name:     "ternary with labels in true branch",
			expr:     `event == "pull_request" ? body.labels.size() : 0`,
			expected: true,
		},
		{
			name:     "ternary with labels in false branch",
			expr:     `event == "push" ? 0 : body.labels.size()`,
			expected: true,
		},
		{
			name:     "ternary without labels - should NOT match",
			expr:     `event == "pull_request" ? "pr" : "other"`,
			expected: false,
		},
		{
			name:     "invalid CEL expression - returns false",
			expr:     `this is not valid CEL`,
			expected: false,
		},
		{
			name:     "empty expression - returns false",
			expr:     ``,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAndCheckForLabelReferences(tt.expr)
			assert.Equal(t, tt.expected, result, "expression: %s", tt.expr)
		})
	}
}

func TestWalkExprASTNodeKinds(t *testing.T) {
	needle := &exprpb.Expr{
		ExprKind: &exprpb.Expr_IdentExpr{
			IdentExpr: &exprpb.Expr_Ident{Name: "needle"},
		},
	}
	other := &exprpb.Expr{
		ExprKind: &exprpb.Expr_IdentExpr{
			IdentExpr: &exprpb.Expr_Ident{Name: "other"},
		},
	}

	tests := []struct {
		name string
		expr *exprpb.Expr
		want bool
	}{
		{
			name: "nil expression",
			want: false,
		},
		{
			name: "const expression has no children",
			expr: &exprpb.Expr{
				ExprKind: &exprpb.Expr_ConstExpr{
					ConstExpr: &exprpb.Constant{ConstantKind: &exprpb.Constant_StringValue{StringValue: "needle"}},
				},
			},
			want: false,
		},
		{
			name: "identifier current node",
			expr: needle,
			want: true,
		},
		{
			name: "select operand",
			expr: &exprpb.Expr{
				ExprKind: &exprpb.Expr_SelectExpr{
					SelectExpr: &exprpb.Expr_Select{Operand: needle, Field: "field"},
				},
			},
			want: true,
		},
		{
			name: "call target",
			expr: &exprpb.Expr{
				ExprKind: &exprpb.Expr_CallExpr{
					CallExpr: &exprpb.Expr_Call{Target: needle},
				},
			},
			want: true,
		},
		{
			name: "call argument",
			expr: &exprpb.Expr{
				ExprKind: &exprpb.Expr_CallExpr{
					CallExpr: &exprpb.Expr_Call{Args: []*exprpb.Expr{other, needle}},
				},
			},
			want: true,
		},
		{
			name: "list element",
			expr: &exprpb.Expr{
				ExprKind: &exprpb.Expr_ListExpr{
					ListExpr: &exprpb.Expr_CreateList{Elements: []*exprpb.Expr{other, needle}},
				},
			},
			want: true,
		},
		{
			name: "struct map key",
			expr: &exprpb.Expr{
				ExprKind: &exprpb.Expr_StructExpr{
					StructExpr: &exprpb.Expr_CreateStruct{Entries: []*exprpb.Expr_CreateStruct_Entry{
						{
							KeyKind: &exprpb.Expr_CreateStruct_Entry_MapKey{MapKey: needle},
							Value:   other,
						},
					}},
				},
			},
			want: true,
		},
		{
			name: "struct value",
			expr: &exprpb.Expr{
				ExprKind: &exprpb.Expr_StructExpr{
					StructExpr: &exprpb.Expr_CreateStruct{Entries: []*exprpb.Expr_CreateStruct_Entry{
						{
							KeyKind: &exprpb.Expr_CreateStruct_Entry_MapKey{MapKey: other},
							Value:   needle,
						},
					}},
				},
			},
			want: true,
		},
		{
			name: "comprehension result",
			expr: &exprpb.Expr{
				ExprKind: &exprpb.Expr_ComprehensionExpr{
					ComprehensionExpr: &exprpb.Expr_Comprehension{
						IterRange:     other,
						AccuInit:      other,
						LoopCondition: other,
						LoopStep:      other,
						Result:        needle,
					},
				},
			},
			want: true,
		},
		{
			name: "no match",
			expr: other,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, walkExprAST(tt.expr, matchIdentifier("needle")))
		})
	}
}

func TestNodeMatchers(t *testing.T) {
	stringKey := &exprpb.Expr{
		ExprKind: &exprpb.Expr_ConstExpr{
			ConstExpr: &exprpb.Constant{ConstantKind: &exprpb.Constant_StringValue{StringValue: "labels"}},
		},
	}

	tests := []struct {
		name    string
		matcher NodeMatcher
		expr    *exprpb.Expr
		want    bool
	}{
		{
			name:    "field access matches configured field",
			matcher: matchFieldAccess("labels"),
			expr: &exprpb.Expr{ExprKind: &exprpb.Expr_SelectExpr{
				SelectExpr: &exprpb.Expr_Select{Field: "labels"},
			}},
			want: true,
		},
		{
			name:    "field access ignores different field",
			matcher: matchFieldAccess("labels"),
			expr: &exprpb.Expr{ExprKind: &exprpb.Expr_SelectExpr{
				SelectExpr: &exprpb.Expr_Select{Field: "title"},
			}},
			want: false,
		},
		{
			name:    "bracket access matches string key",
			matcher: matchBracketAccess("labels"),
			expr: &exprpb.Expr{ExprKind: &exprpb.Expr_CallExpr{
				CallExpr: &exprpb.Expr_Call{Function: "_[_]", Args: []*exprpb.Expr{{}, stringKey}},
			}},
			want: true,
		},
		{
			name:    "bracket access ignores wrong function",
			matcher: matchBracketAccess("labels"),
			expr: &exprpb.Expr{ExprKind: &exprpb.Expr_CallExpr{
				CallExpr: &exprpb.Expr_Call{Function: "_+_", Args: []*exprpb.Expr{{}, stringKey}},
			}},
			want: false,
		},
		{
			name:    "bracket access ignores wrong arg count",
			matcher: matchBracketAccess("labels"),
			expr: &exprpb.Expr{ExprKind: &exprpb.Expr_CallExpr{
				CallExpr: &exprpb.Expr_Call{Function: "_[_]", Args: []*exprpb.Expr{stringKey}},
			}},
			want: false,
		},
		{
			name:    "combined matcher returns true on any match",
			matcher: combinedMatcher(matchIdentifier("other"), matchIdentifier("needle")),
			expr: &exprpb.Expr{ExprKind: &exprpb.Expr_IdentExpr{
				IdentExpr: &exprpb.Expr_Ident{Name: "needle"},
			}},
			want: true,
		},
		{
			name:    "combined matcher returns false when none match",
			matcher: combinedMatcher(matchIdentifier("other"), matchIdentifier("needle")),
			expr: &exprpb.Expr{ExprKind: &exprpb.Expr_IdentExpr{
				IdentExpr: &exprpb.Expr_Ident{Name: "unknown"},
			}},
			want: false,
		},
		{
			name:    "refs heads literal is detected",
			matcher: func(expr *exprpb.Expr) bool { return containsRefsHeadsLiteral(expr) },
			expr: &exprpb.Expr{ExprKind: &exprpb.Expr_ConstExpr{
				ConstExpr: &exprpb.Constant{ConstantKind: &exprpb.Constant_StringValue{StringValue: "refs/heads/main"}},
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.matcher(tt.expr))
		})
	}
}

func TestCelEvaluateErrorBranches(t *testing.T) {
	tests := []struct {
		name           string
		expr           string
		event          *info.Event
		vcx            pacprovider.Interface
		wantErrContain string
	}{
		{
			name: "event body marshal error",
			expr: `event == "push"`,
			event: &info.Event{
				Event:   map[string]any{"bad": make(chan int)},
				Request: &info.Request{Header: http.Header{}},
			},
			vcx:            &testprovider.TestProviderImp{},
			wantErrContain: "unsupported type",
		},
		{
			name: "parse error",
			expr: `event ==`,
			event: &info.Event{
				Request: &info.Request{Header: http.Header{}},
			},
			vcx:            &testprovider.TestProviderImp{},
			wantErrContain: "failed to parse expression",
		},
		{
			name: "check error",
			expr: `missing == "value"`,
			event: &info.Event{
				Request: &info.Request{Header: http.Header{}},
			},
			vcx:            &testprovider.TestProviderImp{},
			wantErrContain: "check failed",
		},
		{
			name: "changed files error",
			expr: `files.all.exists(x, x.matches(".*"))`,
			event: &info.Event{
				Request: &info.Request{Header: http.Header{}},
			},
			vcx:            &failingFilesProvider{},
			wantErrContain: "failed to get files",
		},
		{
			name: "evaluation error",
			expr: `1 / 0 == 0`,
			event: &info.Event{
				Request: &info.Request{Header: http.Header{}},
			},
			vcx:            &testprovider.TestProviderImp{},
			wantErrContain: "failed to evaluate",
		},
		{
			name: "labeled event without label reference returns false",
			expr: `event == "pull_request"`,
			event: &info.Event{
				TriggerTarget: triggertype.PullRequest,
				EventType:     string(triggertype.PullRequestLabeled),
				Request:       &info.Request{Header: http.Header{}},
			},
			vcx: &testprovider.TestProviderImp{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := celEvaluate(context.Background(), tt.expr, tt.event, tt.vcx, nil, nil, nil)
			if tt.wantErrContain != "" {
				assert.ErrorContains(t, err, tt.wantErrContain)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, types.False, out)
		})
	}
}

func TestPathChangedReturnsFalseWhenFilesCannotBeFetched(t *testing.T) {
	tests := []struct {
		name string
		val  types.String
	}{
		{
			name: "get files error",
			val:  types.String("*.go"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pac := celPac{
				vcx:   &failingFilesProvider{},
				ctx:   context.Background(),
				event: &info.Event{},
			}
			assert.Equal(t, types.Bool(false), pac.pathChanged(tt.val))
		})
	}
}
