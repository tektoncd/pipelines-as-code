package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

const fakePrivateKey = `-----BEGIN RSA PRIVATE KEY-----
MIICXAIBAAKBgQC6GorZBeri0eVERMZQDFh5E1RMPjFk9AevaWr27yJse6eiUlos
gY2L2vcZKLOrdvVR+TLLapIMFfg1E1qVr1iTHP3IiSCs1uW6NKDmxEQc9Uf/fG9c
i56tGmTVxLkC94AvlVFmgxtWfHdP3lF2O0EcfRyIi6EIbGkWDqWQVEQG2wIDAQAB
AoGAaKOd6FK0dB5Si6Uj4ERgxosAvfHGMh4n6BAc7YUd1ONeKR2myBl77eQLRaEm
DMXRP+sfDVL5lUQRED62ky1JXlDc0TmdLiO+2YVyXI5Tbej0Q6wGVC25/HedguUX
fw+MdKe8jsOOXVRLrJ2GfpKZ2CmOKGTm/hyrFa10TmeoTxkCQQDa4fvqZYD4vOwZ
CplONnVk+PyQETj+mAyUiBnHEeLpztMImNLVwZbrmMHnBtCNx5We10oCLW+Qndfw
Xi4LgliVAkEA2amSV+TZiUVQmm5j9yzon0rt1FK+cmVWfRS/JAUXyvl+Xh/J+7Gu
QzoEGJNAnzkUIZuwhTfNRWlzURWYA8BVrwJAZFQhfJd6PomaTwAktU0REm9ulTrP
vSNE4PBhoHX6ZOGAqfgi7AgIfYVPm+3rupE5a82TBtx8vvUa/fqtcGkW4QJAaL9t
WPUeJyx/XMJxQzuOe1JA4CQt2LmiBLHeRoRY7ephgQSFXKYmed3KqNT8jWOXp5DY
Q1QWaigUQdpFfNCrqwJBANLgWaJV722PhQXOCmR+INvZ7ksIhJVcq/x1l2BYOLw2
QsncVExbMiPa9Oclo5qLuTosS8qwHm1MJEytp3/SkB8=
-----END RSA PRIVATE KEY-----`

var testNamespace = &corev1.Namespace{
	ObjectMeta: metav1.ObjectMeta{
		Name: "pipelinesascode",
	},
}

var validSecret = &corev1.Secret{
	ObjectMeta: metav1.ObjectMeta{
		Name:      info.DefaultPipelinesAscodeSecretName,
		Namespace: testNamespace.GetName(),
	},
	Data: map[string][]byte{
		"github-application-id": []byte("274799"),
		"github-private-key":    []byte(fakePrivateKey),
	},
}

func TestGenerateJWT(t *testing.T) {
	namespaceWhereSecretNotInstalled := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
	}

	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipelinesascode",
		},
	}

	secretWithInavlidAppID := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      info.DefaultPipelinesAscodeSecretName,
			Namespace: testNamespace.Name,
		},
		Data: map[string][]byte{
			"github-application-id": []byte("abcdf"),
			"github-private-key":    []byte(fakePrivateKey),
		},
	}
	secretWithInvalidPrivateKey := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      info.DefaultPipelinesAscodeSecretName,
			Namespace: testNamespace.Name,
		},
		Data: map[string][]byte{
			"github-application-id": []byte("12345"),
			"github-private-key":    []byte("invalidprivatekey"),
		},
	}

	tests := []struct {
		name      string
		wantErr   bool
		secret    *corev1.Secret
		namespace *corev1.Namespace
	}{{
		name:      "secret not found",
		namespace: namespaceWhereSecretNotInstalled,
		secret:    &corev1.Secret{},
		wantErr:   true,
	}, {
		name:      "invalid github-application-id",
		namespace: testNamespace,
		secret:    secretWithInavlidAppID,
		wantErr:   true,
	}, {
		name:      "invalid private key",
		namespace: testNamespace,
		secret:    secretWithInvalidPrivateKey,
		wantErr:   true,
	}, {
		name:      "valid secret found",
		namespace: testNamespace,
		secret:    validSecret,
		wantErr:   false,
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, _ := logger.GetLogger()
			tdata := testclient.Data{
				Namespaces: []*corev1.Namespace{tt.namespace},
				Secret:     []*corev1.Secret{tt.secret},
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreCurrentControllerName(ctx, "default")
			secretName := ""
			if tt.secret != nil {
				secretName = tt.secret.GetName()
			}

			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			run := &params.Run{
				Clients: clients.Clients{
					Log:            logger,
					PipelineAsCode: stdata.PipelineAsCode,
					Kube:           stdata.Kube,
				},
				Info: info.Info{
					Controller: &info.ControllerInfo{
						Secret: secretName,
					},
				},
			}

			gh := github.New()
			gh.Run = run
			token, err := gh.GenerateJWT(ctx, tt.namespace.GetName(), stdata.Kube)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)

			if token == "" {
				t.Errorf("there should be a generated token")
			}
		})
	}
}

