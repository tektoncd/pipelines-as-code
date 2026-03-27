---
title: "Testing"
weight: 4
---

This page covers how to write and run tests for Pipelines-as-Code (PAC), including unit tests, end-to-end tests, and testing best practices.

## Testing Philosophy

All code contributions must include appropriate tests. The project maintains high test coverage to ensure reliability across multiple Git providers and Kubernetes environments.

### Test Requirements

- **Use `gotest.tools/v3`**: Never use testify for assertions
- **Test all paths**: Cover both success and error scenarios
- **Test provider variations**: Ensure features work across GitHub, GitLab, Bitbucket, and Forgejo
- **Keep tests fast**: Unit tests should run quickly; use mocks for external dependencies
- **Update golden files**: When changing output formats, regenerate golden files

## Test Types

PAC has three main types of tests:

### Unit Tests

Test individual functions and packages in isolation.
**Location**: Alongside the code in `pkg/` directories
**Run unit tests**:

```bash
make test
```

**Run without cache** (force re-run all tests):

```bash
make test-no-cache
```

### E2E Tests

End-to-end tests that validate complete workflows against real Git providers.
**Location**: `test/` directory
**Run E2E tests**:

```bash
make test-e2e
```

E2E tests require specific setup with Git provider credentials. Always ask the user to run E2E tests and provide output rather than running them yourself.

### Gitea/Forgejo Tests

Self-contained E2E tests using a local Forgejo instance.
**Why Gitea/Forgejo?**

- Most comprehensive test suite
- Self-contained (no external dependencies)
- Easier to debug than cloud provider tests
- Perfect for local development

## Writing Unit Tests

### Basic Test Structure

Use the following template when writing unit tests:

```go
package mypackage

import (
    "testing"
    "gotest.tools/v3/assert"
    "gotest.tools/v3/golden"
)

func TestMyFunction(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
        wantErr  bool
    }{
        {
            name:     "success case",
            input:    "test input",
            expected: "test output",
            wantErr:  false,
        },
        {
            name:    "error case",
            input:   "bad input",
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := MyFunction(tt.input)

            if tt.wantErr {
                assert.Assert(t, err != nil)
                return
            }

            assert.NilError(t, err)
            assert.Equal(t, result, tt.expected)
        })
    }
}
```

### Using gotest.tools/v3

#### Common Assertions

```go
import "gotest.tools/v3/assert"

// Check for nil errors
assert.NilError(t, err)

// Check error is not nil
assert.Assert(t, err != nil)

// Check equality
assert.Equal(t, actual, expected)

// Check deep equality (for structs, slices, maps)
assert.DeepEqual(t, actual, expected)

// Custom assertions
assert.Assert(t, len(results) > 0, "expected results to be non-empty")
assert.Assert(t, strings.Contains(output, "expected"))
```

#### Using Golden Files

Golden files store expected test output for comparison:

```go
import "gotest.tools/v3/golden"

func TestCLIOutput(t *testing.T) {
    output := runCLICommand()

    // Compare output with golden file
    golden.Assert(t, output, "testdata/expected-output.golden")
}
```

**Update golden files** when you intentionally change output:

```bash
make update-golden
```

## Running Tests

### Run All Unit Tests

```bash
# Run with default flags (-race -failfast)
make test

# Run without test cache
make test-no-cache
```

### Run Specific Packages

```bash
# Run tests for a specific package
go test ./pkg/matcher/...

# Run tests matching a pattern
go test ./pkg/... -run TestMyFunction

# Run with verbose output
go test -v ./pkg/provider/github/...
```

### CI Test Reporting

PAC uploads raw `go test -json` output to testrr from both Tekton and GitHub
Actions.

Tekton CI uses these fixed values in the pipeline definitions:

- URL: `https://testrr.pipelinesascode.com`
- Project: `pipelinesascode`
- Username: `pac`

Only the password is stored in the cluster secret. Create it with:

```bash
kubectl -n <namespace> create secret generic testrr-secret \
  --from-literal=password='YOUR_PASSWORD'
```

GitHub Actions E2E uploads use the same URL, project, and username. Only the
`TESTRR_PASSWORD` repository secret must be configured in GitHub.

