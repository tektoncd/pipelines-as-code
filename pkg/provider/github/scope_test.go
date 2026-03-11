package github

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/env"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

var (
	installationID = int64(1234567)
	tempToken      = "123abcdfrf"
)

func TestScopeTokenToListOfRepos(t *testing.T) {
	var (
		repoFromWhichEventComes = "https://org.com/owner/repo"
		privateRepo             = "https://org.com/owner2/repo2"
	)
	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipelinesascode",
		},
	}

	validSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipelines-as-code-secret",
			Namespace: testNamespace.Name,
		},
		Data: map[string][]byte{
			"github-application-id": []byte("12345"),
			"github-private-key":    []byte(fakePrivateKey),
		},
	}
	repoData := &v1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "publicrepo",
			Namespace: testNamespace.Name,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: repoFromWhichEventComes,
			Settings: &v1alpha1.Settings{
				GithubAppTokenScopeRepos: []string{"owner2/repo2"},
			},
		},
	}
	repoData1 := &v1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "privaterepo",
			Namespace: testNamespace.Name,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: privateRepo,
		},
	}

	// Additional repos for glob pattern testing
	repoDataGlobAndExact := &v1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "publicrepo-glob-and-exact",
			Namespace: testNamespace.Name,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: repoFromWhichEventComes,
			Settings: &v1alpha1.Settings{
				GithubAppTokenScopeRepos: []string{"owner1/repo1", "owner2/*"},
			},
		},
	}

	repoDataInvalidGlob := &v1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "publicrepo-invalid-glob",
			Namespace: testNamespace.Name,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: repoFromWhichEventComes,
			Settings: &v1alpha1.Settings{
				GithubAppTokenScopeRepos: []string{"owner2/[invalid"},
			},
		},
	}

	repoDataGlobNoMatch := &v1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "publicrepo-glob-nomatch",
			Namespace: testNamespace.Name,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: repoFromWhichEventComes,
			Settings: &v1alpha1.Settings{
				GithubAppTokenScopeRepos: []string{"nonexistent/*"},
			},
		},
	}

	// Additional private repos for glob testing
	privateRepo2 := "https://org.com/owner2/repo3"
	repoData2 := &v1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "privaterepo2",
			Namespace: testNamespace.Name,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: privateRepo2,
		},
	}

	privateRepo3 := "https://org.com/owner1/repo1"
	repoData3 := &v1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "privaterepo3",
			Namespace: testNamespace.Name,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: privateRepo3,
		},
	}

	// Malformed repository URLs for testing validation
	malformedRepoURL := "https://org.com/owner/repo/subgroup"
	repoDataMalformed := &v1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "malformedrepo",
			Namespace: testNamespace.Name,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: malformedRepoURL,
		},
	}

	repoDataMalformedPattern := &v1alpha1.Repository{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "publicrepo-malformed-pattern",
			Namespace: testNamespace.Name,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: repoFromWhichEventComes,
			Settings: &v1alpha1.Settings{
				GithubAppTokenScopeRepos: []string{"owner/repo/extra"},
			},
		},
	}

	tests := []struct {
		name                     string
		tData                    testclient.Data
		envs                     map[string]string
		repository               *v1alpha1.Repository
		repoListsByGlobalConf    string
		secretGHAppRepoScopedKey bool
		wantError                string
		wantToken                string
		repositoryID             []int64
	}{
		{
			name: "repos are listed under global configuration",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
			},
			repository: &v1alpha1.Repository{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "publicrepo",
					Namespace: testNamespace.Name,
				},
				Spec: v1alpha1.RepositorySpec{
					URL: repoFromWhichEventComes,
				},
			},
			repoListsByGlobalConf: "owner1/repo1",
			wantError:             "",
			wantToken:             "123abcdfrf",
			repositoryID:          []int64{789, 10112},
		},
		{
			name: "repos are listed under repo level configuration",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
				Repositories: []*v1alpha1.Repository{
					repoData, repoData1,
				},
			},
			repository:            repoData,
			repoListsByGlobalConf: "",
			wantError:             "",
			wantToken:             "123abcdfrf",
			repositoryID:          []int64{789, 112233},
		},
		{
			name: "repos are listed under repo level configuration but listed repo doesn't exist in namespace",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
				Repositories: []*v1alpha1.Repository{
					repoData,
				},
			},
			repository:            repoData,
			repoListsByGlobalConf: "",
			wantError:             "failed to scope GitHub token as repo with pattern owner2/repo2 does not exist in namespace pipelinesascode",
			wantToken:             "",
		},
		{
			name: "repo exist and repos are listed under both repo level and global configuration",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
				Repositories: []*v1alpha1.Repository{
					repoData, repoData1,
				},
			},
			repository:            repoData,
			repoListsByGlobalConf: "owner1/repo1",
			wantError:             "",
			wantToken:             "123abcdfrf",
			repositoryID:          []int64{789, 10112, 112233},
		},
		{
			name: "failed to scope GitHub token to a list of repositories provided by repo level as repo scoped key secret-github-app-token-scoped is enabled",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
				Repositories: []*v1alpha1.Repository{
					repoData, repoData1,
				},
			},
			repository:               repoData,
			secretGHAppRepoScopedKey: true,
			wantError: "failed to scope GitHub token as repo scoped key secret-github-app-token-scoped is enabled. " +
				"Hint: update key secret-github-app-token-scoped from pipelines-as-code configmap to false",
			wantToken: "",
		},
		{
			name: "successfully scoped GitHub token to a list of repositories provided by global and repo level even though secret-github-app-token-scoped key is enabled because global configuration takes precedence",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
				Repositories: []*v1alpha1.Repository{
					repoData, repoData1,
				},
			},
			repository:               repoData,
			secretGHAppRepoScopedKey: true,
			repoListsByGlobalConf:    "owner1/repo1",
			wantError:                "",
			wantToken:                "123abcdfrf",
			repositoryID:             []int64{789, 10112, 112233},
		},
		{
			name: "repos are listed using both glob pattern and exact match",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
				Repositories: []*v1alpha1.Repository{
					repoDataGlobAndExact, repoData1, repoData2, repoData3,
				},
			},
			repository:            repoDataGlobAndExact,
			repoListsByGlobalConf: "",
			wantError:             "",
			wantToken:             "123abcdfrf",
			repositoryID:          []int64{789, 10112, 112233, 445566},
		},
		{
			name: "invalid glob pattern returns error",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
				Repositories: []*v1alpha1.Repository{
					repoDataInvalidGlob, repoData1,
				},
			},
			repository:            repoDataInvalidGlob,
			repoListsByGlobalConf: "",
			wantError:             "invalid repo glob specified",
			wantToken:             "",
		},
		{
			name: "glob pattern that matches no repos returns error",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
				Repositories: []*v1alpha1.Repository{
					repoDataGlobNoMatch, repoData1,
				},
			},
			repository:            repoDataGlobNoMatch,
			repoListsByGlobalConf: "",
			wantError:             "failed to scope GitHub token as repo with pattern nonexistent/* does not exist in namespace pipelinesascode",
			wantToken:             "",
		},
		{
			name: "invalid glob pattern in global config returns error",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
			},
			repository: &v1alpha1.Repository{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "publicrepo",
					Namespace: testNamespace.Name,
				},
				Spec: v1alpha1.RepositorySpec{
					URL: repoFromWhichEventComes,
				},
			},
			repoListsByGlobalConf: "owner1/[invalid",
			wantError:             "invalid repo glob specified",
			wantToken:             "",
		},
		{
			name: "malformed repository URL with extra path segments returns error",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
				Repositories: []*v1alpha1.Repository{
					repoData, repoDataMalformed,
				},
			},
			repository: &v1alpha1.Repository{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "publicrepo",
					Namespace: testNamespace.Name,
				},
				Spec: v1alpha1.RepositorySpec{
					URL: repoFromWhichEventComes,
					Settings: &v1alpha1.Settings{
						GithubAppTokenScopeRepos: []string{"owner/repo/subgroup"},
					},
				},
			},
			repoListsByGlobalConf: "",
			wantError:             "github repository URL must follow https://github.com/org/repo format without subgroups",
			wantToken:             "",
		},
		{
			name: "malformed pattern with extra path segments returns error",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
				Repositories: []*v1alpha1.Repository{
					repoDataMalformedPattern, repoData1,
				},
			},
			repository:            repoDataMalformedPattern,
			repoListsByGlobalConf: "",
			wantError:             "failed to scope GitHub token as repo with pattern owner/repo/extra does not exist in namespace",
			wantToken:             "",
		},
		{
			name: "malformed pattern in global config with extra path segments returns error",
			tData: testclient.Data{
				Namespaces: []*corev1.Namespace{testNamespace},
				Secret:     []*corev1.Secret{validSecret},
			},
			repository: &v1alpha1.Repository{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "publicrepo",
					Namespace: testNamespace.Name,
				},
				Spec: v1alpha1.RepositorySpec{
					URL: repoFromWhichEventComes,
				},
			},
			repoListsByGlobalConf: "owner/repo/extra",
			wantError:             "github repository URL must follow",
			wantToken:             "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tt.tData)
			logger, _ := logger.GetLogger()
			run := &params.Run{
				Clients: clients.Clients{
					Log:            logger,
					PipelineAsCode: stdata.PipelineAsCode,
					Kube:           stdata.Kube,
				},
				Info: info.Info{
					Controller: &info.ControllerInfo{
						Secret: info.DefaultPipelinesAscodeSecretName,
					},
				},
			}

			pacInfo := &info.PacOpts{
				Settings: settings.Settings{
					SecretGhAppTokenScopedExtraRepos: tt.repoListsByGlobalConf,
					SecretGHAppRepoScoped:            tt.secretGHAppRepoScopedKey,
				},
			}

			ctx = info.StoreCurrentControllerName(ctx, "default")
			ctx = info.StoreNS(ctx, testNamespace.GetName())

			fakeghclient, mux, serverURL, teardown := ghtesthelper.SetupGH()
			defer teardown()
			mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", installationID), func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"token": "%s"}`, tempToken)
			})

			tt.envs = make(map[string]string)
			tt.envs["PAC_GIT_PROVIDER_TOKEN_APIURL"] = serverURL + "/api/v3"
			envRemove := env.PatchAll(t, tt.envs)
			defer envRemove()

			info := &info.Event{
				Provider: &info.Provider{
					URL: "",
				},
				InstallationID: installationID,
			}

			gvcs := &Provider{
				ghClient: fakeghclient,
				Logger:   logger,
				Run:      run,
				pacInfo:  pacInfo,
			}

			extraRepoInstallIDs := map[string]string{"owner/repo": "789", "owner1/repo1": "10112", "owner2/repo2": "112233", "owner2/repo3": "445566"}
			for v := range extraRepoInstallIDs {
				split := strings.Split(v, "/")
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s", split[0], split[1]), func(w http.ResponseWriter, _ *http.Request) {
					sid := extraRepoInstallIDs[fmt.Sprintf("%s/%s", split[0], split[1])]
					_, _ = fmt.Fprintf(w, `{"id": %s}`, sid)
				})
			}
			eventEmitter := events.NewEventEmitter(run.Clients.Kube, logger)
			token, err := ScopeTokenToListOfRepos(ctx, gvcs, pacInfo, tt.repository, run, info, eventEmitter, logger)
			assert.Equal(t, token, tt.wantToken)
			if tt.wantError != "" {
				assert.Assert(t, err != nil, "expected error but got none")
				assert.Assert(t, strings.Contains(err.Error(), tt.wantError), "error %q should contain %q", err.Error(), tt.wantError)
			} else {
				assert.NilError(t, err)
			}
			assert.Equal(t, len(gvcs.RepositoryIDs), len(tt.repositoryID))
		})
	}
}