// Test_GetAndUpdateInstallationID tests we properly obtain the list of repos for a GitHub App and find a matching repo.
func TestGetAndUpdateInstallationID(t *testing.T) {
	enterpriseSecret := validSecret.DeepCopy()
	enterpriseSecret.Data["github-host"] = []byte("matched")
	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{testNamespace},
		Secret:     []*corev1.Secret{enterpriseSecret},
	}
	wantToken := "GOODTOKEN"
	wantID := 120
	badToken := "BADTOKEN"
	badID := 666
	missingID := 111
	orgName := "org"

	fakeghclient, mux, serverURL, teardown := ghtesthelper.SetupGH()
	defer teardown()

	mux.HandleFunc("/app/installations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Authorization", "Bearer 12345")
		w.Header().Set("Accept", "application/vnd.github+json")
		if r.URL.Query().Get("page") == "" {
			w.Header().Add("Link", `<https://api.github.com/app/installations/?page=1&per_page=1>; rel="first",`+`<https://api.github.com/app/installations/?page=2&per_page=1>; rel="next",`)
			_, _ = fmt.Fprintf(w, `[{"id":%d}]`, missingID)
		} else if r.URL.Query().Get("page") == "2" {
			w.Header().Add("Link", `<https://api.github.com/app/installations/?page=3&per_page=1>`)
			_, _ = fmt.Fprintf(w, `[{"id":%d}]`, wantID)
		}
	})

	ctx, _ := rtesting.SetupFakeContext(t)
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)
	logger, _ := logger.GetLogger()
	run := &params.Run{
		Clients: clients.Clients{
			Log:            logger,
			PipelineAsCode: stdata.PipelineAsCode,
			Kube:           stdata.Kube,
		},
		Info: info.Info{
			Controller: &info.ControllerInfo{Secret: validSecret.GetName()},
		},
	}
	ctx = info.StoreCurrentControllerName(ctx, "default")
	ctx = info.StoreNS(ctx, testNamespace.GetName())

	gh := github.New()
	gh.Run = run
	jwtToken, err := gh.GenerateJWT(ctx, testNamespace.GetName(), stdata.Kube)
	assert.NilError(t, err)
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "repo",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: fmt.Sprintf("https://matched/%s/incoming", orgName),
			Incomings: &[]v1alpha1.Incoming{
				{
					Targets: []string{"main"},
					Secret: v1alpha1.Secret{
						Name: "secret",
					},
				},
			},
		},
	}

	gprovider := &github.Provider{APIURL: &serverURL, Run: run}
	gprovider.SetGithubClient(fakeghclient)
	mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", wantID), func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r)
		w.Header().Set("Authorization", "Bearer "+jwtToken)
		w.Header().Set("Accept", "application/vnd.github+json")
		_, _ = fmt.Fprintf(w, `{"token": "%s"}`, wantToken)
	})

	mux.HandleFunc(fmt.Sprintf("/orgs/%s/installation", orgName), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id": %d}`, wantID)
	})

	mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", badID), func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r)
		w.Header().Set("Authorization", "Bearer "+jwtToken)
		w.Header().Set("Accept", "application/vnd.github+json")
		_, _ = fmt.Fprintf(w, `{"token": "%s"}`, badToken)
	})

	t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", serverURL+"/api/v3")

	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Authorization", "Bearer 12345")
		w.Header().Set("Accept", "application/vnd.github+json")
		_, _ = fmt.Fprintf(w,
			`{"total_count": 2,"repositories": [{"id":1,"html_url": "https://matched/%s/incoming"},{"id":2,"html_url": "https://anotherrepo/that/would/failit"}]}`,
			orgName)
	})
	ip := NewInstallation(run, repo, gprovider, testNamespace.GetName())
	_, token, installationID, err := ip.GetAndUpdateInstallationID(ctx)
	assert.NilError(t, err)
	assert.Equal(t, installationID, int64(wantID))
	assert.Equal(t, *gprovider.Token, wantToken)
	assert.Equal(t, token, wantToken)
}

