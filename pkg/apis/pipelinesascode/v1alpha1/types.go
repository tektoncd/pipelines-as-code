package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Repository is the representation of a Git repository from a Git provider platform.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=repo
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Succeeded",type=string,JSONPath=`.pipelinerun_status[-1].conditions[?(@.type=="Succeeded")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.pipelinerun_status[-1].conditions[?(@.type=="Succeeded")].reason`
// +kubebuilder:printcolumn:name="StartTime",type=date,JSONPath=`.pipelinerun_status[-1].startTime`
// +kubebuilder:printcolumn:name="CompletionTime",type=date,JSONPath=`.pipelinerun_status[-1].completionTime`
type Repository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata"`

	Spec   RepositorySpec        `json:"spec"`
	Status []RepositoryRunStatus `json:"pipelinerun_status,omitempty"`
}

type RepositoryRunStatus struct {
	duckv1.Status `json:",inline"`

	// PipelineRunName is the name of the PipelineRun
	// +optional
	PipelineRunName string `json:"pipelineRunName,omitempty"`

	// StartTime is the time the PipelineRun is actually started.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is the time the PipelineRun completed.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// SHA is the name of the SHA that has been tested
	// +optional
	SHA *string `json:"sha,omitempty"`

	// SHA the URL of the SHA to view it
	// +optional
	SHAURL *string `json:"sha_url,omitempty"`

	// Title is the title of the commit SHA that has been tested
	// +optional
	Title *string `json:"title,omitempty"`

	// LogURL is the full URL to the log for this run.
	// +optional
	LogURL *string `json:"logurl,omitempty"`

	// TargetBranch is the target branch of that run
	// +optional
	TargetBranch *string `json:"target_branch,omitempty"`

	// EventType is the event type of that run
	// +optional
	EventType *string `json:"event_type,omitempty"`

	// CollectedTaskInfos is the information about tasks
	CollectedTaskInfos *map[string]TaskInfos `json:"failure_reason,omitempty"`
}

// TaskInfos contains information about a task.
type TaskInfos struct {
	Name           string       `json:"name"`
	Message        string       `json:"message,omitempty"`
	LogSnippet     string       `json:"log_snippet,omitempty"`
	Reason         string       `json:"reason,omitempty"`
	DisplayName    string       `json:"display_name,omitempty"`
	CompletionTime *metav1.Time `json:"completion_time,omitempty"`
}

// RepositorySpec defines the desired state of a Repository, including its URL,
// Git provider configuration, and operational settings.
type RepositorySpec struct {
	// ConcurrencyLimit defines the maximum number of concurrent pipelineruns that can
	// run for this repository. This helps prevent resource exhaustion when many events trigger
	// pipelines simultaneously.
	// +optional
	// +kubebuilder:validation:Minimum=1
	ConcurrencyLimit *int `json:"concurrency_limit,omitempty"` // move it to settings in further version of the spec

	// URL of the repository we are building. Must be a valid HTTP/HTTPS Git repository URL
	// that PAC will use to clone and fetch pipeline definitions from.
	// +optional
	URL string `json:"url"`

	// GitProvider details specific to a git provider configuration. Contains authentication,
	// API endpoints, and provider type information needed to interact with the Git service.
	// +optional
	GitProvider *GitProvider `json:"git_provider,omitempty"`

	// Incomings defines incoming webhook configurations. Each configuration specifies how to
	// handle external webhook requests that don't come directly from the primary Git provider.
	// +optional
	Incomings *[]Incoming `json:"incoming,omitempty"`

	// Params defines repository level parameters that can be referenced in PipelineRuns.
	// These parameters can be used as default values or configured for specific events.
	// +optional
	Params *[]Params `json:"params,omitempty"`

	// Settings contains the configuration settings for the repository, including
	// authorization policies, provider-specific configuration, and provenance settings.
	// +optional
	Settings *Settings `json:"settings,omitempty"`
}

func (r *RepositorySpec) Merge(newRepo RepositorySpec) {
	if newRepo.ConcurrencyLimit != nil && r.ConcurrencyLimit == nil {
		r.ConcurrencyLimit = newRepo.ConcurrencyLimit
	}
	if newRepo.Settings != nil && r.Settings != nil {
		r.Settings.Merge(newRepo.Settings)
	}
	if r.GitProvider != nil && newRepo.GitProvider != nil {
		r.GitProvider.Merge(newRepo.GitProvider)
	}

	// TODO(chmouel): maybe let it merges those between the user Repo Incomings and Params with the global ones?
	// we need to gather feedback first with users to know what they want.
	if newRepo.Incomings != nil && r.Incomings == nil {
		r.Incomings = newRepo.Incomings
	}
	if newRepo.Params != nil && r.Params == nil {
		r.Params = newRepo.Params
	}
}

