package configfile

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"
)

func TestLoadConfigValid(t *testing.T) {
	content := `
common:
  controller_url: "https://controller.example.com"
  webhook_secret: "my-secret"
github:
  api_url: "https://api.github.com"
  token: "ghp_test123"
gitea:
  api_url: "http://localhost:3000"
  password: "pac"
  username: "pac"
  repo_owner: "pac/pac"
  smee_url: "https://smee.io/test"
`
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	assert.NilError(t, os.WriteFile(configPath, []byte(content), 0o600))

	// Clear any pre-existing env vars that could interfere
	envVars := []string{
		"TEST_EL_URL", "TEST_EL_WEBHOOK_SECRET",
		"TEST_GITHUB_API_URL", "TEST_GITHUB_TOKEN",
		"TEST_GITEA_API_URL", "TEST_GITEA_PASSWORD",
		"TEST_GITEA_USERNAME", "TEST_GITEA_REPO_OWNER",
		"TEST_GITEA_SMEEURL",
	}
	for _, env := range envVars {
		t.Setenv(env, "")
		os.Unsetenv(env)
	}

	assert.NilError(t, LoadConfig(configPath))

	assert.Equal(t, os.Getenv("TEST_EL_URL"), "https://controller.example.com")
	assert.Equal(t, os.Getenv("TEST_EL_WEBHOOK_SECRET"), "my-secret")
	assert.Equal(t, os.Getenv("TEST_GITHUB_API_URL"), "https://api.github.com")
	assert.Equal(t, os.Getenv("TEST_GITHUB_TOKEN"), "ghp_test123")
	assert.Equal(t, os.Getenv("TEST_GITEA_API_URL"), "http://localhost:3000")
	assert.Equal(t, os.Getenv("TEST_GITEA_PASSWORD"), "pac")
	assert.Equal(t, os.Getenv("TEST_GITEA_USERNAME"), "pac")
	assert.Equal(t, os.Getenv("TEST_GITEA_REPO_OWNER"), "pac/pac")
	assert.Equal(t, os.Getenv("TEST_GITEA_SMEEURL"), "https://smee.io/test")
}

func TestLoadConfigEnvVarOverride(t *testing.T) {
	content := `
github:
  api_url: "https://from-yaml.example.com"
  token: "yaml-token"
`
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	assert.NilError(t, os.WriteFile(configPath, []byte(content), 0o600))

	t.Setenv("TEST_GITHUB_API_URL", "https://from-env.example.com")
	os.Unsetenv("TEST_GITHUB_TOKEN")

	assert.NilError(t, LoadConfig(configPath))

	// Env var should win over YAML
	assert.Equal(t, os.Getenv("TEST_GITHUB_API_URL"), "https://from-env.example.com")
	// YAML value should be used when env var is not set
	assert.Equal(t, os.Getenv("TEST_GITHUB_TOKEN"), "yaml-token")
}

func TestLoadConfigMissingFile(t *testing.T) {
	err := LoadConfig("/nonexistent/path/config.yaml")
	assert.ErrorContains(t, err, "reading config file")
}

func TestLoadConfigPartial(t *testing.T) {
	content := `
gitea:
  api_url: "http://localhost:3000"
  password: "pac"
`
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	assert.NilError(t, os.WriteFile(configPath, []byte(content), 0o600))

	envVars := []string{
		"TEST_GITEA_API_URL", "TEST_GITEA_PASSWORD",
		"TEST_GITHUB_API_URL", "TEST_EL_URL",
	}
	for _, env := range envVars {
		t.Setenv(env, "")
		os.Unsetenv(env)
	}

	assert.NilError(t, LoadConfig(configPath))

	assert.Equal(t, os.Getenv("TEST_GITEA_API_URL"), "http://localhost:3000")
	assert.Equal(t, os.Getenv("TEST_GITEA_PASSWORD"), "pac")
	// Unset fields should not produce env vars
	assert.Equal(t, os.Getenv("TEST_GITHUB_API_URL"), "")
	assert.Equal(t, os.Getenv("TEST_EL_URL"), "")
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	content := `{invalid yaml: [`
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	assert.NilError(t, os.WriteFile(configPath, []byte(content), 0o600))

	err := LoadConfig(configPath)
	assert.ErrorContains(t, err, "parsing config file")
}