func TestGetAndUpdateInstallationIDUsesConfiguredPublicEndpoint(t *testing.T) {
	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{testNamespace},
		Secret:     []*corev1.Secret{validSecret},
	}
	wantToken := "GOODTOKEN"
	wantID := int64(120)
	orgName := "org"
	repoName := "repo"

	fakeghclient, mux, serverURL, teardown := ghtesthelper.SetupGH()
	defer teardown()

	ctx, _ := rtesting.SetupFakeContext(t)
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)
	logger, _ := logger.GetLogger()
	run := &params.Run{
		Clients: clients.Clients{
			Log:            logger,
			PipelineAsCode: stdata.PipelineAsCode,
			Kube:           stdata.Kube,
		},
		Info: info.Info{
			Controller: &info.ControllerInfo{Secret: validSecret.GetName()},
		},
	}
	ctx = info.StoreCurrentControllerName(ctx, "default")
	ctx = info.StoreNS(ctx, testNamespace.GetName())

	gh := github.New()
	gh.Run = run
	jwtToken, err := gh.GenerateJWT(ctx, testNamespace.GetName(), stdata.Kube)
	assert.NilError(t, err)

	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/installation", orgName, repoName), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"id": %d}`, wantID)
	})
	mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", wantID), func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r)
		w.Header().Set("Authorization", "Bearer "+jwtToken)
		w.Header().Set("Accept", "application/vnd.github+json")
		_, _ = fmt.Fprintf(w, `{"token": "%s"}`, wantToken)
	})

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "repo",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: fmt.Sprintf("https://github.com/%s/%s", orgName, repoName),
		},
	}

	gprovider := &github.Provider{APIURL: &serverURL, Run: run}
	gprovider.SetGithubClient(fakeghclient)
	t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", serverURL+"/api/v3")

	ip := NewInstallation(run, repo, gprovider, testNamespace.GetName())
	enterpriseURL, token, installationID, err := ip.GetAndUpdateInstallationID(ctx)
	assert.NilError(t, err)
	assert.Equal(t, enterpriseURL, "")
	assert.Equal(t, installationID, wantID)
	assert.Equal(t, token, wantToken)
}

func testMethod(t *testing.T, r *http.Request) {
	want := "POST"
	t.Helper()
	if got := r.Method; got != want {
		t.Errorf("Request method: %v, want %v", got, want)
	}
}