type Settings struct {
	// GithubAppTokenScopeRepos lists repositories that can access the GitHub App token when using the
	// GitHub App authentication method. This allows specific repositories to use tokens generated for
	// the GitHub App installation, useful for cross-repository access.
	// +optional
	GithubAppTokenScopeRepos []string `json:"github_app_token_scope_repos,omitempty"`

	// PipelineRunProvenance configures how PipelineRun definitions are fetched.
	// Options:
	// - 'source': Fetch definitions from the event source branch/SHA (default)
	// - 'default_branch': Fetch definitions from the repository default branch
	// +optional
	// +kubebuilder:validation:Enum=source;default_branch
	PipelineRunProvenance string `json:"pipelinerun_provenance,omitempty"`

	// GitOpsCommandPrefix configures the prefix for the GitOps command.
	// This is used to identify the GitOps command in the PipelineRun.
	// +optional
	GitOpsCommandPrefix string `json:"gitops_command_prefix,omitempty"`

	// Policy defines authorization policies for the repository, controlling who can
	// trigger PipelineRuns under different conditions.
	// +optional
	Policy *Policy `json:"policy,omitempty"`

	// Gitlab contains GitLab-specific settings for repositories hosted on GitLab.
	// +optional
	Gitlab *GitlabSettings `json:"gitlab,omitempty"`

	Github *GithubSettings `json:"github,omitempty"`

	// Forgejo contains Forgejo/Gitea-specific settings.
	// +optional
	Forgejo *ForgejoSettings `json:"forgejo,omitempty"`

	// AIAnalysis contains AI/LLM analysis configuration for automated CI/CD pipeline analysis.
	// +optional
	AIAnalysis *AIAnalysisConfig `json:"ai,omitempty"`
}

type GitlabSettings struct {
	// CommentStrategy defines how GitLab comments are handled for pipeline results.
	// Options:
	// - 'disable_all': Disables all comments on merge requests
	// - 'update': Updates a single comment per PipelineRun on every trigger.
	// +optional
	// +kubebuilder:validation:Enum="";disable_all;update
	CommentStrategy string `json:"comment_strategy,omitempty"`
}

type GithubSettings struct {
	// CommentStrategy defines how GitHub comments are handled for pipeline results.
	// Options:
	// - 'disable_all': Disables all comments on merge requests
	// - 'update': Updates a single comment per PipelineRun on every trigger.
	// +optional
	// +kubebuilder:validation:Enum="";disable_all;update
	CommentStrategy string `json:"comment_strategy,omitempty"`
}

type ForgejoSettings struct {
	// UserAgent sets the User-Agent header on API requests to the Gitea/Forgejo instance.
	// This is useful when the instance is behind an AI scraping protection proxy (e.g. Anubis)
	// that blocks requests without a recognized User-Agent.
	// Defaults to "pipelines-as-code/<version>" when left empty.
	// +optional
	UserAgent string `json:"user_agent,omitempty"`

	// CommentStrategy defines how Forgejo/Gitea comments are handled for pipeline results.
	// Options:
	// - 'disable_all': Disables all comments on pull requests
	// - 'update': Updates a single comment per PipelineRun on every trigger.
	// +optional
	// +kubebuilder:validation:Enum="";disable_all;update
	CommentStrategy string `json:"comment_strategy,omitempty"`
}

func (s *Settings) Merge(newSettings *Settings) {
	if newSettings.PipelineRunProvenance != "" && s.PipelineRunProvenance == "" {
		s.PipelineRunProvenance = newSettings.PipelineRunProvenance
	}
	if newSettings.Policy != nil && s.Policy == nil {
		s.Policy = newSettings.Policy
	}
	if newSettings.GithubAppTokenScopeRepos != nil && s.GithubAppTokenScopeRepos == nil {
		s.GithubAppTokenScopeRepos = newSettings.GithubAppTokenScopeRepos
	}
	if newSettings.Forgejo != nil && s.Forgejo == nil {
		s.Forgejo = newSettings.Forgejo
	}
	if newSettings.AIAnalysis != nil && s.AIAnalysis == nil {
		s.AIAnalysis = newSettings.AIAnalysis
	}
}

type Policy struct {
	// OkToTest defines a list of usernames that are allowed to trigger pipeline runs on pull requests
	// from external contributors by commenting "/ok-to-test" on the PR. These users are typically
	// repository maintainers or trusted contributors who can vouch for external contributions.
	// +optional
	OkToTest []string `json:"ok_to_test,omitempty"`

	// PullRequest defines a list of usernames that are explicitly allowed to execute
	// pipelines on their pull requests, even if they wouldn't normally have permission.
	// This is useful for allowing specific external contributors to trigger pipeline runs.
	// +optional
	PullRequest []string `json:"pull_request,omitempty"`
}

