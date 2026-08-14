package webhook

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pac "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/listers/pipelinesascode/v1alpha1"
	pipeline "github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "k8s.io/api/admission/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"knative.dev/pkg/webhook"
)

var universalDeserializer = serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()

// Path implements AdmissionController.
func (ac *reconciler) Path() string {
	return ac.path
}

// Admit implements AdmissionController.
func (ac *reconciler) Admit(ctx context.Context, request *v1.AdmissionRequest) *v1.AdmissionResponse {
	raw := request.Object.Raw
	repo := v1alpha1.Repository{}
	if _, _, err := universalDeserializer.Decode(raw, nil, &repo); err != nil {
		return webhook.MakeErrorStatus("validation failed: %v", err)
	}

	// Check that if we have a URL set only for non global repository which can be set as empty.
	if repo.GetNamespace() != os.Getenv("SYSTEM_NAMESPACE") {
		if repo.Spec.URL == "" {
			return webhook.MakeErrorStatus("URL must be set")
		}

		var gitProviderType string
		if repo.Spec.GitProvider != nil {
			gitProviderType = repo.Spec.GitProvider.Type
		}
		if err := validateRepositoryURL(repo.Spec.URL, gitProviderType); err != nil {
			return webhook.MakeErrorStatus("%s", err.Error())
		}

		allowed, err := ac.canCreatePipelineRuns(ctx, request)
		if err != nil {
			return webhook.MakeErrorStatus("validation failed: %v", err)
		}
		if !allowed {
			return webhook.MakeErrorStatus("user %s does not have permission to create PipelineRuns in namespace %s", request.UserInfo.Username, request.Namespace)
		}
	}

	exist, err := checkIfRepoExist(ac.pacLister, &repo, "")
	if err != nil {
		return webhook.MakeErrorStatus("validation failed: %v", err)
	}

	if exist {
		return webhook.MakeErrorStatus("repository already exists with URL: %s", repo.Spec.URL)
	}

	if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit == 0 {
		return webhook.MakeErrorStatus("concurrency limit must be greater than 0")
	}

	return &v1.AdmissionResponse{Allowed: true}
}

func (ac *reconciler) canCreatePipelineRuns(ctx context.Context, request *v1.AdmissionRequest) (bool, error) {
	sar, err := ac.client.AuthorizationV1().SubjectAccessReviews().Create(ctx, &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   request.UserInfo.Username,
			Groups: request.UserInfo.Groups,
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: request.Namespace,
				Verb:      "create",
				Group:     pipeline.PipelineRunResource.Group,
				Resource:  pipeline.PipelineRunResource.Resource,
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to check PipelineRun permissions: %w", err)
	}
	return sar.Status.Allowed, nil
}

func checkIfRepoExist(pac pac.RepositoryLister, repo *v1alpha1.Repository, ns string) (bool, error) {
	repositories, err := pac.Repositories(ns).List(labels.NewSelector())
	if err != nil {
		return false, err
	}
	for i := len(repositories) - 1; i >= 0; i-- {
		repoFromCluster := repositories[i]
		if strings.TrimRight(strings.TrimSpace(repoFromCluster.Spec.URL), "/") == strings.TrimRight(strings.TrimSpace(repo.Spec.URL), "/") &&
			(repoFromCluster.Name != repo.Name || repoFromCluster.Namespace != repo.Namespace) {
			return true, nil
		}
	}
	return false, nil
}

func validateRepositoryURL(repoURL, gitProviderType string) error {
	parsedURL, err := url.Parse(repoURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https")
	}

	// Validate github repository URL does not include additional path segments
	// (like https://github.com/org/repo/extra).
	// Detect if this is a GitHub instance (github.com or GHE) by checking headers
	//  and API endpoints.
	if isGitHubInstance(parsedURL.Host, parsedURL.Scheme, gitProviderType) {
		// Remove leading and trailing "/"
		repoPath := strings.Trim(parsedURL.Path, "/")

		split := strings.Split(repoPath, "/")
		if len(split) != 2 {
			return fmt.Errorf("github repository URL must follow https://github.com/org/repo format without subgroups (found %d path segments, expected 2): %s", len(split), repoURL)
		}
	}

	return nil
}

// isGitHubInstance detects if a host is github.com or a GitHub Enterprise instance.
// It checks the provider type first, then the host, and falls back to HTTP detection.
func isGitHubInstance(host, scheme, gitProviderType string) bool {
	if gitProviderType == "github" {
		return true
	}

	if host == "github.com" {
		return true
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	client := &http.Client{}

	// Try HEAD request to check the server header.
	url := fmt.Sprintf("%s://%s", scheme, host)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		server := resp.Header.Get("Server")
		if strings.Contains(strings.ToLower(server), "github.com") {
			return true
		}
	}

	return false
}
