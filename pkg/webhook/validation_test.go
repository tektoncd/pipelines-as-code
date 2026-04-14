package webhook

import (
	"encoding/json"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testnewrepo "github.com/openshift-pipelines/pipelines-as-code/pkg/test/repository"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/env"
	v1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestReconciler_Admit(t *testing.T) {
	globalNamespace := "globalNamespace"
	envRemove := env.PatchAll(t, map[string]string{"SYSTEM_NAMESPACE": globalNamespace})
	defer envRemove()
	tests := []struct {
		name    string
		repo    *v1alpha1.Repository
		allowed bool
		result  string
	}{
		{
			name: "allow",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: "namespace",
				URL:              "https://github.com/openshift-pipelines/pipelines-as-code",
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
				URL:              "h t t p s://github.com/openshift-pipelines/pipelines-as-code", // nolint: dupword
			}),
			allowed: false,
			result:  `invalid URL format: parse "h t t p s://github.com/openshift-pipelines/pipelines-as-code": first path segment in URL cannot contain colon`, // nolint: dupword
		},
		{
			name: "bad url for global namespace allowed",
			repo: testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-run",
				InstallNamespace: globalNamespace,
				URL:              "h t t p s://github.com/openshift-pipelines/pipelines-as-code", // nolint: dupword
			}),
			allowed: true,
			result:  `invalid URL format: parse "h t t p s://github.com/openshift-pipelines/pipelines-as-code": first path segment in URL cannot contain colon`, // nolint: dupword
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
			allowed: false,
			result:  "repository already exists with URL: https://pac.test/already/installed",
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			alreadyInstalledRepo := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-repo-already-installed",
				InstallNamespace: "namespace",
				URL:              "https://pac.test/already/installed",
			})
			tdata := testclient.Data{Repositories: []*v1alpha1.Repository{alreadyInstalledRepo}}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)

			r := reconciler{
				pacLister: stdata.RepositoryLister,
			}

			userRepo, err := json.Marshal(tt.repo)
			assert.NilError(t, err)
			req := &v1.AdmissionRequest{Object: runtime.RawExtension{Raw: userRepo}}
			res := r.Admit(ctx, req)

			assert.Equal(t, res.Allowed, tt.allowed)
			if !res.Allowed {
				assert.Equal(t, res.Result.Message, tt.result)
			}
		})
	}
}
