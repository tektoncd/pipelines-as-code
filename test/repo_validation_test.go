//go:build e2e

package test

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	pacrepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestOthersRepoValidation(t *testing.T) {
	ctx := context.TODO()
	run := params.New()
	assert.NilError(t, run.Clients.NewClients(ctx, &run.Info))
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	assert.NilError(t, pacrepo.CreateNS(ctx, targetNS, run))

	tests := []struct {
		name        string
		url         string
		expectedErr string
	}{
		{
			name:        "not http or https",
			url:         "foobar",
			expectedErr: "URL scheme must be http or https",
		},
		{
			name:        "invalid URL",
			url:         "http://   ",
			expectedErr: "invalid URL format",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name: targetNS,
				},
				Spec: v1alpha1.RepositorySpec{
					URL: tt.url,
				},
			}
			err := pacrepo.CreateRepo(ctx, targetNS, run, repository)
			assert.ErrorContains(t, err, tt.expectedErr)
		})
	}
}

// TestOthersRepoValidationCommentStrategy covers the Repository CRD's
// OpenAPI enum constraint on comment_strategy for each provider's
// settings, enforced by the API server rather than the admission webhook.
func TestOthersRepoValidationCommentStrategy(t *testing.T) {
	ctx := context.TODO()
	run := params.New()
	assert.NilError(t, run.Clients.NewClients(ctx, &run.Info))
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	assert.NilError(t, pacrepo.CreateNS(ctx, targetNS, run))
	defer pacrepo.NSTearDown(ctx, t, run, targetNS)

	tests := []struct {
		name        string
		url         string
		settings    *v1alpha1.Settings
		allowed     bool
		expectedErr string
	}{
		{
			name:     "allow github comment strategy update",
			url:      "https://github.com/owner1/repo",
			settings: &v1alpha1.Settings{Github: &v1alpha1.GithubSettings{CommentStrategy: "update"}},
			allowed:  true,
		},
		{
			name:     "reject invalid github comment strategy",
			url:      "https://github.com/owner2/repo",
			settings: &v1alpha1.Settings{Github: &v1alpha1.GithubSettings{CommentStrategy: "invalid"}},
			allowed:  false,
			// Rejected by the CRD's OpenAPI enum constraint before the admission webhook runs.
			expectedErr: `spec.settings.github.comment_strategy: Unsupported value: "invalid": supported values: "", "disable_all", "update"`,
		},
		{
			name:     "allow gitlab comment strategy disable_all",
			url:      "https://gitlab.com/owner1/repo",
			settings: &v1alpha1.Settings{Gitlab: &v1alpha1.GitlabSettings{CommentStrategy: "disable_all"}},
			allowed:  true,
		},
		{
			name:     "reject invalid gitlab comment strategy",
			url:      "https://gitlab.com/owner2/repo",
			settings: &v1alpha1.Settings{Gitlab: &v1alpha1.GitlabSettings{CommentStrategy: "invalid"}},
			allowed:  false,
			// Rejected by the CRD's OpenAPI enum constraint before the admission webhook runs.
			expectedErr: `spec.settings.gitlab.comment_strategy: Unsupported value: "invalid": supported values: "", "disable_all", "update"`,
		},
		{
			name:     "allow forgejo comment strategy update",
			url:      "https://forgejo.example.com/owner1/repo",
			settings: &v1alpha1.Settings{Forgejo: &v1alpha1.ForgejoSettings{CommentStrategy: "update"}},
			allowed:  true,
		},
		{
			name:     "reject invalid forgejo comment strategy",
			url:      "https://forgejo.example.com/owner2/repo",
			settings: &v1alpha1.Settings{Forgejo: &v1alpha1.ForgejoSettings{CommentStrategy: "invalid"}},
			allowed:  false,
			// Rejected by the CRD's OpenAPI enum constraint before the admission webhook runs.
			expectedErr: `spec.settings.forgejo.comment_strategy: Unsupported value: "invalid": supported values: "", "disable_all", "update"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name: names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("repo"),
				},
				Spec: v1alpha1.RepositorySpec{
					URL:      tt.url,
					Settings: tt.settings,
				},
			}
			err := pacrepo.CreateRepo(ctx, targetNS, run, repository)
			if tt.allowed {
				assert.NilError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.expectedErr)
		})
	}
}
