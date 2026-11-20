package webhook

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	api "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	listers "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/listers/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
)

func TestRefreshBeforeRelease(t *testing.T) {
	tests := []struct {
		name                         string
		remaining                    time.Duration
		dryRun, failUpdate, mint     bool
		wrongRepo, wrongOwner        bool
		withProviderSecret           bool
		repoURL                      string
		withoutExpiry, withoutSecret bool
		issueFails                   bool
		nextStatus                   tektonv1.PipelineRunSpecStatus
		wantError                    string
	}{
		{name: "GitHub unavailable", remaining: -time.Hour, mint: true, issueFails: true, wantError: "GitHub unavailable"},
		{name: "cancel pending build", remaining: -time.Hour, nextStatus: tektonv1.PipelineRunSpecStatusCancelled},
		{name: "expired", remaining: -time.Hour, mint: true},
		{name: "near expiry", remaining: time.Minute, mint: true},
		{name: "App run with Repository token Secret", remaining: -time.Hour, mint: true, withProviderSecret: true},
		{name: "mixed-case GitHub host", remaining: -time.Hour, mint: true, repoURL: "https://GitHub.com/org/repo"},
		{name: "repository mismatch", remaining: -time.Hour, wrongRepo: true, wantError: "does not match an active Repository"},
		{name: "foreign Secret", remaining: -time.Hour, wrongOwner: true, wantError: "does not match queued PipelineRun"},
		{name: "Secret without recorded expiry", withoutExpiry: true},
		{name: "auto-created Secret absent", withoutSecret: true},
		{name: "fresh", remaining: time.Hour},
		{name: "dry run", remaining: -time.Hour, dryRun: true},
		{name: "failed write blocks release", remaining: -time.Hour, failUpdate: true, mint: true, wantError: "write failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			repoURL := "https://github.com/org/repo"
			if tt.repoURL != "" {
				repoURL = tt.repoURL
			}
			run := tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
				Name: "build", Namespace: "builds", UID: "one",
				Annotations: map[string]string{
					keys.Repository: "source", keys.RepoURL: repoURL,
					keys.GitAuthSecret: "auth", keys.InstallationID: "1",
				},
			}}
			secret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "auth", Namespace: run.Namespace,
					OwnerReferences: []metav1.OwnerReference{{Name: run.Name, Kind: "PipelineRun", UID: run.UID}},
					Annotations: map[string]string{
						keys.GitHubTokenExpiry: time.Now().Add(tt.remaining).Format(time.RFC3339),
					},
				},
				Data: map[string][]byte{
					".git-credentials":   []byte("https://git:old@github.com/org/repo"),
					"git-provider-token": []byte("old"),
				},
			}
			if tt.wrongOwner {
				secret.OwnerReferences[0].UID = "different"
			}
			if tt.withoutExpiry {
				delete(secret.Annotations, keys.GitHubTokenExpiry)
			}
			client := fake.NewSimpleClientset()
			if !tt.withoutSecret {
				_, err := client.CoreV1().Secrets(run.Namespace).Create(ctx, &secret, metav1.CreateOptions{})
				assert.NilError(t, err)
			}
			index := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
			repo := &api.Repository{ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "builds"}, Spec: api.RepositorySpec{URL: run.Annotations[keys.RepoURL]}}
			if tt.wrongRepo {
				repo.Spec.URL = strings.Replace(repo.Spec.URL, "/repo", "/another", 1)
			}
			if tt.withProviderSecret {
				repo.Spec.GitProvider = &api.GitProvider{Secret: &api.Secret{Name: "webhook-token"}}
			}
			assert.NilError(t, index.Add(repo))
			if tt.failUpdate {
				client.PrependReactor("update", "secrets", func(ktesting.Action) (bool, runtime.Object, error) { return true, nil, errors.New("write failed") })
			}
			ac := reconciler{pacLister: listers.NewRepositoryLister(index), client: client}
			run.Spec.Status = tt.nextStatus
			next, err := json.Marshal(run)
			assert.NilError(t, err)
			run.Spec.Status = tektonv1.PipelineRunSpecStatusPending
			old, err := json.Marshal(run)
			assert.NilError(t, err)
			req := admissionv1.AdmissionRequest{Namespace: "builds", Operation: admissionv1.Update, DryRun: &tt.dryRun, OldObject: runtime.RawExtension{Raw: old}, Object: runtime.RawExtension{Raw: next}}
			calls := 0
			issue := func(_ context.Context, current *api.Repository, installationID int64, controller *info.ControllerInfo) (string, time.Time, error) {
				calls++
				assert.DeepEqual(t, current, repo)
				assert.Equal(t, installationID, int64(1))
				assert.DeepEqual(t, controller, info.GetControllerInfoFromEnvOrDefault())
				if tt.issueFails {
					return "", time.Time{}, errors.New("GitHub unavailable")
				}
				return "fresh", time.Now().Add(time.Hour), nil
			}
			err = ac.refreshBeforeRelease(ctx, &req, issue)
			if tt.wantError != "" {
				assert.ErrorContains(t, err, tt.wantError)
			} else {
				assert.NilError(t, err)
			}
			assert.Equal(t, calls > 0, tt.mint)
			if tt.withoutSecret {
				return
			}
			got, err := client.CoreV1().Secrets("builds").Get(ctx, "auth", metav1.GetOptions{})
			assert.NilError(t, err)
			want := "old"
			if tt.mint && tt.wantError == "" {
				want = "fresh"
				assert.NilError(t, ac.refreshBeforeRelease(ctx, &req, issue))
				assert.Equal(t, calls, 1)
			}
			assert.Equal(t, string(got.Data["git-provider-token"]), want)
			assert.Equal(t, string(got.Data[".git-credentials"]), "https://git:"+want+"@github.com/org/repo")
		})
	}
}

