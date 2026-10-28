package webhook

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	pipeline "github.com/tektoncd/pipeline/pkg/apis/pipeline"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	testnewrepo "github.com/tektoncd/pipelines-as-code/pkg/test/repository"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/env"
	v1 "k8s.io/api/admission/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestReconcilerAdmit(t *testing.T) {
	globalNamespace := "globalNamespace"
	envRemove := env.PatchAll(t, map[string]string{"SYSTEM_NAMESPACE": globalNamespace})
	defer envRemove()
	defaultSAR := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User: "test-user",
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace: "namespace",
				Verb:      "create",
				Group:     pipeline.PipelineRunResource.Group,
				Resource:  pipeline.PipelineRunResource.Resource,
			},
		},
		Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true},
	}
	tests := []struct {
		name    string
		repo    *v1alpha1.Repository
		allowed bool
		result  string
		sars    []*authorizationv1.SubjectAccessReview
		setup   func(*v1alpha1.Repository)
	}{
		{
			name: "allow",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://github.com/tektoncd/pipelines-as-code",
			}),
			allowed: true,
			result:  "",
		},
		{
			name: "no http or https",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "foobar",
			}),
			allowed: false,
			result:  "URL scheme must be http or https",
		},
		{
			name: "missing url",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
			}),
			allowed: false,
			result:  "URL must be set",
		},
		{
			name: "no http or https for global namespace allowed",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: globalNamespace,
				URL:              "foobar",
			}),
			allowed: true,
			result:  "URL scheme must be http or https",
		},
		{
			name: "bad url",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "h t t p s://github.com/tektoncd/pipelines-as-code", // nolint: dupword
			}),
			allowed: false,
			result:  `invalid URL format: parse "h t t p s://github.com/tektoncd/pipelines-as-code": first path segment in URL cannot contain colon`, // nolint: dupword
		},
		{
			name: "bad url for global namespace allowed",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: globalNamespace,
				URL:              "h t t p s://github.com/tektoncd/pipelines-as-code", // nolint: dupword
			}),
			allowed: true,
			result:  `invalid URL format: parse "h t t p s://github.com/tektoncd/pipelines-as-code": first path segment in URL cannot contain colon`, // nolint: dupword
		},
		{
			name: "reject",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://pac.test/already/installed",
			}),
			allowed: false,
			result:  "repository already exists with URL: https://pac.test/already/installed",
		},
		{
			name: "allow as it is be update to existing repo",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-repo-already-installed",
				InstallNamespace: "namespace",
				URL:              "https://pac.test/already/installed",
			}),
			allowed: true,
		},
		{
			name: "reject as repo namespace different",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-repo-already-installed",
				InstallNamespace: "test",
				URL:              "https://pac.test/already/installed",
			}),
			sars: []*authorizationv1.SubjectAccessReview{
				{
					Spec: authorizationv1.SubjectAccessReviewSpec{
						User: "test-user",
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Namespace: "test",
							Verb:      "create",
							Group:     pipeline.PipelineRunResource.Group,
							Resource:  pipeline.PipelineRunResource.Resource,
						},
					},
					Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true},
				},
			},
			allowed: false,
			result:  "repository already exists with URL: https://pac.test/already/installed",
		},
		{
			name: "reject github.com URL with subgroup, GitHub auto-detected",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://github.com/owner/repo/subgroup",
			}),
			allowed: false,
			result:  "github repository URL must follow https://github.com/org/repo format without subgroups (found 3 path segments, expected 2): https://github.com/owner/repo/subgroup",
		},
		{
			name: "allow github.com URL with correct format, GitHub auto-detected",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://github.com/owner/repo",
			}),
			allowed: true,
		},
		{
			name: "reject GitHub repository URL with subgroup",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://ghe.pipelinesascode.com/owner/repo/subgroup",
				GitProviderType:  "github",
			}),
			allowed: false,
			result:  "github repository URL must follow https://github.com/org/repo format without subgroups (found 3 path segments, expected 2): https://ghe.pipelinesascode.com/owner/repo/subgroup",
		},
		{
			name: "reject GitHub repository URL with multiple subgroups",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://github.com/owner/repo/subgroup/extra",
			}),
			allowed: false,
			result:  "github repository URL must follow https://github.com/org/repo format without subgroups (found 4 path segments, expected 2): https://github.com/owner/repo/subgroup/extra",
		},
		{
			name: "allow GitLab repository URL with subgroups",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://gitlab.com/owner/group/repo",
			}),
			allowed: true,
		},
		{
			name: "allow Bitbucket repository URL with subgroups",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://bitbucket.org/workspace/project/repo",
			}),
			allowed: true,
		},
		{
			name: "allow GitHub URL with correct format",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://ghe.company.com/owner/repo",
				GitProviderType:  "github",
			}),
			allowed: true,
		},
		{
			name: "reject repository URL with trailing slash",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://pac.test/already/installed/",
			}),
			allowed: false,
			result:  "repository already exists with URL: https://pac.test/already/installed/",
		},
		{
			name: "reject repository URL with multiple trailing slashes",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://pac.test/already/installed//////",
			}),
			allowed: false,
			result:  "repository already exists with URL: https://pac.test/already/installed//////",
		},
		{
			name: "reject user without PipelineRun create permission",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "ns",
				URL:              "https://github.com/owner/new-repo",
			}),
			sars: []*authorizationv1.SubjectAccessReview{
				{
					Spec: authorizationv1.SubjectAccessReviewSpec{
						User: "unauthorized-user",
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Namespace: "ns",
							Verb:      "create",
							Group:     pipeline.PipelineRunResource.Group,
							Resource:  pipeline.PipelineRunResource.Resource,
						},
					},
					Status: authorizationv1.SubjectAccessReviewStatus{Allowed: false},
				},
			},
			allowed: false,
			result:  "user unauthorized-user does not have permission to create PipelineRuns in namespace ns",
		},
		{
			name: "allow user with PipelineRun create permission",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://github.com/owner/new-repo",
			}),
			sars: []*authorizationv1.SubjectAccessReview{
				{
					Spec: authorizationv1.SubjectAccessReviewSpec{
						User: "authorized-user",
						ResourceAttributes: &authorizationv1.ResourceAttributes{
							Namespace: "namespace",
							Verb:      "create",
							Group:     pipeline.PipelineRunResource.Group,
							Resource:  pipeline.PipelineRunResource.Resource,
						},
					},
					Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true},
				},
			},
			allowed: true,
		},
		{
			name: "reject zero concurrency limit",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://github.com/owner/new-repo",
			}),
			setup: func(repo *v1alpha1.Repository) {
				zero := 0
				repo.Spec.ConcurrencyLimit = &zero
			},
			allowed: false,
			result:  "concurrency limit must be greater than 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			alreadyInstalledRepo := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-repo-already-installed",
				InstallNamespace: "namespace",
				URL:              "https://pac.test/already/installed",
			})

			sars := tt.sars
			if sars == nil {
				sars = []*authorizationv1.SubjectAccessReview{defaultSAR}
			}
			tdata := testclient.Data{
				Repositories:         []*v1alpha1.Repository{alreadyInstalledRepo},
				SubjectAccessReviews: sars,
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)

			r := reconciler{
				pacLister: stdata.RepositoryLister,
				client:    stdata.Kube,
			}

			username := "test-user"
			if len(tt.sars) > 0 {
				username = tt.sars[0].Spec.User
			}
			if tt.setup != nil {
				tt.setup(tt.repo)
			}

			userRepo, err := json.Marshal(tt.repo)
			assert.NilError(t, err)
			req := &v1.AdmissionRequest{
				Namespace: tt.repo.GetNamespace(),
				UserInfo:  authenticationv1.UserInfo{Username: username},
				Object:    runtime.RawExtension{Raw: userRepo},
			}
			res := r.Admit(ctx, req)

			assert.Equal(t, res.Allowed, tt.allowed)
			if !res.Allowed {
				assert.Equal(t, res.Result.Message, tt.result)
			}
		})
	}
}

func TestValidateRepositoryURLGitHubEnterpriseDetection(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		serverHeader   string
		wantErrContain string
	}{
		{
			name:           "github enterprise header rejects subgroup",
			path:           "/owner/repo/subgroup",
			serverHeader:   "GitHub.com",
			wantErrContain: "found 3 path segments",
		},
		{
			name:         "non github enterprise header allows subgroup",
			path:         "/owner/repo/subgroup",
			serverHeader: "nginx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Server", tt.serverHeader)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			err := validateRepositoryURL(fmt.Sprintf("%s%s", server.URL, tt.path), "")
			if tt.wantErrContain != "" {
				assert.ErrorContains(t, err, tt.wantErrContain)
				return
			}
			assert.NilError(t, err)
		})
	}
}