type Params struct {
	// Name of the parameter. This is the key that will be used to reference this parameter
	// in PipelineRun definitions through via the {{ name }} syntax.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Value of the parameter. The literal value to be provided to the PipelineRun.
	// This field is mutually exclusive with SecretRef.
	// +optional
	Value string `json:"value,omitempty"`

	// SecretRef references a secret for the parameter value. Use this when the parameter
	// contains sensitive information that should not be stored directly in the Repository CR.
	// This field is mutually exclusive with Value.
	// +optional
	SecretRef *Secret `json:"secret_ref,omitempty"`

	// Filter defines when this parameter applies. It can be used to conditionally
	// apply parameters based on the event type, branch name, or other attributes.
	// +optional
	Filter string `json:"filter,omitempty"`
}

type Incoming struct {
	// Type of the incoming webhook. Currently only 'webhook-url' is supported, which allows
	// external systems to trigger PipelineRuns via generic webhook requests.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=webhook-url
	Type string `json:"type"`

	// Secret for the incoming webhook authentication. This secret is used to validate
	// that webhook requests are coming from authorized sources.
	// +kubebuilder:validation:Required
	Secret Secret `json:"secret"`

	// Params defines parameter names to extract from the webhook payload. These parameters
	// will be made available to the PipelineRuns triggered by this webhook.
	// +optional
	Params []string `json:"params,omitempty"`

	// Targets defines target branches for this webhook. When specified, only webhook
	// events targeting these branches will trigger PipelineRuns.
	// +optional
	Targets []string `json:"targets,omitempty"`
}

type GitProvider struct {
	// URL of the git provider API endpoint. This is the base URL for API requests to the
	// Git provider (e.g., 'https://api.github.com' for GitHub or a custom GitLab instance URL).
	// +optional
	URL string `json:"url,omitempty"`

	// User of the git provider. Username to use for authentication when using basic auth
	// or token-based authentication methods. Not used for GitHub Apps authentication.
	// +optional
	User string `json:"user,omitempty"`

	// Secret reference for authentication with the Git provider. Contains the token,
	// password, or private key used to authenticate requests to the Git provider API.
	// +optional
	Secret *Secret `json:"secret,omitempty"`

	// WebhookSecret reference for webhook validation. Contains the shared secret used to
	// validate that incoming webhooks are legitimate and coming from the Git provider.
	// +optional
	WebhookSecret *Secret `json:"webhook_secret,omitempty"`

	// Type of git provider. Determines which Git provider API and authentication flow to use.
	// Supported values:
	// - 'github': GitHub.com or GitHub Enterprise
	// - 'gitlab': GitLab.com or self-hosted GitLab
	// - 'bitbucket-datacenter': Bitbucket Data Center (self-hosted)
	// - 'bitbucket-cloud': Bitbucket Cloud (bitbucket.org)
	// - 'forgejo': Forgejo instances
	// - 'gitea': Gitea instances (alias for forgejo, kept for backwards compatibility)
	// +optional
	// +kubebuilder:validation:Enum=github;gitlab;bitbucket-datacenter;bitbucket-cloud;gitea;forgejo
	Type string `json:"type,omitempty"`
}

// areEquivalentProviderTypes checks if gitea/forgejo aliases refer to the same provider.
func areEquivalentProviderTypes(a, b string) bool {
	if a == b {
		return true
	}
	giteaTypes := map[string]bool{"gitea": true, "forgejo": true}
	return giteaTypes[a] && giteaTypes[b]
}

func (g *GitProvider) Merge(newGitProvider *GitProvider) {
	// only merge of the same type (gitea and forgejo are equivalent)
	if newGitProvider.Type != "" && g.Type != "" && !areEquivalentProviderTypes(g.Type, newGitProvider.Type) {
		return
	}
	if newGitProvider.URL != "" && g.URL == "" {
		g.URL = newGitProvider.URL
	}
	if newGitProvider.User != "" && g.User == "" {
		g.User = newGitProvider.User
	}
	if newGitProvider.Type != "" && g.Type == "" {
		g.Type = newGitProvider.Type
	}
	if newGitProvider.Secret != nil && g.Secret == nil {
		g.Secret = newGitProvider.Secret
	}
	if newGitProvider.WebhookSecret != nil && g.WebhookSecret == nil {
		g.WebhookSecret = newGitProvider.WebhookSecret
	}
}

type Secret struct {
	// Name of the secret
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Key in the secret
	// +optional
	Key string `json:"key"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// RepositoryList is the list of Repositories.
// +kubebuilder:object:root=true
type RepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Repository `json:"items"`
}
