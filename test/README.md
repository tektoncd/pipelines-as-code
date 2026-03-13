# E2E Tests

The E2E Tests make sure we have the repositories getting updated if we have a repo with PAC installed on it.

It will checks if the repository have been updated at the end.

Most E2E tests has this basic flow :

- Create a temporary Namespace
- Create a Repository CR into it
- Create a Branch on a GitHUB repo
- Create a commit with files like pipelinerun inside that branch, the pipelinerun with have the namespace annotation to
  force the repository match on the namespace we have created and not catching other CR that may matching it.
- Wait that the Repository is updated.
- Some other stuff are done directly on the controller sink, bypassing a bit the GitHUB apis and generating the
  webhook ourselves.

## Settings

here are all the variables that is used by the E2E tests.

- `TEST_GITHUB_API_URL` -- GitHub Api URL, needs to be set i.e: `api.github.com`
- `TEST_GITHUB_TOKEN` -- Github token used to talk to the api url
- `TEST_GITHUB_REPO_OWNER` - The repo and owner (i.e: organization/repo)
- `TEST_GITHUB_REPO_OWNER_GITHUBAPP` - A repository/owner github repo that is configured with github apps.
- `TEST_GITHUB_REPO_INSTALLATION_ID` - The installation id when you have installed the repo on the app. (get it from the
  webhook event on the console)