func TestLoadConfigAllProviders(t *testing.T) {
	content := `
common:
  controller_url: "https://ctrl.example.com"
  webhook_secret: "secret123"
  no_cleanup: "true"
github:
  api_url: "https://api.github.com"
  token: "ghp_abc"
  repo_owner_githubapp: "org/repo"
  repo_owner_webhook: "org/repo-webhook"
  repo_installation_id: "12345"
  private_task_name: "task-remote"
  private_task_url: "https://github.com/org/repo/blob/main/task.yaml"
github_enterprise:
  api_url: "https://ghe.example.com/api/v3"
  token: "ghe-token"
  controller_url: "https://ghe-ctrl.example.com"
  repo_owner_githubapp: "ghe-org/repo"
  repo_installation_id: "1"
gitlab:
  api_url: "https://gitlab.com"
  token: "glpat-xyz"
  second_token: "glpat-second"
  group: "pac-e2e-tests"
  second_group: "pac-e2e-tests-second"
  smee_url: "https://hook.pipelinesascode.com/gitlab-test"
gitea:
  api_url: "http://localhost:3000"
  internal_url: "http://forgejo:3000"
  password: "pac"
  username: "pac"
  repo_owner: "pac/pac"
  smee_url: "https://smee.io/test"
bitbucket_cloud:
  api_url: "https://api.bitbucket.org/2.0"
  user: "bbuser"
  token: "bb-token"
  e2e_repository: "workspace/repo"
bitbucket_server:
  api_url: "https://bitbucket.example.com"
  user: "bbsuser"
  token: "bbs-token"
  e2e_repository: "project/repo"
  webhook_secret: "bbs-secret"
`
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	assert.NilError(t, os.WriteFile(configPath, []byte(content), 0o600))

	// Clear all env vars
	allEnvVars := []string{
		"TEST_EL_URL", "TEST_EL_WEBHOOK_SECRET", "TEST_NOCLEANUP",
		"TEST_GITHUB_API_URL", "TEST_GITHUB_TOKEN",
		"TEST_GITHUB_REPO_OWNER_GITHUBAPP", "TEST_GITHUB_REPO_OWNER_WEBHOOK",
		"TEST_GITHUB_REPO_INSTALLATION_ID",
		"TEST_GITHUB_PRIVATE_TASK_NAME", "TEST_GITHUB_PRIVATE_TASK_URL",
		"TEST_GITHUB_SECOND_API_URL", "TEST_GITHUB_SECOND_TOKEN",
		"TEST_GITHUB_SECOND_EL_URL", "TEST_GITHUB_SECOND_REPO_OWNER_GITHUBAPP",
		"TEST_GITHUB_SECOND_REPO_INSTALLATION_ID",
		"TEST_GITLAB_API_URL", "TEST_GITLAB_TOKEN", "TEST_GITLAB_SECOND_TOKEN",
		"TEST_GITLAB_GROUP", "TEST_GITLAB_SECOND_GROUP", "TEST_GITLAB_SMEEURL",
		"TEST_GITEA_API_URL", "TEST_GITEA_INTERNAL_URL", "TEST_GITEA_PASSWORD",
		"TEST_GITEA_USERNAME", "TEST_GITEA_REPO_OWNER", "TEST_GITEA_SMEEURL",
		"TEST_BITBUCKET_CLOUD_API_URL", "TEST_BITBUCKET_CLOUD_USER",
		"TEST_BITBUCKET_CLOUD_TOKEN", "TEST_BITBUCKET_CLOUD_E2E_REPOSITORY",
		"TEST_BITBUCKET_DATA_CENTER_API_URL", "TEST_BITBUCKET_DATA_CENTER_USER",
		"TEST_BITBUCKET_DATA_CENTER_TOKEN", "TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY",
		"TEST_BITBUCKET_DATA_CENTER_WEBHOOK_SECRET",
	}
	for _, env := range allEnvVars {
		t.Setenv(env, "")
		os.Unsetenv(env)
	}

	assert.NilError(t, LoadConfig(configPath))

	expected := map[string]string{
		"TEST_EL_URL":                               "https://ctrl.example.com",
		"TEST_EL_WEBHOOK_SECRET":                    "secret123",
		"TEST_NOCLEANUP":                            "true",
		"TEST_GITHUB_API_URL":                       "https://api.github.com",
		"TEST_GITHUB_TOKEN":                         "ghp_abc",
		"TEST_GITHUB_REPO_OWNER_GITHUBAPP":          "org/repo",
		"TEST_GITHUB_REPO_OWNER_WEBHOOK":            "org/repo-webhook",
		"TEST_GITHUB_REPO_INSTALLATION_ID":          "12345",
		"TEST_GITHUB_PRIVATE_TASK_NAME":             "task-remote",
		"TEST_GITHUB_PRIVATE_TASK_URL":              "https://github.com/org/repo/blob/main/task.yaml",
		"TEST_GITHUB_SECOND_API_URL":                "https://ghe.example.com/api/v3",
		"TEST_GITHUB_SECOND_TOKEN":                  "ghe-token",
		"TEST_GITHUB_SECOND_EL_URL":                 "https://ghe-ctrl.example.com",
		"TEST_GITHUB_SECOND_REPO_OWNER_GITHUBAPP":   "ghe-org/repo",
		"TEST_GITHUB_SECOND_REPO_INSTALLATION_ID":   "1",
		"TEST_GITLAB_API_URL":                       "https://gitlab.com",
		"TEST_GITLAB_TOKEN":                         "glpat-xyz",
		"TEST_GITLAB_SECOND_TOKEN":                  "glpat-second",
		"TEST_GITLAB_GROUP":                         "pac-e2e-tests",
		"TEST_GITLAB_SECOND_GROUP":                  "pac-e2e-tests-second",
		"TEST_GITLAB_SMEEURL":                       "https://hook.pipelinesascode.com/gitlab-test",
		"TEST_GITEA_API_URL":                        "http://localhost:3000",
		"TEST_GITEA_INTERNAL_URL":                   "http://forgejo:3000",
		"TEST_GITEA_PASSWORD":                       "pac",
		"TEST_GITEA_USERNAME":                       "pac",
		"TEST_GITEA_REPO_OWNER":                     "pac/pac",
		"TEST_GITEA_SMEEURL":                        "https://smee.io/test",
		"TEST_BITBUCKET_CLOUD_API_URL":              "https://api.bitbucket.org/2.0",
		"TEST_BITBUCKET_CLOUD_USER":                 "bbuser",
		"TEST_BITBUCKET_CLOUD_TOKEN":                "bb-token",
		"TEST_BITBUCKET_CLOUD_E2E_REPOSITORY":       "workspace/repo",
		"TEST_BITBUCKET_DATA_CENTER_API_URL":        "https://bitbucket.example.com",
		"TEST_BITBUCKET_DATA_CENTER_USER":           "bbsuser",
		"TEST_BITBUCKET_DATA_CENTER_TOKEN":          "bbs-token",
		"TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY": "project/repo",
		"TEST_BITBUCKET_DATA_CENTER_WEBHOOK_SECRET": "bbs-secret",
	}

	for envVar, expectedVal := range expected {
		assert.Equal(t, os.Getenv(envVar), expectedVal, "env var %s", envVar)
	}
}