func TestGetAndUpdateInstallationIDFallbacks(t *testing.T) {
	enterpriseSecret := validSecret.DeepCopy()
	enterpriseSecret.Data["github-host"] = []byte("matched")
	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{testNamespace},
		Secret:     []*corev1.Secret{enterpriseSecret},
	}
	wantToken := "GOODTOKEN"
	orgName := "org"
	repoName := "repo"
	userName := "user"
	orgID := int64(120)
	userID := int64(121)

	tests := []struct {
		name                string
		repoURL             string
		setupMux            func(mux *http.ServeMux, jwtToken string)
		wantErr             bool
		wantInstallationID  int64
		wantToken           string
		wantEnterpriseHost  string
		skip                bool
		expectedErrorString string
	}{
		{
			name:    "repo installation fails, org installation succeeds",
			repoURL: fmt.Sprintf("https://matched/%s/%s", orgName, repoName),
			setupMux: func(mux *http.ServeMux, jwtToken string) {
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/installation", orgName, repoName), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				})
				mux.HandleFunc(fmt.Sprintf("/orgs/%s/installation", orgName), func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprintf(w, `{"id": %d}`, orgID)
				})
				mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", orgID), func(w http.ResponseWriter, r *http.Request) {
					testMethod(t, r)
					w.Header().Set("Authorization", "Bearer "+jwtToken)
					w.Header().Set("Accept", "application/vnd.github+json")
					_, _ = fmt.Fprintf(w, `{"token": "%s"}`, wantToken)
				})
			},
			wantErr:            false,
			wantInstallationID: orgID,
			wantToken:          wantToken,
			wantEnterpriseHost: "https://matched",
		},
		{
			name:    "repo and org installation fail, user installation succeeds",
			repoURL: fmt.Sprintf("https://matched/%s/%s", userName, repoName),
			setupMux: func(mux *http.ServeMux, jwtToken string) {
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/installation", userName, repoName), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				})
				mux.HandleFunc(fmt.Sprintf("/orgs/%s/installation", userName), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				})
				mux.HandleFunc(fmt.Sprintf("/users/%s/installation", userName), func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprintf(w, `{"id": %d}`, userID)
				})
				mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", userID), func(w http.ResponseWriter, r *http.Request) {
					testMethod(t, r)
					w.Header().Set("Authorization", "Bearer "+jwtToken)
					w.Header().Set("Accept", "application/vnd.github+json")
					_, _ = fmt.Fprintf(w, `{"token": "%s"}`, wantToken)
				})
			},
			wantErr:            false,
			wantInstallationID: userID,
			wantToken:          wantToken,
			wantEnterpriseHost: "https://matched",
		},
		{
			name:    "all installations fail",
			repoURL: fmt.Sprintf("https://matched/%s/%s", orgName, repoName),
			setupMux: func(mux *http.ServeMux, _ string) {
				mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/installation", orgName, repoName), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				})
				mux.HandleFunc(fmt.Sprintf("/orgs/%s/installation", orgName), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				})
				mux.HandleFunc(fmt.Sprintf("/users/%s/installation", orgName), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNotFound)
				})
			},
			wantErr:             true,
			expectedErrorString: "could not find repository, organization or user installation",
		},
		{
			name:    "invalid repo url",
			repoURL: "https://invalid/url",
			setupMux: func(_ *http.ServeMux, _ string) {
			},
			wantErr:             true,
			expectedErrorString: "invalid repository URL path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip {
				t.Skip()
			}
			fakeghclient, mux, serverURL, teardown := ghtesthelper.SetupGH()
			defer teardown()

			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			logger, _ := logger.GetLogger()
			run := &params.Run{
				Clients: clients.Clients{
					Log:            logger,
					PipelineAsCode: stdata.PipelineAsCode,
					Kube:           stdata.Kube,
				},
				Info: info.Info{
					Controller: &info.ControllerInfo{Secret: validSecret.GetName()},
				},
			}
			ctx = info.StoreCurrentControllerName(ctx, "default")
			ctx = info.StoreNS(ctx, testNamespace.GetName())

			gh := github.New()
			gh.Run = run
			jwtToken, err := gh.GenerateJWT(ctx, testNamespace.GetName(), stdata.Kube)
			assert.NilError(t, err)

			tt.setupMux(mux, jwtToken)

			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "repo",
				},
				Spec: v1alpha1.RepositorySpec{
					URL: tt.repoURL,
				},
			}

			gprovider := &github.Provider{APIURL: &serverURL, Run: run}
			gprovider.SetGithubClient(fakeghclient)
			t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", serverURL)

			ip := NewInstallation(run, repo, gprovider, testNamespace.GetName())
			enterpriseHost, token, installationID, err := ip.GetAndUpdateInstallationID(ctx)

			if tt.wantErr {
				assert.Assert(t, err != nil)
				if tt.expectedErrorString != "" {
					assert.Assert(t, strings.Contains(err.Error(), tt.expectedErrorString), "expected error string '%s' not found in '%s'", tt.expectedErrorString, err.Error())
				}
				return
			}

			assert.NilError(t, err)
			assert.Equal(t, installationID, tt.wantInstallationID)
			assert.Equal(t, token, tt.wantToken)
			assert.Equal(t, enterpriseHost, tt.wantEnterpriseHost)
		})
	}
}

