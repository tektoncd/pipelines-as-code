package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	ktypes "github.com/openshift-pipelines/pipelines-as-code/pkg/secrets/types"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	testkubernetestint "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
	"knative.dev/pkg/system"
)

// TestFollowUpGitHubAppTokenScope checks the GitHub App token the watcher
// mints for a follow-up (and hands to its run through the git auth secret) is
// scoped like the event-time token: to the triggering repository, extended
// with the repositories the local or the global Repository CR lists.
func TestFollowUpGitHubAppTokenScope(t *testing.T) {
	const (
		secretName = "pipelines-as-code-secret"
		ns         = "ns"
		globalNS   = "global"
		globalName = "global-repo"
	)
	repoIDs := map[string]int64{"app": 111, "extra": 222, "local": 333}

	tests := []struct {
		name            string
		localScope      []string
		globalScope     []string
		wantRepoNames   []string
		wantRepoIDs     []int64
		wantGetRepoHits []string
	}{
		{
			name:          "no extra repository scopes the token to the triggering repository",
			wantRepoNames: []string{"app"},
		},
		{
			name:        "global repository scope list is merged in",
			globalScope: []string{"random/extra"},
			wantRepoIDs: []int64{repoIDs["extra"], repoIDs["app"]},
		},
		{
			name:        "local repository scope list wins over the global one",
			localScope:  []string{"random/local"},
			globalScope: []string{"random/extra"},
			wantRepoIDs: []int64{repoIDs["local"], repoIDs["app"]},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			_, mux, serverURL, teardown := ghtesthelper.SetupGH()
			defer teardown()
			t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", serverURL+"/api/v3")

			var (
				mu        sync.Mutex
				lastBody  map[string]any
				firstBody map[string]any
			)
			mux.HandleFunc("/app/installations/1234/access_tokens", func(rw http.ResponseWriter, req *http.Request) {
				body := map[string]any{}
				_ = json.NewDecoder(req.Body).Decode(&body)
				mu.Lock()
				if firstBody == nil {
					firstBody = body
				}
				lastBody = body
				mu.Unlock()
				fmt.Fprint(rw, `{"token":"ghs_test_token","expires_at":"2099-01-01T00:00:00Z"}`)
			})
			for name, id := range repoIDs {
				mux.HandleFunc("/repos/random/"+name, func(rw http.ResponseWriter, _ *http.Request) {
					fmt.Fprintf(rw, `{"id":%d,"full_name":"random/%s"}`, id, name)
				})
			}

			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "build",
					Namespace: ns,
					Annotations: map[string]string{
						keys.GitProvider:    "github",
						keys.InstallationID: "1234",
						keys.RepoURL:        "https://github.com/random/app",
						keys.URLOrg:         "random",
						keys.URLRepository:  "app",
						keys.SHA:            "123afc",
						keys.Repository:     "app",
					},
				},
			}
			newRepo := func(name, namespace, url string, scope []string) *v1alpha1.Repository {
				repo := &v1alpha1.Repository{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
					Spec:       v1alpha1.RepositorySpec{URL: url},
				}
				if scope != nil {
					repo.Spec.Settings = &v1alpha1.Settings{GithubAppTokenScopeRepos: scope}
				}
				return repo
			}
			repos := []*v1alpha1.Repository{
				newRepo("app", ns, "https://github.com/random/app", tt.localScope),
				newRepo("extra", ns, "https://github.com/random/extra", nil),
				newRepo("local", ns, "https://github.com/random/local", nil),
				newRepo(globalName, globalNS, "", tt.globalScope),
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
				Repositories: repos,
				Secret: []*corev1.Secret{{
					ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: system.Namespace()},
					Data: map[string][]byte{
						keys.GithubApplicationID: []byte("12345"),
						keys.GithubPrivateKey:    []byte(reconcilerFakePrivateKey),
					},
				}},
				ConfigMap: []*corev1.ConfigMap{defaultPolicyConfigMap()},
			})
			ctx = info.StoreNS(ctx, system.Namespace())

			// repository-level scope lists require turning the default
			// repo-scoped token setting off, like users have to
			pacOpts := info.NewPacOpts()
			pacOpts.SecretGHAppRepoScoped = false

			r := &Reconciler{
				repoLister:   informers.Repository.Lister(),
				kinteract:    &testkubernetestint.KinterfaceTest{GetSecretResult: map[string]string{secretName: "webhook-secret"}},
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
				run: &params.Run{
					Clients: clients.Clients{
						Kube:           stdata.Kube,
						PipelineAsCode: stdata.PipelineAsCode,
						Log:            logger,
					},
					Info: info.Info{
						Pac:  pacOpts,
						Kube: &info.KubeOpts{Namespace: globalNS},
						Controller: &info.ControllerInfo{
							Name:             "default",
							Secret:           secretName,
							GlobalRepository: globalName,
						},
					},
				},
			}

			// the follow-up path: the local repository of the event, the
			// provider client the follow-up is created with merges the global one
			repo, err := r.followUpRepo(pr)
			assert.NilError(t, err)
			_, event, err := r.initGitProviderClient(ctx, logger, repo, pr)
			assert.NilError(t, err)
			assert.Equal(t, "ghs_test_token", event.Provider.Token)

			mu.Lock()
			defer mu.Unlock()
			assert.Assert(t, lastBody != nil, "no installation token was requested")
			// like at event time, the bootstrap token the extra repositories
			// are looked up with is not narrowed to the triggering
			// repository: a private extra repository would not be found
			assert.Assert(t, firstBody["repositories"] == nil && firstBody["repository_ids"] == nil,
				"the bootstrap token is scoped: %v", firstBody)
			if tt.wantRepoNames != nil {
				names, ok := lastBody["repositories"].([]any)
				assert.Assert(t, ok, "the token is not scoped by repository name: %v", lastBody)
				got := make([]string, 0, len(names))
				for _, v := range names {
					name, _ := v.(string)
					got = append(got, name)
				}
				assert.DeepEqual(t, tt.wantRepoNames, got)
				assert.Assert(t, lastBody["repository_ids"] == nil)
			}
			if tt.wantRepoIDs != nil {
				ids, ok := lastBody["repository_ids"].([]any)
				assert.Assert(t, ok, "the token is not scoped by repository id: %v", lastBody)
				got := make([]int64, 0, len(ids))
				for _, v := range ids {
					id, _ := v.(float64)
					got = append(got, int64(id))
				}
				assert.DeepEqual(t, tt.wantRepoIDs, got)
			}
		})
	}
}

