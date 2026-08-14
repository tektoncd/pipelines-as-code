//go:build e2e

package test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	pacversioned "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	ghtest "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func TestOthersRepositoryCreation(t *testing.T) {
	ctx := context.TODO()
	ctx, runcnx, _, _, err := ghtest.Setup(ctx, false, false)
	assert.NilError(t, err)

	targetNs := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("test-repo")
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: targetNs,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://pac.test/pac/app",
		},
	}

	defer repository.NSTearDown(ctx, t, runcnx, targetNs)
	err = repository.CreateNS(ctx, targetNs, runcnx)
	assert.NilError(t, err)
	err = repository.CreateRepo(ctx, targetNs, runcnx, repo)
	assert.NilError(t, err)

	// create a new cr with same git url
	targetNsNew := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("test-repo-new")
	repo.Name = "test-repo-new"

	defer repository.NSTearDown(ctx, t, runcnx, targetNsNew)
	err = repository.CreateNS(ctx, targetNsNew, runcnx)
	assert.NilError(t, err)
	err = repository.CreateRepo(ctx, targetNsNew, runcnx, repo)
	assert.Assert(t, err != nil)
	assert.Equal(t, err.Error(), "admission webhook \"validation.pipelinesascode.tekton.dev\" denied the request: repository already exists with URL: https://pac.test/pac/app")
}

func TestOthersRepositoryCreationWithTrailingSlash(t *testing.T) {
	ctx := context.TODO()
	ctx, runcnx, _, _, err := ghtest.Setup(ctx, false, false)
	assert.NilError(t, err)

	targetNs := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("test-repo")
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: targetNs,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://pac.test/pac/app",
		},
	}

	defer repository.NSTearDown(ctx, t, runcnx, targetNs)
	err = repository.CreateNS(ctx, targetNs, runcnx)
	assert.NilError(t, err)
	err = repository.CreateRepo(ctx, targetNs, runcnx, repo)
	assert.NilError(t, err)

	// create a new cr with same git url
	targetNsNew := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("test-repo-new")
	repo.Name = "test-repo-new"
	repo.Spec.URL = "https://pac.test/pac/app/"

	defer repository.NSTearDown(ctx, t, runcnx, targetNsNew)
	err = repository.CreateNS(ctx, targetNsNew, runcnx)
	assert.NilError(t, err)
	err = repository.CreateRepo(ctx, targetNsNew, runcnx, repo)
	assert.Assert(t, err != nil)
	assert.Equal(t, err.Error(), "admission webhook \"validation.pipelinesascode.tekton.dev\" denied the request: repository already exists with URL: https://pac.test/pac/app/")
}

func TestOthersRepositoryCreationWithoutPipelineRunPermission(t *testing.T) {
	ctx := context.TODO()
	ctx, runcnx, _, _, err := ghtest.Setup(ctx, false, false)
	assert.NilError(t, err)

	targetNs := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("test-sar")
	err = repository.CreateNS(ctx, targetNs, runcnx)
	assert.NilError(t, err)
	defer repository.NSTearDown(ctx, t, runcnx, targetNs)

	saName := "restricted-sa"
	_, err = runcnx.Clients.Kube.CoreV1().ServiceAccounts(targetNs).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: saName},
	}, metav1.CreateOptions{})
	assert.NilError(t, err)

	_, err = runcnx.Clients.Kube.RbacV1().Roles(targetNs).Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "repo-only"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"pipelinesascode.tekton.dev"},
				Resources: []string{"repositories"},
				Verbs:     []string{"create", "get", "list", "watch"},
			},
		},
	}, metav1.CreateOptions{})
	assert.NilError(t, err)

	_, err = runcnx.Clients.Kube.RbacV1().RoleBindings(targetNs).Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "repo-only-binding"},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: targetNs,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "repo-only",
		},
	}, metav1.CreateOptions{})
	assert.NilError(t, err)

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	config, err := kubeConfig.ClientConfig()
	assert.NilError(t, err)

	config.Impersonate = rest.ImpersonationConfig{
		UserName: fmt.Sprintf("system:serviceaccount:%s:%s", targetNs, saName),
	}

	pacClient, err := pacversioned.NewForConfig(config)
	assert.NilError(t, err)

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-restricted",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://github.com/owner/restricted-repo",
		},
	}

	_, err = pacClient.PipelinesascodeV1alpha1().Repositories(targetNs).Create(ctx, repo, metav1.CreateOptions{})
	assert.Assert(t, err != nil)
	assert.Assert(t, strings.Contains(err.Error(), "does not have permission to create PipelineRuns"))
}

func TestOthersRepositoryCreationWithPipelineRunPermission(t *testing.T) {
	ctx := context.TODO()
	ctx, runcnx, _, _, err := ghtest.Setup(ctx, false, false)
	assert.NilError(t, err)

	targetNs := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("test-sar-ok")
	err = repository.CreateNS(ctx, targetNs, runcnx)
	assert.NilError(t, err)
	defer repository.NSTearDown(ctx, t, runcnx, targetNs)

	saName := "full-access-sa"
	_, err = runcnx.Clients.Kube.CoreV1().ServiceAccounts(targetNs).Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: saName},
	}, metav1.CreateOptions{})
	assert.NilError(t, err)

	_, err = runcnx.Clients.Kube.RbacV1().Roles(targetNs).Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "repo-and-pipelinerun"},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"pipelinesascode.tekton.dev"},
				Resources: []string{"repositories"},
				Verbs:     []string{"create", "get", "list", "patch", "watch"},
			},
			{
				APIGroups: []string{"tekton.dev"},
				Resources: []string{"pipelineruns"},
				Verbs:     []string{"create"},
			},
		},
	}, metav1.CreateOptions{})
	assert.NilError(t, err)

	_, err = runcnx.Clients.Kube.RbacV1().RoleBindings(targetNs).Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "repo-and-pipelinerun-binding"},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: targetNs,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "repo-and-pipelinerun",
		},
	}, metav1.CreateOptions{})
	assert.NilError(t, err)

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	config, err := kubeConfig.ClientConfig()
	assert.NilError(t, err)

	config.Impersonate = rest.ImpersonationConfig{
		UserName: fmt.Sprintf("system:serviceaccount:%s:%s", targetNs, saName),
	}

	pacClient, err := pacversioned.NewForConfig(config)
	assert.NilError(t, err)

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-full-access",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://github.com/owner/full-access-repo",
		},
	}

	_, err = pacClient.PipelinesascodeV1alpha1().Repositories(targetNs).Create(ctx, repo, metav1.CreateOptions{})
	assert.NilError(t, err)
}