func TestGetAndUpdateInstallationIDRejectsUnconfiguredRepositoryHost(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = info.StoreNS(ctx, testNamespace.GetName())
	seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Secret: []*corev1.Secret{validSecret},
	})
	run := &params.Run{
		Clients: clients.Clients{
			Kube: seedData.Kube,
		},
		Info: info.Info{
			Controller: &info.ControllerInfo{Secret: validSecret.GetName()},
		},
	}
	repo := &v1alpha1.Repository{
		Spec: v1alpha1.RepositorySpec{
			URL: server.URL + "/owner/repo",
		},
	}

	ip := NewInstallation(run, repo, github.New(), testNamespace.GetName())
	enterpriseURL, token, installationID, err := ip.GetAndUpdateInstallationID(ctx)
	assert.ErrorContains(t, err, "has not been authenticated yet")
	assert.Equal(t, enterpriseURL, "")
	assert.Equal(t, token, "")
	assert.Equal(t, installationID, int64(0))
	assert.Equal(t, requests, 0)
}

func TestGetAndUpdateInstallationIDRejectsInvalidRepositoryURLs(t *testing.T) {
	tests := []struct {
		name        string
		repoURL     string
		wantErrText string
	}{
		{
			name:        "parse error",
			repoURL:     "https://github.com/%zz/repo",
			wantErrText: "failed to parse repository URL",
		},
		{
			name:        "http scheme",
			repoURL:     "http://github.com/owner/repo",
			wantErrText: "must use https without userinfo",
		},
		{
			name:        "userinfo",
			repoURL:     "https://user@github.com/owner/repo",
			wantErrText: "must use https without userinfo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, testNamespace.GetName())
			seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				Secret: []*corev1.Secret{validSecret},
			})
			run := &params.Run{
				Clients: clients.Clients{
					Kube: seedData.Kube,
				},
				Info: info.Info{
					Controller: &info.ControllerInfo{Secret: validSecret.GetName()},
				},
			}
			repo := &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: tt.repoURL,
				},
			}

			ip := NewInstallation(run, repo, github.New(), testNamespace.GetName())
			enterpriseURL, token, installationID, err := ip.GetAndUpdateInstallationID(ctx)
			assert.ErrorContains(t, err, tt.wantErrText)
			assert.Equal(t, enterpriseURL, "")
			assert.Equal(t, token, "")
			assert.Equal(t, installationID, int64(0))
		})
	}
}

