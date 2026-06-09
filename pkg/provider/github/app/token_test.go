package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
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

func Test_GenerateJWT(t *testing.T) {
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

			ip := NewInstallation(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost", strings.NewReader("")), run, &v1alpha1.Repository{}, &github.Provider{}, tt.namespace.GetName())
			token, err := ip.GenerateJWT(ctx)
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
func Test_GetAndUpdateInstallationID(t *testing.T) {
	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{testNamespace},
		Secret:     []*corev1.Secret{validSecret},
	}
	wantToken := "GOODTOKEN"
	wantID := 120
	badToken := "BADTOKEN"
	badID := 666
	missingID := 111

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
			Pac: &info.PacOpts{
				Settings: settings.Settings{},
			},
			Controller: &info.ControllerInfo{Secret: validSecret.GetName()},
		},
	}
	ctx = info.StoreCurrentControllerName(ctx, "default")
	ctx = info.StoreNS(ctx, testNamespace.GetName())

	ip := NewInstallation(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost", strings.NewReader("")), run, &v1alpha1.Repository{}, &github.Provider{}, testNamespace.GetName())
	jwtToken, err := ip.GenerateJWT(ctx)
	assert.NilError(t, err)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost", strings.NewReader(""))
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "repo",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://matched/by/incoming",
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
		testMethod(t, r, "POST")
		w.Header().Set("Authorization", "Bearer "+jwtToken)
		w.Header().Set("Accept", "application/vnd.github+json")
		_, _ = fmt.Fprintf(w, `{"token": "%s"}`, wantToken)
	})

	mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", badID), func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "POST")
		w.Header().Set("Authorization", "Bearer "+jwtToken)
		w.Header().Set("Accept", "application/vnd.github+json")
		_, _ = fmt.Fprintf(w, `{"token": "%s"}`, badToken)
	})

	t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", serverURL+"/api/v3")

	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Authorization", "Bearer 12345")
		w.Header().Set("Accept", "application/vnd.github+json")
		_, _ = fmt.Fprint(w, `{"total_count": 2,"repositories": [{"id":1,"html_url": "https://matched/by/incoming"},{"id":2,"html_url": "https://anotherrepo/that/would/failit"}]}`)
	})
	ip = NewInstallation(req, run, repo, gprovider, testNamespace.GetName())
	_, token, installationID, err := ip.GetAndUpdateInstallationID(ctx)
	assert.NilError(t, err)
	assert.Equal(t, installationID, int64(wantID))
	assert.Equal(t, *gprovider.Token, wantToken)
	assert.Equal(t, token, wantToken)
}

func TestGetAndUpdateInstallationIDIgnoresEnterpriseHostHeader(t *testing.T) {
	tdata := testclient.Data{
		Namespaces: []*corev1.Namespace{testNamespace},
		Secret:     []*corev1.Secret{validSecret},
	}
	wantToken := "GOODTOKEN"
	wantID := int64(120)

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
			Pac: &info.PacOpts{
				Settings: settings.Settings{},
			},
			Controller: &info.ControllerInfo{Secret: validSecret.GetName()},
		},
	}
	ctx = info.StoreCurrentControllerName(ctx, "default")
	ctx = info.StoreNS(ctx, testNamespace.GetName())

	jwtInstallation := &Install{run: run, namespace: testNamespace.GetName()}
	jwtToken, err := jwtInstallation.GenerateJWT(ctx)
	assert.NilError(t, err)

	mux.HandleFunc("/app/installations", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `[{"id": %d}]`, wantID)
	})
	mux.HandleFunc(fmt.Sprintf("/app/installations/%d/access_tokens", wantID), func(w http.ResponseWriter, r *http.Request) {
		testMethod(t, r, "POST")
		w.Header().Set("Authorization", "Bearer "+jwtToken)
		w.Header().Set("Accept", "application/vnd.github+json")
		_, _ = fmt.Fprintf(w, `{"token": "%s"}`, wantToken)
	})
	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"total_count": 1,"repositories": [{"id":1,"html_url": "https://github.com/org/repo"}]}`)
	})

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "repo",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://github.com/org/repo",
		},
	}

	gprovider := &github.Provider{APIURL: &serverURL, Run: run}
	gprovider.SetGithubClient(fakeghclient)
	t.Setenv("PAC_GIT_PROVIDER_TOKEN_APIURL", serverURL+"/api/v3")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost", strings.NewReader(""))
	req.Header.Set("X-GitHub-Enterprise-Host", "127.0.0.1:1")
	ip := NewInstallation(req, run, repo, gprovider, testNamespace.GetName())
	enterpriseURL, token, installationID, err := ip.GetAndUpdateInstallationID(ctx)
	assert.NilError(t, err)
	assert.Equal(t, enterpriseURL, "")
	assert.Equal(t, installationID, wantID)
	assert.Equal(t, token, wantToken)
}

func testMethod(t *testing.T, r *http.Request, want string) {
	t.Helper()
	if got := r.Method; got != want {
		t.Errorf("Request method: %v, want %v", got, want)
	}
}

func Test_ListRepos(t *testing.T) {
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	mux.HandleFunc("user/installations/1/repositories/2", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw)
	})

	mux.HandleFunc("/installation/repositories", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Authorization", "Bearer 12345")
		w.Header().Set("Accept", "application/vnd.github+json")
		_, _ = fmt.Fprint(w, `{"total_count": 1,"repositories": [{"id":1,"html_url": "https://matched/by/incoming"}]}`)
	})

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: "repo",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://matched/by/incoming",
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

	ctx, _ := rtesting.SetupFakeContext(t)
	gprovider := &github.Provider{}
	gprovider.SetGithubClient(fakeclient)
	ip := NewInstallation(httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost", strings.NewReader("")),
		&params.Run{}, repo, gprovider, testNamespace.GetName())
	exist, err := ip.matchRepos(ctx)
	assert.NilError(t, err)
	assert.Equal(t, exist, true)
}