Upload failures are non-fatal in both systems. Test execution still determines
the final CI status.

### Test Timeout

The default timeout for unit tests is **20 minutes**. For E2E tests, it’s **45 minutes**.

```bash
# Run with custom timeout
go test -timeout 10m ./pkg/...
```

### Test Coverage

1

Generate coverage report

```bash
make html-coverage
```

This creates an HTML report at `tmp/c.out` and opens it in your browser.

2

View coverage for specific packages

```bash
go test -coverprofile=coverage.out ./pkg/matcher/...
go tool cover -html=coverage.out
```

## E2E Testing

### Prerequisites

E2E tests require:

- A running Kubernetes cluster (kind, minikube, etc.)
- PAC installed on the cluster
- Git provider credentials set as environment variables

### Forgejo E2E Tests (Recommended)

Forgejo tests are the easiest to run locally because they’re self-contained.

1

Set up Forgejo with startpaac

```bash
cd startpaac
./startpaac -f  # Install Forgejo
```

Default Forgejo settings:

- URL: <https://localhost:3000/>
- Admin Username: `pac`
- Admin Password: `pac`

2

Create a webhook forwarding URL

Generate a hook URL at <https://hook.pipelinesascode.com/new>

```bash
export TEST_GITEA_SMEEURL="https://hook.pipelinesascode.com/YOUR_ID"
```

3

Run the Forgejo tests

```bash
make test-e2e
```

### Provider-Specific E2E Tests

