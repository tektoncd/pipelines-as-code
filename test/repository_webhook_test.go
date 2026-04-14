//go:build e2e

package test

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	ghtest "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
