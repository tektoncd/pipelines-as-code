package v1alpha1

const (
	// defaultContainerLogsMaxLines is the default maximum number of log lines to fetch per container.
	defaultContainerLogsMaxLines = 50
	defaultOpenAIURL             = "https://api.openai.com/v1"
	defaultGeminiURL             = "https://generativelanguage.googleapis.com/v1beta"
)

// AIAnalysisConfig defines configuration for AI/LLM-powered analysis of CI/CD pipeline events.
type AIAnalysisConfig struct {
	// Enabled controls whether AI analysis is active for this repository
	// +kubebuilder:validation:Required
	Enabled bool `json:"enabled"`

	// Provider specifies which LLM provider to use for analysis
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=openai;gemini
	Provider string `json:"provider"`

	// APIURL is an optional base URL to override the default API endpoint of the LLM provider.
	// If not specified, provider-specific defaults are used:
	// - OpenAI: https://api.openai.com/v1
	// - Gemini: https://generativelanguage.googleapis.com/v1beta
	// Use this to configure self-hosted LLM instances, proxy services, or alternative endpoints.
	// +optional
	APIURL string `json:"api_url,omitempty"`

	// TokenSecretRef references the Kubernetes secret containing the LLM provider API token
	// +kubebuilder:validation:Required
	TokenSecretRef *Secret `json:"secret_ref"`

	// TimeoutSeconds sets the maximum time to wait for LLM analysis (default: 30)
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=300
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`

	// MaxTokens limits the response length from the LLM (default: 1000)
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=4000
	MaxTokens int `json:"max_tokens,omitempty"`

	// Roles defines different analysis scenarios and their configurations
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Roles []AnalysisRole `json:"roles"`
}

// AnalysisRole defines a specific analysis scenario with its prompt, conditions, and output configuration.
type AnalysisRole struct {
	// Name is a unique identifier for this analysis role
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Prompt is the base prompt template sent to the LLM for analysis
	// +kubebuilder:validation:Required
	Prompt string `json:"prompt"`

	// Model specifies which LLM model to use for this role (optional).
	// You can specify any model supported by your provider.
	// If not specified, provider-specific defaults are used:
	// - OpenAI: gpt-5.4-mini
	// - Gemini: gemini-3.1-flash-lite-preview
	// +optional
	Model string `json:"model,omitempty"`

	// OnCEL is a CEL expression that determines when this role should be triggered
	// +optional
	OnCEL string `json:"on_cel,omitempty"`

	// Output specifies where the analysis results should be sent (default: pr-comment)
	// +optional
	// +kubebuilder:default=pr-comment
	// +kubebuilder:validation:Enum=pr-comment
	Output string `json:"output,omitempty"`

	// ContextItems defines what context data to include in the analysis
	// +optional
	ContextItems *ContextConfig `json:"context_items,omitempty"`
}

// ContextConfig defines what contextual information to include in LLM analysis.
type ContextConfig struct {
	// CommitContent includes commit message and diff information
	// +optional
	CommitContent bool `json:"commit_content,omitempty"`

	// PRContent includes pull request title, description, and metadata
	// +optional
	PRContent bool `json:"pr_content,omitempty"`

	// ErrorContent includes error messages and failure summaries
	// +optional
	ErrorContent bool `json:"error_content,omitempty"`

	// ContainerLogs configures inclusion of container/task logs
	// +optional
	ContainerLogs *ContainerLogsConfig `json:"container_logs,omitempty"`
}

// ContainerLogsConfig defines how container logs should be included in analysis.
type ContainerLogsConfig struct {
	// Enabled controls whether container logs are included
	Enabled bool `json:"enabled"`

	// MaxLines limits the number of log lines to include (default: 50)
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	MaxLines int `json:"max_lines,omitempty"`
}

func (c *ContainerLogsConfig) GetMaxLines() int {
	if c == nil || c.MaxLines == 0 {
		return defaultContainerLogsMaxLines
	}
	return c.MaxLines
}

// GetOutput returns the output destination with a default value if not specified.
func (r *AnalysisRole) GetOutput() string {
	if r.Output == "" {
		return "pr-comment"
	}
	return r.Output
}

// GetModel returns the configured model or an empty string to use provider default.
func (r *AnalysisRole) GetModel() string {
	return r.Model
}

// GetAPIURL returns the configured API URL, or the provider's default if not specified.
func (c *AIAnalysisConfig) GetAPIURL() string {
	if c.APIURL != "" {
		return c.APIURL
	}
	return GetProviderDefaultAPIURL(c.Provider)
}

// GetProviderDefaultAPIURL returns the default API URL for a given LLM provider.
func GetProviderDefaultAPIURL(provider string) string {
	switch provider {
	case "openai":
		return defaultOpenAIURL
	case "gemini":
		return defaultGeminiURL
	default:
		return ""
	}
}
