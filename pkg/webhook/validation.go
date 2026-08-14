package webhook

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pac "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/listers/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	pipeline "github.com/tektoncd/pipeline/pkg/apis/pipeline"
	v1 "k8s.io/api/admission/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/sets"
	"knative.dev/pkg/webhook"
)

var universalDeserializer = serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()

var allowedGitlabDisableCommentStrategyOnMr = sets.NewString("", provider.DisableAllCommentStrategy, provider.UpdateCommentStrategy)

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

		parsed, err := url.Parse(repo.Spec.URL)
		if err != nil {
			return webhook.MakeErrorStatus("invalid URL format: %v", err)
		}

		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return webhook.MakeErrorStatus("URL scheme must be http or https")
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

	if repo.Spec.Settings != nil && repo.Spec.Settings.Gitlab != nil {
		if !allowedGitlabDisableCommentStrategyOnMr.Has(repo.Spec.Settings.Gitlab.CommentStrategy) {
			return webhook.MakeErrorStatus("comment strategy '%s' is not supported for Gitlab MRs", repo.Spec.Settings.Gitlab.CommentStrategy)
		}
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
		if repoFromCluster.Spec.URL == repo.Spec.URL &&
			(repoFromCluster.Name != repo.Name || repoFromCluster.Namespace != repo.Namespace) {
			return true, nil
		}
	}
	return false, nil
}
