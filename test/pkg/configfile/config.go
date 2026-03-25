package configfile

import (
	"fmt"
	"os"
	"reflect"

	"sigs.k8s.io/yaml"
)

type E2EConfig struct {
	Common           CommonConfig           `json:"common"            yaml:"common"`
	GitHub           GitHubConfig           `json:"github"            yaml:"github"`
	GitHubEnterprise GitHubEnterpriseConfig `json:"github_enterprise" yaml:"github_enterprise"`
	GitLab           GitLabConfig           `json:"gitlab"            yaml:"gitlab"`
	Gitea            GiteaConfig            `json:"gitea"             yaml:"gitea"`
	BitbucketCloud   BitbucketCloudConfig   `json:"bitbucket_cloud"   yaml:"bitbucket_cloud"`
	BitbucketServer  BitbucketServerConfig  `json:"bitbucket_server"  yaml:"bitbucket_server"`
}

type CommonConfig struct {
	ControllerURL string `env:"TEST_EL_URL"            json:"controller_url" yaml:"controller_url"`
	WebhookSecret string `env:"TEST_EL_WEBHOOK_SECRET" json:"webhook_secret" yaml:"webhook_secret"`
	NoCleanup     string `env:"TEST_NOCLEANUP"         json:"no_cleanup"     yaml:"no_cleanup"`
}

type GitHubConfig struct {
	APIURL             string `env:"TEST_GITHUB_API_URL"              json:"api_url"              yaml:"api_url"`
	Token              string `env:"TEST_GITHUB_TOKEN"                json:"token"                yaml:"token"`
	RepoOwnerGithubApp string `env:"TEST_GITHUB_REPO_OWNER_GITHUBAPP" json:"repo_owner_githubapp" yaml:"repo_owner_githubapp"`
	RepoOwnerWebhook   string `env:"TEST_GITHUB_REPO_OWNER_WEBHOOK"   json:"repo_owner_webhook"   yaml:"repo_owner_webhook"`
	RepoInstallationID string `env:"TEST_GITHUB_REPO_INSTALLATION_ID" json:"repo_installation_id" yaml:"repo_installation_id"`
	PrivateTaskName    string `env:"TEST_GITHUB_PRIVATE_TASK_NAME"    json:"private_task_name"    yaml:"private_task_name"`
	PrivateTaskURL     string `env:"TEST_GITHUB_PRIVATE_TASK_URL"     json:"private_task_url"     yaml:"private_task_url"`
}

type GitHubEnterpriseConfig struct {
	APIURL             string `env:"TEST_GITHUB_SECOND_API_URL"              json:"api_url"              yaml:"api_url"`
	Token              string `env:"TEST_GITHUB_SECOND_TOKEN"                json:"token"                yaml:"token"`
	ControllerURL      string `env:"TEST_GITHUB_SECOND_EL_URL"               json:"controller_url"       yaml:"controller_url"`
	RepoOwnerGithubApp string `env:"TEST_GITHUB_SECOND_REPO_OWNER_GITHUBAPP" json:"repo_owner_githubapp" yaml:"repo_owner_githubapp"`
	RepoInstallationID string `env:"TEST_GITHUB_SECOND_REPO_INSTALLATION_ID" json:"repo_installation_id" yaml:"repo_installation_id"`
	SmeeURL            string `env:"TEST_GITHUB_SECOND_SMEE_URL"             json:"smee_url"             yaml:"smee_url"`
	WebhookSmeeURL     string `env:"TEST_GITHUB_SECOND_WEBHOOK_SMEE_URL"     json:"webhook_smee_url"     yaml:"webhook_smee_url"`
	WebhookOrg         string `env:"TEST_GITHUB_SECOND_WEBHOOK_ORG"          json:"webhook_org"          yaml:"webhook_org"`
}

type GitLabConfig struct {
	APIURL      string `env:"TEST_GITLAB_API_URL"      json:"api_url"      yaml:"api_url"`
	Token       string `env:"TEST_GITLAB_TOKEN"        json:"token"        yaml:"token"`
	SecondToken string `env:"TEST_GITLAB_SECOND_TOKEN" json:"second_token" yaml:"second_token"`
	Group       string `env:"TEST_GITLAB_GROUP"        json:"group"        yaml:"group"`
	SecondGroup string `env:"TEST_GITLAB_SECOND_GROUP" json:"second_group" yaml:"second_group"`
	SmeeURL     string `env:"TEST_GITLAB_SMEEURL"      json:"smee_url"     yaml:"smee_url"`
}

type GiteaConfig struct {
	APIURL      string `env:"TEST_GITEA_API_URL"      json:"api_url"      yaml:"api_url"`
	InternalURL string `env:"TEST_GITEA_INTERNAL_URL" json:"internal_url" yaml:"internal_url"`
	Password    string `env:"TEST_GITEA_PASSWORD"     json:"password"     yaml:"password"`
	Username    string `env:"TEST_GITEA_USERNAME"     json:"username"     yaml:"username"`
	RepoOwner   string `env:"TEST_GITEA_REPO_OWNER"   json:"repo_owner"   yaml:"repo_owner"`
	SmeeURL     string `env:"TEST_GITEA_SMEEURL"      json:"smee_url"     yaml:"smee_url"`
}

type BitbucketCloudConfig struct {
	APIURL        string `env:"TEST_BITBUCKET_CLOUD_API_URL"        json:"api_url"        yaml:"api_url"`
	User          string `env:"TEST_BITBUCKET_CLOUD_USER"           json:"user"           yaml:"user"`
	Token         string `env:"TEST_BITBUCKET_CLOUD_TOKEN"          json:"token"          yaml:"token"`
	E2ERepository string `env:"TEST_BITBUCKET_CLOUD_E2E_REPOSITORY" json:"e2e_repository" yaml:"e2e_repository"`
}

type BitbucketServerConfig struct {
	APIURL        string `env:"TEST_BITBUCKET_SERVER_API_URL"        json:"api_url"        yaml:"api_url"`
	User          string `env:"TEST_BITBUCKET_SERVER_USER"           json:"user"           yaml:"user"`
	Token         string `env:"TEST_BITBUCKET_SERVER_TOKEN"          json:"token"          yaml:"token"`
	E2ERepository string `env:"TEST_BITBUCKET_SERVER_E2E_REPOSITORY" json:"e2e_repository" yaml:"e2e_repository"`
	WebhookSecret string `env:"TEST_BITBUCKET_SERVER_WEBHOOK_SECRET" json:"webhook_secret" yaml:"webhook_secret"`
}

// LoadConfig reads a YAML config file and sets environment variables for any
// field that has an `env` struct tag. Existing environment variables take
// precedence over values from the config file.
func LoadConfig(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading config file %s: %w", path, err)
	}

	var cfg E2EConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("parsing config file %s: %w", path, err)
	}

	setEnvsFromStruct(reflect.ValueOf(cfg))
	return nil
}

// setEnvsFromStruct walks all fields of a struct (recursing into nested
// structs) and calls os.Setenv for each field that has an `env` tag, but only
// when the environment variable is not already set and the YAML value is
// non-empty.
func setEnvsFromStruct(v reflect.Value) {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		value := v.Field(i)

		if field.Type.Kind() == reflect.Struct {
			setEnvsFromStruct(value)
			continue
		}

		envKey := field.Tag.Get("env")
		if envKey == "" {
			continue
		}

		yamlVal := value.String()
		if yamlVal == "" {
			continue
		}

		if _, exists := os.LookupEnv(envKey); exists {
			continue
		}

		os.Setenv(envKey, yamlVal)
	}
}