func TestIssueQueuedTokenUsesConfiguredScope(t *testing.T) {
	const controllerNS = "pipelines-as-code"
	controller := info.ControllerInfo{
		Name:             "secondary",
		Secret:           "secondary-app",
		Configmap:        "secondary-config",
		GlobalRepository: "secondary-global",
	}
	annotation, err := json.Marshal(controller)
	assert.NilError(t, err)

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	assert.NilError(t, err)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	tests := []struct {
		name             string
		failSourceLookup bool
		wantAllowed      bool
		wantErrContains  string
		wantScopes       []int64
	}{
		{
			name:        "configured extra repos include source repo",
			wantAllowed: true,
			wantScopes:  []int64{2, 4, 3},
		},
		{
			name:             "source repo lookup failure blocks release",
			failSourceLookup: true,
			wantAllowed:      false,
			wantErrContains:  "cannot resolve GitHub repository org/source",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SYSTEM_NAMESPACE", controllerNS)
			t.Setenv("PAC_CONTROLLER_SECRET", "default-app")
			t.Setenv("PAC_CONTROLLER_CONFIGMAP", "default-config")
			t.Setenv("PAC_CONTROLLER_GLOBAL_REPOSITORY", "default-global")

			expiry := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/v3/repos/org/source/installation", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, `{"id":1}`)
			})
			mux.HandleFunc("GET /api/v3/repos/org/{repo}", func(w http.ResponseWriter, r *http.Request) {
				if tt.failSourceLookup && r.PathValue("repo") == "source" {
					http.Error(w, "upstream error", http.StatusInternalServerError)
					return
				}
				ids := map[string]int{"source": 2, "dependency": 3, "shared": 4}
				_, _ = fmt.Fprintf(w, `{"id":%d}`, ids[r.PathValue("repo")])
			})
			scopes := make(chan []int64, 2)
			mux.HandleFunc("POST /api/v3/app/installations/1/access_tokens", func(w http.ResponseWriter, r *http.Request) {
				var scope struct {
					RepositoryIDs []int64 `json:"repository_ids"`
				}
				if err := json.NewDecoder(r.Body).Decode(&scope); err != nil {
					http.Error(w, "invalid token request", http.StatusBadRequest)
					return
				}
				scopes <- scope.RepositoryIDs
				token := "bootstrap"
				if len(scope.RepositoryIDs) > 0 {
					token = "fresh"
				}
				_, _ = fmt.Fprintf(w, `{"token":%q,"expires_at":%q}`, token, expiry.Format(time.RFC3339))
			})
			server := httptest.NewServer(mux)
			t.Cleanup(server.Close)
			t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", server.URL+"/api/v3")

			repo := &api.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: "builds"},
				Spec:       api.RepositorySpec{URL: "https://github.com/org/source"},
			}
			run := tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{
				Name: "build", Namespace: repo.Namespace, UID: "one",
				Annotations: map[string]string{
					keys.Repository: repo.Name, keys.RepoURL: repo.Spec.URL,
					keys.GitAuthSecret: "auth", keys.InstallationID: "1",
					keys.ControllerInfo: string(annotation),
				},
			}}
			client := fake.NewSimpleClientset(
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: controller.Secret, Namespace: controllerNS},
					Data: map[string][]byte{
						keys.GithubApplicationID: []byte("123"),
						keys.GithubPrivateKey:    privateKeyPEM,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: controller.Configmap, Namespace: controllerNS},
					Data:       map[string]string{"secret-github-app-scope-extra-repos": "org/shared"},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name: "auth", Namespace: run.Namespace,
						OwnerReferences: []metav1.OwnerReference{{Name: run.Name, Kind: "PipelineRun", UID: run.UID}},
						Annotations:     map[string]string{keys.GitHubTokenExpiry: time.Now().Add(-time.Hour).Format(time.RFC3339)},
					},
					Data: map[string][]byte{
						".git-credentials":   []byte("https://git:old@github.com/org/source"),
						"git-provider-token": []byte("old"),
					},
				},
			)
			index := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
			assert.NilError(t, index.Add(repo))
			assert.NilError(t, index.Add(&api.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "dependency", Namespace: repo.Namespace},
				Spec:       api.RepositorySpec{URL: "https://github.com/org/dependency"},
			}))
			assert.NilError(t, index.Add(&api.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: controller.GlobalRepository, Namespace: controllerNS},
				Spec:       api.RepositorySpec{Settings: &api.Settings{GithubAppTokenScopeRepos: []string{"org/dependency"}}},
			}))
			ac := reconciler{client: client, pacLister: listers.NewRepositoryLister(index)}
			next, err := json.Marshal(run)
			assert.NilError(t, err)
			run.Spec.Status = tektonv1.PipelineRunSpecStatusPending
			old, err := json.Marshal(run)
			assert.NilError(t, err)
			response := ac.Admit(t.Context(), &admissionv1.AdmissionRequest{
				Resource:  metav1.GroupVersionResource{Group: "tekton.dev", Version: "v1", Resource: "pipelineruns"},
				Namespace: run.Namespace, Operation: admissionv1.Update,
				OldObject: runtime.RawExtension{Raw: old}, Object: runtime.RawExtension{Raw: next},
			})
			assert.Equal(t, response.Allowed, tt.wantAllowed, "%v", response.Result)
			secret, err := client.CoreV1().Secrets(run.Namespace).Get(t.Context(), "auth", metav1.GetOptions{})
			assert.NilError(t, err)
			if !tt.wantAllowed {
				assert.Assert(t, response.Result != nil)
				assert.Assert(t, strings.Contains(response.Result.Message, tt.wantErrContains), "got %q", response.Result.Message)
				assert.Equal(t, string(secret.Data["git-provider-token"]), "old")
				assert.Equal(t, len(scopes), 1)
				assert.Assert(t, len(<-scopes) == 0)
				return
			}
			assert.Equal(t, string(secret.Data["git-provider-token"]), "fresh")
			assert.Equal(t, secret.Annotations[keys.GitHubTokenExpiry], expiry.Format(time.RFC3339))
			assert.Equal(t, len(scopes), 2)
			assert.Assert(t, len(<-scopes) == 0)
			assert.DeepEqual(t, <-scopes, tt.wantScopes)
			assert.Assert(t, repo.Spec.Settings == nil)
		})
	}
}