For GitHub, GitLab, and Bitbucket tests, you need to set up provider-specific environment variables. See the [E2E on kind workflow](https://github.com/tektoncd/pipelines-as-code/blob/main/.github/workflows/kind-e2e-tests.yaml) for the complete list.

### Debugging E2E Tests

#### Keep Test Resources

By default, E2E tests clean up after themselves. To keep the test namespace and resources:

```bash
export TEST_NOCLEANUP=true
make test-e2e
```

#### Watch Test Execution

```bash
# Watch PipelineRuns
watch kubectl get pipelineruns -A

# Follow controller logs
kubectl logs -n pipelines-as-code -l app.kubernetes.io/name=controller -f

# Check test namespace
kubectl get all -n <test-namespace>
```

#### Replay Webhook Events

Save webhook events for debugging:

```bash
gosmee client --saveDir /tmp/webhooks https://hook.pipelinesascode.com/YOUR_ID http://localhost:8080
```

Replayed events are saved as shell scripts in `/tmp/webhooks`.

## Test Naming Conventions

PAC enforces E2E test naming conventions to maintain consistency.

### Check Test Naming

```bash
make lint-e2e-naming
```

This verifies that E2E test names follow the project’s conventions.

### Test Naming Rules

- Use descriptive names that explain what’s being tested
- Include the provider if provider-specific (e.g., `TestGitHubPullRequest`)
- Use table-driven tests with named test cases
- Avoid generic names like `TestRun` or `TestProcess`

## Golden Files

Golden files store expected output for comparison in tests.

### When to Use Golden Files

- CLI command output
- Generated YAML/JSON
- Formatted text output
- Complex struct comparisons

### Updating Golden Files

1

Make your code changes

Modify the code that affects test output.

2

Run the update command

```bash
make update-golden
```

This regenerates all golden files with new output.

3

Review the changes

```bash
git diff testdata/
```

Verify that the changes are intentional and correct.

4

Commit the updated golden files

```bash
git add testdata/
git commit -m "test: update golden files for new output format"
```

### E2E Golden Files

For E2E test golden files, see [test/README.md](https://github.com/tektoncd/pipelines-as-code/blob/main/test/README.md).

## Mocking

### When to Mock

- External API calls (Git providers, Kubernetes API)
- File system operations
- Time-dependent behavior
- Network requests

### Example Mock

```go
type mockGitProvider struct {
    createCommentFunc func(string, string, int, string) error
}

func (m *mockGitProvider) CreateComment(owner, repo string, number int, comment string) error {
    if m.createCommentFunc != nil {
        return m.createCommentFunc(owner, repo, number, comment)
    }
    return nil
}

func TestWithMock(t *testing.T) {
    mock := &mockGitProvider{
        createCommentFunc: func(owner, repo string, number int, comment string) error {
            assert.Equal(t, owner, "tektoncd")
            assert.Equal(t, repo, "pipelines-as-code")
            return nil
        },
    }

    // Use mock in your test
    err := someFunction(mock)
    assert.NilError(t, err)
}
```

## CI Integration

Tests run automatically in CI for:

- Every pull request
- Every push to `main`
- Release tags

### CI Test Jobs

- **Unit tests**: Run on every PR
- **Lint checks**: Run on every PR
- **E2E tests**: Run on PRs (GitHub, GitLab, Bitbucket, Forgejo)
- **Integration tests**: Run on specific workflows

### Pre-merge Requirements

All tests must pass before a PR can be merged. This includes:

- Unit tests
- Linting checks
- E2E tests for all supported providers

## Test Cleanup

### Cleanup E2E Test Namespaces

If E2E tests leave behind namespaces or resources:

```bash
make test-e2e-cleanup
```

This removes leftover test namespaces and resources.

## Common Testing Patterns

### Table-Driven Tests

```go
func TestMyFunction(t *testing.T) {
    tests := []struct {
        name     string
        input    Input
        expected Output
        wantErr  bool
    }{
        {
            name:     "basic case",
            input:    Input{Value: "test"},
            expected: Output{Result: "TEST"},
            wantErr:  false,
        },
        {
            name:    "error case",
            input:   Input{Value: ""},
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := MyFunction(tt.input)
            if tt.wantErr {
                assert.Assert(t, err != nil)
                return
            }
            assert.NilError(t, err)
            assert.DeepEqual(t, result, tt.expected)
        })
    }
}
```

### Testing Error Cases

```go
func TestErrorHandling(t *testing.T) {
    _, err := FunctionThatFails()

    // Check error is not nil
    assert.Assert(t, err != nil)

    // Check error message
    assert.ErrorContains(t, err, "expected error text")

    // Check error type
    assert.Assert(t, errors.Is(err, ErrExpectedType))
}
```

### Testing Kubernetes Resources

```go
import (
    "gotest.tools/v3/assert"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPipelineRunCreation(t *testing.T) {
    pr := createPipelineRun()

    assert.Equal(t, pr.Name, "test-pipeline-run")
    assert.Equal(t, pr.Namespace, "test-namespace")
    assert.Assert(t, pr.Annotations != nil)
    assert.Equal(t, pr.Annotations["pipelinesascode.tekton.dev/on-event"], "[pull_request]")
}
```

## Troubleshooting Tests

### Tests Failing Locally but Passing in CI

**Possible causes**:

- Outdated dependencies: Run `make vendor`
- Stale test cache: Run `make test-no-cache`
- Different Go version: Check `go version` matches CI

### Golden File Mismatches

**Problem**: Tests fail with golden file differences
**Solution**:

1. Review the diff to ensure changes are intentional
2. Run `make update-golden`
3. Commit the updated golden files

### E2E Tests Timing Out

**Possible causes**:

- Cluster resources exhausted
- Network connectivity issues
- Webhook forwarding not working

**Solutions**:

- Check cluster resources: `kubectl top nodes`
- Verify webhook forwarding with gosmee
- Increase timeout: `go test -timeout 60m`

## Best Practices

- Write tests as you write code (TDD approach)
- Keep tests isolated and independent
- Use descriptive test names
- Test both success and failure paths
- Mock external dependencies
- Keep tests fast (< 1 second for unit tests)
- Update golden files when changing output formats
- Clean up resources in E2E tests

## Next Steps

{{< cards >}}
  {{< card link="../architecture" title="Architecture" subtitle="Understand the PAC architecture" >}}
  {{< card link="../flows-diagram" title="Event Flows" subtitle="See how events flow through the system" >}}
  {{< card link="../setup" title="Development Setup" subtitle="Set up your development environment" >}}
  {{< card link="../" title="Contributing" subtitle="Learn about contributing to PAC" >}}
{{< /cards >}}