// namespacedSecrets is a kubeinteraction fake that only finds a secret in
// the namespace it lives in.
type namespacedSecrets struct {
	testkubernetestint.KinterfaceTest
	secrets map[string]string // "namespace/name" -> value
}

func (k *namespacedSecrets) GetSecret(_ context.Context, opt ktypes.GetSecretOpt) (string, error) {
	value, ok := k.secrets[opt.Namespace+"/"+opt.Name]
	if !ok {
		return "", fmt.Errorf("secret %s/%s does not exist", opt.Namespace, opt.Name)
	}
	return value, nil
}

// TestFollowUpClient checks the follow-up's provider client is initialized
// from the local repository, so a git provider secret inherited from the
// global repository is read from the global namespace, while the repository
// the follow-up is created for honors the global settings.
func TestFollowUpClient(t *testing.T) {
	const (
		ns         = "ns"
		globalNS   = "global"
		globalName = "global-repo"
	)
	tests := []struct {
		name        string
		localSecret string
		wantToken   string
	}{
		{
			name:      "the git provider secret is inherited from the global repository",
			wantToken: "global-token",
		},
		{
			name:        "a local git provider secret is read from the repository namespace",
			localSecret: "local-provider-secret",
			wantToken:   "local-token",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "build",
					Namespace: ns,
					Annotations: map[string]string{
						keys.GitProvider:   "github",
						keys.RepoURL:       "https://github.com/org/repo",
						keys.URLOrg:        "org",
						keys.URLRepository: "repo",
						keys.SHA:           "abc123",
						keys.Repository:    "repo",
					},
				},
			}
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: ns},
				Spec: v1alpha1.RepositorySpec{
					URL:         "https://github.com/org/repo",
					GitProvider: &v1alpha1.GitProvider{},
				},
			}
			if tt.localSecret != "" {
				repo.Spec.GitProvider.Secret = &v1alpha1.Secret{Name: tt.localSecret}
			}
			limit := 2
			globalRepo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: globalName, Namespace: globalNS},
				Spec: v1alpha1.RepositorySpec{
					ConcurrencyLimit: &limit,
					GitProvider: &v1alpha1.GitProvider{
						Secret: &v1alpha1.Secret{Name: "global-provider-secret"},
					},
				},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
				Repositories: []*v1alpha1.Repository{repo, globalRepo},
				ConfigMap:    []*corev1.ConfigMap{defaultPolicyConfigMap()},
			})
			ctx = info.StoreNS(ctx, system.Namespace())
			r := &Reconciler{
				repoLister: informers.Repository.Lister(),
				kinteract: &namespacedSecrets{secrets: map[string]string{
					globalNS + "/global-provider-secret": "global-token",
					ns + "/local-provider-secret":        "local-token",
				}},
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
				run: &params.Run{
					Clients: clients.Clients{
						Kube:           stdata.Kube,
						PipelineAsCode: stdata.PipelineAsCode,
						Log:            logger,
					},
					Info: info.Info{
						Kube:       &info.KubeOpts{Namespace: globalNS},
						Controller: &info.ControllerInfo{GlobalRepository: globalName},
						Pac:        info.NewPacOpts(),
					},
				},
			}

			merged, _, event, err := r.followUpClient(ctx, logger, pr)
			assert.NilError(t, err)
			assert.Equal(t, tt.wantToken, event.Provider.Token)
			assert.Assert(t, merged.Spec.ConcurrencyLimit != nil && *merged.Spec.ConcurrencyLimit == limit,
				"the follow-up must honor the global concurrency limit")
		})
	}
}
