package params

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testnewrepo "github.com/openshift-pipelines/pipelines-as-code/pkg/test/repository"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	rtesting "knative.dev/pkg/reconciler/testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
)

func TestGetRepository(t *testing.T) {
	repo1 := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "repo-one",
		URL:              "https://github.com/owner/repo-one",
		InstallNamespace: "ns-one",
	})
	repo2 := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "repo-two",
		URL:              "https://github.com/owner/repo-two",
		InstallNamespace: "ns-two",
	})

	tests := []struct {
		name        string
		data        testclient.Data
		useLister   bool
		namespace   string
		repoName    string
		wantErr     bool
		wantNothing bool
	}{
		{
			name: "get repository with lister - found",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1, repo2},
			},
			useLister: true,
			namespace: "ns-one",
			repoName:  "repo-one",
			wantErr:   false,
		},
		{
			name: "get repository with lister - not found",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1},
			},
			useLister:   true,
			namespace:   "ns-two",
			repoName:    "nonexistent",
			wantErr:     true,
			wantNothing: true,
		},
		{
			name: "get repository without lister - found via API",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1, repo2},
			},
			useLister: false,
			namespace: "ns-two",
			repoName:  "repo-two",
			wantErr:   false,
		},
		{
			name: "get repository without lister - not found via API",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1},
			},
			useLister:   false,
			namespace:   "ns-one",
			repoName:    "nonexistent",
			wantErr:     true,
			wantNothing: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			tclients, informers := testclient.SeedTestData(t, ctx, tt.data)

			run := &Run{
				Clients: clients.Clients{
					PipelineAsCode: tclients.PipelineAsCode,
					Log:            zap.NewNop().Sugar(),
				},
				Info: info.Info{},
			}

			if tt.useLister {
				run.RepositoryLister = tclients.RepositoryLister
				// Ensure informer cache is populated
				for _, repo := range tt.data.Repositories {
					if err := informers.Repository.Informer().GetIndexer().Add(repo); err != nil {
						t.Fatal(err)
					}
				}
			}

			got, err := run.GetRepository(ctx, tt.namespace, tt.repoName)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got none")
				if tt.wantNothing {
					assert.Assert(t, apierrors.IsNotFound(err), "expected NotFound error")
				}
				return
			}

			assert.NilError(t, err)
			assert.Assert(t, got != nil)
			assert.Equal(t, got.Name, tt.repoName)
			assert.Equal(t, got.Namespace, tt.namespace)
		})
	}
}

func TestGetRepositoryDeepCopy(t *testing.T) {
	// Verify that GetRepository returns a deep copy when using the lister,
	// so mutations don't corrupt the cache.
	repo := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "test-repo",
		URL:              "https://github.com/owner/test-repo",
		InstallNamespace: "test-ns",
	})
	repo.Spec.Settings = &v1alpha1.Settings{
		GithubAppTokenScopeRepos: []string{"repo-alpha"},
	}

	ctx, _ := rtesting.SetupFakeContext(t)
	tclients, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{repo},
	})

	run := &Run{
		Clients: clients.Clients{
			PipelineAsCode: tclients.PipelineAsCode,
			Log:            zap.NewNop().Sugar(),
		},
		Info:             info.Info{},
		RepositoryLister: tclients.RepositoryLister,
	}

	if err := informers.Repository.Informer().GetIndexer().Add(repo); err != nil {
		t.Fatal(err)
	}

	// Get the repository twice
	got1, err := run.GetRepository(ctx, "test-ns", "test-repo")
	assert.NilError(t, err)

	got2, err := run.GetRepository(ctx, "test-ns", "test-repo")
	assert.NilError(t, err)

	// Mutate the first returned object's slice
	got1.Spec.Settings.GithubAppTokenScopeRepos[0] = "modified-repo"

	// The second get should still have the original value (proof of deep copy)
	assert.Equal(t, got2.Spec.Settings.GithubAppTokenScopeRepos[0], "repo-alpha",
		"mutation of first object affected the cache - deep copy failed")
}

func TestListRepositories(t *testing.T) {
	repo1 := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "repo-one",
		URL:              "https://github.com/owner/repo-one",
		InstallNamespace: "ns-one",
	})
	repo2 := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "repo-two",
		URL:              "https://github.com/owner/repo-two",
		InstallNamespace: "ns-one",
	})
	repo3 := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "repo-three",
		URL:              "https://github.com/owner/repo-three",
		InstallNamespace: "ns-two",
	})

	tests := []struct {
		name      string
		data      testclient.Data
		useLister bool
		namespace string
		wantCount int
	}{
		{
			name: "list repositories with lister - specific namespace",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1, repo2, repo3},
			},
			useLister: true,
			namespace: "ns-one",
			wantCount: 2,
		},
		{
			name: "list repositories with lister - all namespaces",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1, repo2, repo3},
			},
			useLister: true,
			namespace: "",
			wantCount: 3,
		},
		{
			name: "list repositories without lister - specific namespace via API",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1, repo2, repo3},
			},
			useLister: false,
			namespace: "ns-two",
			wantCount: 1,
		},
		{
			name: "list repositories without lister - all namespaces via API",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1, repo2, repo3},
			},
			useLister: false,
			namespace: "",
			wantCount: 3,
		},
		{
			name: "list repositories with lister - empty namespace",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1},
			},
			useLister: true,
			namespace: "nonexistent",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			tclients, informers := testclient.SeedTestData(t, ctx, tt.data)

			run := &Run{
				Clients: clients.Clients{
					PipelineAsCode: tclients.PipelineAsCode,
					Log:            zap.NewNop().Sugar(),
				},
				Info: info.Info{},
			}

			if tt.useLister {
				run.RepositoryLister = tclients.RepositoryLister
				for _, repo := range tt.data.Repositories {
					if err := informers.Repository.Informer().GetIndexer().Add(repo); err != nil {
						t.Fatal(err)
					}
				}
			}

			got, err := run.ListRepositories(ctx, tt.namespace)
			assert.NilError(t, err)
			assert.Equal(t, len(got), tt.wantCount)

			// Verify namespace filtering when a specific namespace is requested
			if tt.namespace != "" {
				for _, repo := range got {
					assert.Equal(t, repo.Namespace, tt.namespace,
						"repository %s has wrong namespace", repo.Name)
				}
			}
		})
	}
}

func TestListRepositoriesDeepCopy(t *testing.T) {
	// Verify that ListRepositories returns deep copies when using the lister
	repo := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "test-repo",
		URL:              "https://github.com/owner/test-repo",
		InstallNamespace: "test-ns",
	})
	repo.Spec.URL = "https://original.url"

	ctx, _ := rtesting.SetupFakeContext(t)
	tclients, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{repo},
	})

	run := &Run{
		Clients: clients.Clients{
			PipelineAsCode: tclients.PipelineAsCode,
			Log:            zap.NewNop().Sugar(),
		},
		Info:             info.Info{},
		RepositoryLister: tclients.RepositoryLister,
	}

	if err := informers.Repository.Informer().GetIndexer().Add(repo); err != nil {
		t.Fatal(err)
	}

	// List repositories twice
	got1, err := run.ListRepositories(ctx, "test-ns")
	assert.NilError(t, err)
	assert.Equal(t, len(got1), 1)

	got2, err := run.ListRepositories(ctx, "test-ns")
	assert.NilError(t, err)
	assert.Equal(t, len(got2), 1)

	// Mutate the first result
	got1[0].Spec.URL = "https://modified.url"

	// The second list should still have the original value
	assert.Equal(t, got2[0].Spec.URL, "https://original.url",
		"mutation of first list affected the cache - deep copy failed")
}
