package webhook

import (
	"context"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pac "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/listers/pipelinesascode/v1alpha1"
	v1 "k8s.io/api/admission/v1"
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
func (ac *reconciler) Admit(_ context.Context, request *v1.AdmissionRequest) *v1.AdmissionResponse {
	raw := request.Object.Raw
	repo := v1alpha1.Repository{}
	if _, _, err := universalDeserializer.Decode(raw, nil, &repo); err != nil {
		return webhook.MakeErrorStatus("validation failed: %v", err)
	}

	exist, err := checkIfRepoExist(ac.pacLister, &repo, "")
	if err != nil {
		return webhook.MakeErrorStatus("validation failed: %v", err)
	}

	if exist {
		return webhook.MakeErrorStatus("repository already exist with url: %s", repo.Spec.URL)
	}

	if repo.Spec.ConcurrencyLimit != nil && *repo.Spec.ConcurrencyLimit == 0 {
		return webhook.MakeErrorStatus("concurrency limit must be greater than 0")
	}

	return &v1.AdmissionResponse{Allowed: true}
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