**Hint:** Go to [Github Apps](https://github.com/settings/apps) (or *Settings > Developer settings > GitHub Apps*) choose the Github App and go to *Advanced > Recent Deliveries*
and search for **installation** which looks something like below

  ```yaml
    "installation": {
      "id": 29494069,
      "node_id": "MDIzOkludGVncmF0aW9uSW5zdGFsbGF0aW9uMjk0OTQwNjk="
    }
  ```

- `TEST_EL_URL` - The controller public url, ingress or openshfit's route
- `TEST_EL_WEBHOOK_SECRET` - The webhook secret.
- `TEST_GITHUB_REPO_OWNER_WEBHOOK` - A repository/owner github repo that is configured with github webhooks and
this repo should differ from the one which is configured as part of `TEST_GITHUB_REPO_OWNER_GITHUBAPP` env.
- `TEST_BITBUCKET_CLOUD_API_URL` - Bitbucket Cloud Api URL: probably: `https://api.bitbucket.org/2.0`
- `TEST_BITBUCKET_CLOUD_USER` - Bitbucket Cloud Username (you can get from "Personal Bitbucket settings" in UI)
- `TEST_BITBUCKET_CLOUD_E2E_REPOSITORY` - Bitbucket Cloud repository (i.e. `project/repo`)
- `TEST_BITBUCKET_CLOUD_TOKEN` - Bitbucket Cloud token
- `TEST_GITLAB_API_URL` - Gitlab API URL i.e: `https://gitlab.com`
- `TEST_GITLAB_GROUP` - Gitlab group/namespace where test projects will be created and deleted
- `TEST_GITLAB_TOKEN` - Gitlab Token
- `TEST_GITLAB_SMEEURL` - Smee URL for forwarding GitLab webhooks to the controller
- `TEST_GITEA_API_URL` - URL where GITEA is running (i.e: [GITEA_HOST](http://localhost:3000))
- `TEST_GITEA_SMEEURL` - URL of smee
- `TEST_GITEA_PASSWORD` - set password as **pac**
- `TEST_GITEA_USERNAME` - set username as **pac**
- `TEST_GITEA_REPO_OWNER` - set repo owner as **pac/pac**
- `TEST_BITBUCKET_SERVER_USER` - Bitbucket Data Center Username
- `TEST_BITBUCKET_SERVER_TOKEN` - Bitbucket Data Center token
- `TEST_BITBUCKET_SERVER_E2E_REPOSITORY` - Bitbucket Data Center repository (i.e. `project/repo`)
- `TEST_BITBUCKET_SERVER_API_URL` - URL where your Bitbucket Data Center instance is running.
- `TEST_BITBUCKET_SERVER_WEBHOOK_SECRET` - Webhook secret

- `PAC_API_INSTRUMENTATION_DIR` - Optional. When set, E2E tests write per-test JSON reports of GitHub API calls parsed from controller logs to this directory. Useful for analyzing API usage and rate limits. Example: `export PAC_API_INSTRUMENTATION_DIR=/tmp/api-instrumentation`.

You don't need to configure all of those if you restrict running your e2e tests to a subset.

### YAML Configuration File

Instead of setting individual environment variables, you can use a YAML
configuration file. Set `PAC_E2E_CONFIG` to the path of your config file:

```shell
PAC_E2E_CONFIG=./test/e2e-config.yaml make test-e2e
```

Copy the example file and fill in the values for the providers you want to test:

```shell
cp test/e2e-config.yaml.example test/e2e-config.yaml
# edit test/e2e-config.yaml with your values
```

The YAML file groups settings by provider section (`common`, `github`,
`github_enterprise`, `gitlab`, `gitea`, `bitbucket_cloud`,
`bitbucket_server`). See `test/e2e-config.yaml.example` for the full list of
fields.

Environment variables always take precedence over YAML values, so you can use
the config file for base settings and override specific values via env vars
(useful for CI secrets).

## Running

As long you have env variables set, you can just do a :

`make test-e2e`

and it will run the test-suite and cleans after itself,

You can specify only a subsets of test to run with :

```shell
% cd test/; go test -tags=e2e -v -run TestGithub .
```

same goes for `TestGitlab` or other methods.

### Running GitLab tests manually

GitLab tests require a smee URL to forward webhooks from the external GitLab
instance to your local controller (the same pattern as Gitea tests).

1. Create your own group on the GitLab instance
   (e.g. `https://gitlab.pipelinesascode.com`) to hold the temporary test
   projects. Each test run creates a project inside this group and deletes it
   on cleanup. Use `TEST_GITLAB_GROUP` to point to your group.

2. Generate a smee channel and start the gosmee client to forward webhooks to
   your controller:

   ```shell
   # Generate a new smee channel URL
   SMEE_URL=$(curl -s https://hook.pipelinesascode.com -o /dev/null -w '%{redirect_url}')

   # Start forwarding webhooks to your controller
   gosmee client "${SMEE_URL}" "https://your-controller-url"
   ```

3. Set the required environment variables (or use a
   [YAML config file](#yaml-configuration-file)):

   ```shell
   export TEST_GITLAB_API_URL=https://gitlab.pipelinesascode.com
   export TEST_GITLAB_TOKEN=<your-token>
   export TEST_GITLAB_GROUP=<your-group>
   export TEST_GITLAB_SMEEURL="${SMEE_URL}"
   export TEST_EL_URL=https://your-controller-url
   export TEST_EL_WEBHOOK_SECRET=<your-webhook-secret>
   ```

4. Run the tests:

   ```shell
   cd test/; go test -tags=e2e -v -run TestGitlab .
   ```

To clean up stale test projects (older than 7 days) left from previous runs:

```shell
./hack/cleanup-gitlab-projects.py        # dry-run
./hack/cleanup-gitlab-projects.py --force # actually delete
```

If you need to update the golden files in the end-to-end test, add the `-update` flag to the [go test](https://pkg.go.dev/cmd/go#hdr-Test_packages) command to refresh those files. First, run it if you expect the test output to change (or for a new test), then run it again without the flag to ensure everything is correct.

## Continuous Integration (CI) with GitHub Actions

Our E2E tests are automatically run as part of our CI pipeline using GitHub Actions. This section explains how the CI process works, the components involved, and how to troubleshoot or extend it.

### Overview

The E2E test CI pipeline runs on GitHub Actions using a Kind (Kubernetes in Docker) cluster. The workflow is defined in `.github/workflows/kind-e2e-tests.yaml` and uses helper scripts in `hack/gh-workflow-ci.sh`. Tests are executed against multiple provider categories to validate functionality across different Git providers.

The CI flow generally follows these steps:

1. Set up a Kind cluster
2. Install Pipelines as Code (PAC)
3. Configure necessary secrets
4. Run tests against different provider groups
5. Collect and store logs

### When Tests Run

Tests run on:

- Every Pull Request (PR) that modifies Go files
- As a nightly job at 05:00 UTC to detect regressions
- Manually via workflow dispatch (with optional debug capabilities)

### Test Categories

The tests are separated into provider categories (matrix strategy):

- `github_public` - Public GitHub tests (excluding GHE and concurrency)
- `github_ghe` - GitHub Enterprise (GHE) tests
- `gitlab_bitbucket` - GitLab and Bitbucket tests
- `gitea_1`, `gitea_2`, `gitea_3` - Gitea tests (split into 3 chunks)
- `concurrency` - Concurrency-specific tests

This split helps reduce the load on external APIs during testing and provides more focused test results.

### Environment Variables

Tests rely heavily on environment variables for configuration. These are set at the job level in the workflow file and supplemented with secrets where needed. Some key variables include:

- Basic configuration: `KO_DOCKER_REPO`, `CONTROLLER_DOMAIN_URL`, etc.
- Provider-specific endpoints and credentials
- Test repository information
- Webhook configurations

Secrets are stored in GitHub Secrets and made available to the workflow via `${{ secrets.SECRET_NAME }}`.

### Helper Script

The `hack/gh-workflow-ci.sh` script contains several functions that assist in the CI process:

1. `create_pac_github_app_secret` - Creates the required secrets for GitHub app authentication
2. ~~`create_second_github_app_controller_on_ghe`~~ - Use [startpaac](https://github.com/openshift-pipelines/startpaac) instead. See [Second Controller Setup](#second-controller-setup) below.
3. `run_e2e_tests` - Executes the E2E tests with proper filters
4. `collect_logs` - Gathers logs and diagnostic information

The script filters tests by category using pattern matching on test function names.

#### Second Controller Setup

In CI, use [startpaac](https://github.com/openshift-pipelines/startpaac) to install the second GitHub controller (GHE). When running with the `--ci` flag, startpaac automatically installs the second controller when `PAC_SECOND_SECRET_FOLDER` is set.

Example from e2e.yaml workflow:

```yaml
- name: Start installing cluster with startpaac
  env:
    PAC_SECOND_SECRET_FOLDER: ~/secrets-second
  run: |
    mkdir -p ~/secrets-second
    echo "${{ vars.TEST_GITHUB_SECOND_APPLICATION_ID }}" > ~/secrets-second/github-application-id
    echo "${{ secrets.TEST_GITHUB_SECOND_PRIVATE_KEY }}" > ~/secrets-second/github-private-key
    # ... other secrets ...

    cd startpaac
    ./startpaac --ci -a  # Automatically installs second controller
```

For manual setup or non-CI environments, see the [Second Controller documentation](https://pipelinesascode.com/docs/install/second_controller/).

> [!NOTE]
> For details on how API call metrics are generated and archived as artifacts, see [API Instrumentation (optional)](#api-instrumentation-optional).

### API Instrumentation (optional)

To help debug and analyze GitHub API usage during E2E runs, tests can emit
structured JSON reports of API calls when the environment variable
`PAC_API_INSTRUMENTATION_DIR` is set.

> [!NOTE]
> Currently supported only for GitHub (both GitHub App and GitHub webhook flows). Support for other providers is planned.

- Set `PAC_API_INSTRUMENTATION_DIR` to a writable path before running tests,
for example:
  - `export PAC_API_INSTRUMENTATION_DIR=/tmp/api-instrumentation`
- Each test produces a file named like `YYYY-MM-DDTHH-MM-SS_<test_name>.json` containing summary fields and an array of API calls (operation, duration_ms, url_path, status_code, rate_limit_remaining, provider, repo).
- In CI, this variable defaults to `/tmp/api-instrumentation` and `hack/gh-workflow-ci.sh collect_logs` copies the directory into the uploaded artifacts.

Log source details:

- Parses controller pod logs by resolving the controller label and container name dynamically.
- Tries known controller labels first (for example `app.kubernetes.io/name=controller` or `ghe-controller`) and then falls back to the standard PAC controller labels.
- Considers only log lines after the last occurrence of `github-app: initialized OAuth2 client`.
- Matches lines containing `GitHub API call completed` and extracts the embedded JSON payload.

Sample output:

```json
{
  "test_name": "TestGithubAppSimple",
  "timestamp": "2025-08-05T16:12:20Z",
  "controller": "controller",
  "pr_number": 123,
  "sha": "abcdef1",
  "target_namespace": "pac-e2e-ns-xyz12",
  "total_calls": 2,
  "oauth2_marker_line": 42,
  "github_api_calls": [
    {
      "operation": "get_commit",
      "duration_ms": 156,
      "url_path": "/api/v3/repos/org/repo/git/commits/62a0...",
      "rate_limit_remaining": "",
      "status_code": 200,
      "provider": "github",
      "repo": "org/repo"
    }
  ]
}
```

### Test Execution Flow

1. **Setup**:
   - Checkout code
   - Setup Go, ko, and gosmee client
   - Start Kind cluster
   - Install PAC controller

2. **Configuration**:
   - Create GitHub App secrets
   - Configure GHE environment (if needed)
   - Setup test environment variables

3. **Test Execution**:
   - Run selected tests against the specified provider category
   - For nightly runs, additional tests are included

4. **Artifacts Collection**:
   - Collect logs regardless of test outcome
   - Detect any panic in the controller logs
   - Upload artifacts to GitHub Actions
   - If `PAC_API_INSTRUMENTATION_DIR` is set, include API instrumentation reports (see [API Instrumentation (optional)](#api-instrumentation-optional))

### Debugging CI

If a test fails in CI, you can:

1. Examine the workflow logs in GitHub Actions
2. Download the artifacts (logs) for detailed investigation
3. Use the "debug_enabled" option when manually triggering the workflow to get a tmate session

For local debugging, you can:

1. Set the same environment variables locally
2. Run `make test-e2e` with specific test filters

### LLM E2E Tests

The LLM E2E tests uses a fake AI called `nonoai` to reply to the e2e tests and make them reliable (and cheap).

Deploy it with ko with `./pkg/test/nonoai/deployment.yaml`

Responses and fake are included in this json file `./pkg/test/nonoai/responses.json`

See an example of an E2E Test using it in
[./gitea_llm_test.go](./gitea_llm_test.go)

### Notifications

Failed nightly runs trigger Slack notifications to alert the team of potential regressions.

### Extending the CI

To add new provider tests:

1. Create test files following the existing patterns
2. Ensure proper naming convention to match the test category filters
3. Update environment variables if needed

For infrastructure changes:

1. Modify the `kind-e2e-tests.yaml` workflow file
2. Update the `gh-workflow-ci.sh` helper script as needed
3. Test changes using workflow dispatch before merging

### Performance Considerations

1. Tests are configured with concurrency limits to prevent overlapping runs
2. The matrix strategy allows parallelization across provider categories
3. Rate limiting is managed by separating frequently-run tests from nightly tests
