package matcher

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testnewrepo "github.com/openshift-pipelines/pipelines-as-code/pkg/test/repository"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestGetRepoByNameWithLister(t *testing.T) {
	repo1 := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "shared-name",
		URL:              "https://github.com/owner/repo-one",
		InstallNamespace: "ns-one",
	})
	repo2 := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "shared-name",
		URL:              "https://github.com/owner/repo-two",
		InstallNamespace: "ns-two",
	})
	repo3 := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
		Name:             "unique-name",
		URL:              "https://github.com/owner/repo-three",
		InstallNamespace: "ns-three",
	})

	tests := []struct {
		name      string
		data      testclient.Data
		useLister bool
		repoName  string
		namespace string
		wantErr   bool
		wantNS    string
	}{
		{
			name: "get by name with lister - found in specific namespace",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1, repo2, repo3},
			},
			useLister: true,
			repoName:  "shared-name",
			namespace: "ns-one",
			wantErr:   false,
			wantNS:    "ns-one",
		},
		{
			name: "get by name with lister - conflict without namespace",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1, repo2},
			},
			useLister: true,
			repoName:  "shared-name",
			namespace: "",
			wantErr:   true,
		},
		{
			name: "get by name with lister - unique name without namespace",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1, repo2, repo3},
			},
			useLister: true,
			repoName:  "unique-name",
			namespace: "",
			wantErr:   false,
			wantNS:    "ns-three",
		},
		{
			name: "get by name with lister - not found",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1},
			},
			useLister: true,
			repoName:  "nonexistent",
			namespace: "ns-one",
			wantErr:   false,
			wantNS:    "",
		},
		{
			name: "get by name without lister - uses API with FieldSelector",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1, repo2, repo3},
			},
			useLister: false,
			repoName:  "shared-name",
			namespace: "",
			wantErr:   true, // conflict
		},
		{
			name: "get by name without lister - found via API",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo3},
			},
			useLister: false,
			repoName:  "unique-name",
			namespace: "",
			wantErr:   false,
			wantNS:    "ns-three",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			tclients, informers := testclient.SeedTestData(t, ctx, tt.data)

			run := &params.Run{
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

			got, err := GetRepoByName(ctx, run, tt.repoName, tt.namespace)

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got none")
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantNS == "" {
				assert.Assert(t, got == nil, "expected nil but got repository")
			} else {
				assert.Assert(t, got != nil, "expected repository but got nil")
				assert.Equal(t, got.Name, tt.repoName)
				assert.Equal(t, got.Namespace, tt.wantNS)
			}
		})
	}
}

func TestMatchEventURLRepoWithLister(t *testing.T) {
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

	tests := []struct {
		name      string
		data      testclient.Data
		useLister bool
		eventURL  string
		namespace string
		wantFound bool
		wantName  string
	}{
		{
			name: "match event URL with lister - found",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1, repo2},
			},
			useLister: true,
			eventURL:  "https://github.com/owner/repo-one",
			namespace: "",
			wantFound: true,
			wantName:  "repo-one",
		},
		{
			name: "match event URL with lister - not found",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo1},
			},
			useLister: true,
			eventURL:  "https://github.com/owner/nonexistent",
			namespace: "",
			wantFound: false,
		},
		{
			name: "match event URL without lister - uses API List",
			data: testclient.Data{
				Repositories: []*v1alpha1.Repository{repo2},
			},
			useLister: false,
			eventURL:  "https://github.com/owner/repo-two",
			namespace: "",
			wantFound: true,
			wantName:  "repo-two",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			tclients, informers := testclient.SeedTestData(t, ctx, tt.data)

			run := &params.Run{
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

			event := &info.Event{
				URL: tt.eventURL,
			}

			got, err := MatchEventURLRepo(ctx, run, event, tt.namespace)
			assert.NilError(t, err)

			if tt.wantFound {
				assert.Assert(t, got != nil, "expected to find repository but got nil")
				assert.Equal(t, got.Name, tt.wantName)
			} else {
				assert.Assert(t, got == nil, "expected nil but found repository %v", got)
			}
		})
	}
}
