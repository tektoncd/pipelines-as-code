# Pipelines-as-Code Project Guide

## Project Overview

- Tekton pipeline integration with Git providers: GitHub, GitLab, Bitbucket, Gitea
- Targets arm64 and amd64 architectures
- Uses `ko` for building container images in development

## Provider Logging

In provider code (`pkg/provider/`), always use the provider's own logger (`v.Logger`) for logging — never `run.Clients.Log`. The provider logger carries standard context variables (provider, repository, event-id) that `run.Clients.Log` lacks. This applies to all providers: GitHub, GitLab, Bitbucket, Bitbucket Data Center, and Gitea.

## Code Quality Workflow

### After Editing Code

**Go files**: `make fumpt` to format
**Python files**: `make fix-python-errors`
**Markdown files**: `make fix-markdownlint && make fix-trailing-spaces`

### Before Committing

1. `make test` - Run test suite
2. `make lint` - Check code quality
3. `make check` - Combined lint + test

**Pre-commit hooks**: Install with `pre-commit install`

- Runs on push (skip with `git push --no-verify`)
- Linters: golangci-lint, yamllint, markdownlint, ruff, shellcheck, vale, codespell
- Skip specific hook: `SKIP=hook-name git push`

## Testing

### Test Structure

- **Use table-driven tests**: Anonymous struct pattern with `tests := []struct{...}{...}`
- **No underscores in test names**: Use PascalCase like `TestGetTektonDir`, not `TestGetTektonDir_Something`
- **Subtest naming**: Use descriptive `name` field for `t.Run(tt.name, ...)`
- **Complex setup**: Add `setup func(t *testing.T, ...)` field to test struct instead of creating separate test functions
- **Example**:

  ```go
  func TestGetTektonDir(t *testing.T) {
      tests := []struct {
          name  string
          event *info.Event
          setup func(t *testing.T, mux *http.ServeMux)
      }{
          {
              name: "simple case",
              event: &info.Event{...},
              setup: func(t *testing.T, mux *http.ServeMux) {
                  t.Helper()
                  mux.HandleFunc("/repos/...", ...)
              },
          },
      }
      for _, tt := range tests {
          t.Run(tt.name, func(t *testing.T) { ... })
      }
  }
  ```

### Assertions

- **Use `gotest.tools/v3/assert`** (never testify or pkg/assert)
  - `assert.NilError(t, err)` - verify no error
  - `assert.Assert(t, condition, msg)` - general assertions
  - `assert.Equal(t, expected, actual)` - equality checks
  - `assert.ErrorContains(t, err, substring)` - error validation

### Test Types

- **Unit tests**: Fast, focused, use mocks from `pkg/test/github`
- **E2E tests**: Require specific setup - always ask user to run and copy output
- **Gitea tests**: Most comprehensive and self-contained

### Commands

- `make test` - Run test suite
- `make test-no-cache` - Run without cache
- `make html-coverage` - Generate coverage report

## Dependencies

- Add: `go get -u dependency`
- **Always run after**: `make vendor` (required!)
- Update Go version: `go mod tidy -go=1.20`
- Remove unnecessary `replace` clauses in go.mod

## Documentation

- Preview: `make dev-docs` (<http://localhost:1313>)
- Hugo with hugo-book theme
- Custom shortcodes: `tech_preview`, `support_matrix`

## Useful Commands

- `make help` - Show all make targets
- `make all` - Build, test, lint
- `make clean` - Clean artifacts
- `make fix-linters` - Auto-fix most linting issues

## Skills

For complex workflows, use these repo-local skills:

- **Commit messages**: Conventional commits with Jira integration, line length validation, required footers. Trigger: "create commit", "commit changes", "generate commit message"
- **Jira tickets**: SRVKP story and bug templates with Jira markdown formatting. Trigger: "create Jira story", "create Jira bug", "SRVKP ticket"