func TestGetAndUpdateInstallationIDReturnsSetupErrors(t *testing.T) {
	tests := []struct {
		name        string
		secret      *corev1.Secret
		envAPIURL   string
		wantErrText string
	}{
		{
			name: "missing controller secret",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "different-secret",
					Namespace: testNamespace.GetName(),
				},
			},
			wantErrText: "not found",
		},
		{
			name:        "invalid token api url",
			secret:      validSecret,
			envAPIURL:   "https://example.com",
			wantErrText: "PAC_GIT_PROVIDER_TOKEN_APIURL must target a loopback IP address",
		},
		{
			name: "invalid application id",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      info.DefaultPipelinesAscodeSecretName,
					Namespace: testNamespace.GetName(),
				},
				Data: map[string][]byte{
					"github-application-id": []byte("invalid"),
					"github-private-key":    []byte(fakePrivateKey),
				},
			},
			wantErrText: "could not parse the github application_id number",
		},
		{
			name: "invalid private key",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      info.DefaultPipelinesAscodeSecretName,
					Namespace: testNamespace.GetName(),
				},
				Data: map[string][]byte{
					"github-application-id": []byte("12345"),
					"github-private-key":    []byte("not-a-private-key"),
				},
			},
			wantErrText: "failed to parse private key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envAPIURL != "" {
				t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", tt.envAPIURL)
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, testNamespace.GetName())
			seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				Secret: []*corev1.Secret{tt.secret},
			})
			run := &params.Run{
				Clients: clients.Clients{
					Kube: seedData.Kube,
				},
				Info: info.Info{
					Controller: &info.ControllerInfo{Secret: info.DefaultPipelinesAscodeSecretName},
				},
			}
			gh := github.New()
			gh.Run = run
			repo := &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: "https://github.com/owner/repo",
				},
			}

			ip := NewInstallation(run, repo, gh, testNamespace.GetName())
			enterpriseURL, token, installationID, err := ip.GetAndUpdateInstallationID(ctx)
			assert.ErrorContains(t, err, tt.wantErrText)
			assert.Equal(t, enterpriseURL, "")
			assert.Equal(t, token, "")
			assert.Equal(t, installationID, int64(0))
		})
	}
}

func TestGetAndUpdateInstallationIDHandlesInstallationAndTokenErrors(t *testing.T) {
	tests := []struct {
		name               string
		repoInstallation   string
		wantErrText        string
		wantEnterpriseURL  string
		wantToken          string
		wantInstallationID int64
	}{
		{
			name:             "installation has no id",
			repoInstallation: `{}`,
			wantErrText:      "github App installation found but contained no ID",
		},
		{
			name:               "token request fails",
			repoInstallation:   `{"id": 99}`,
			wantEnterpriseURL:  "",
			wantToken:          "",
			wantInstallationID: 99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mux, serverURL, teardown := ghtesthelper.SetupGH()
			defer teardown()
			t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", serverURL+"/api/v3")
			mux.HandleFunc("/repos/owner/repo/installation", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprint(w, tt.repoInstallation)
			})
			if tt.wantInstallationID != 0 {
				mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", tt.wantInstallationID), func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				})
			}

			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreNS(ctx, testNamespace.GetName())
			seedData, _ := testclient.SeedTestData(t, ctx, testclient.Data{
				Secret: []*corev1.Secret{validSecret},
			})
			logger, _ := logger.GetLogger()
			run := &params.Run{
				Clients: clients.Clients{
					Log:  logger,
					Kube: seedData.Kube,
				},
				Info: info.Info{
					Controller: &info.ControllerInfo{Secret: validSecret.GetName()},
				},
			}
			gh := github.New()
			gh.Run = run
			repo := &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: "https://github.com/owner/repo",
				},
			}

			ip := NewInstallation(run, repo, gh, testNamespace.GetName())
			enterpriseURL, token, installationID, err := ip.GetAndUpdateInstallationID(ctx)
			if tt.wantErrText != "" {
				assert.ErrorContains(t, err, tt.wantErrText)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, enterpriseURL, tt.wantEnterpriseURL)
			assert.Equal(t, token, tt.wantToken)
			assert.Equal(t, installationID, tt.wantInstallationID)
		})
	}
}
