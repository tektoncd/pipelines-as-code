package matcher

import (
	"testing"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/decls"
	"cel.dev/cel-go/common/types"
	"gotest.tools/v3/assert"
)

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
