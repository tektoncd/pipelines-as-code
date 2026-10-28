package info

import (
	_ "embed"
	"fmt"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	tcli "github.com/tektoncd/pipelines-as-code/pkg/test/cli"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	httptesting "github.com/tektoncd/pipelines-as-code/pkg/test/http"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

// script kiddies, don't get too excited, this has been randomly generated with random words.
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

func TestInfo(t *testing.T) {
	ns1 := "ns1"
	ns2 := "ns2"
	somerepositories := []*v1alpha1.Repository{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "repo1",
				Namespace: ns1,
			},
			Spec: v1alpha1.RepositorySpec{
				URL: "https://anurl.com",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "repo2",
				Namespace: ns2,
			},
			Spec: v1alpha1.RepositorySpec{
				URL: "https://somewhere.com",
			},
		},
	}
	tests := []struct {
		name             string
		wantErr          bool
		secret           *corev1.Secret
		repositories     []*v1alpha1.Repository
		controllerLabels map[string]string
		controllerNs     string
	}{
		{
			name:         "with github app",
			repositories: somerepositories,
			controllerNs: "pipelines-as-code",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pipelines-as-code-secret",
					Namespace: "pipelines-as-code",
				},
				Data: map[string][]byte{
					"github-application-id": []byte("12345"),
					"github-private-key":    []byte(fakePrivateKey),
				},
			},
		},
		{
			name:         "without github app",
			repositories: somerepositories,
			controllerNs: "pipelines-as-code",
		},
		{
			name:         "no repos",
			controllerNs: "pipelines-as-code",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespaces := []*corev1.Namespace{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: ns1,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: ns2,
					},
				},
			}
			if tt.controllerLabels == nil {
				tt.controllerLabels = map[string]string{
					"app.kubernetes.io/version": "testing",
				}
			}
			secrets := []*corev1.Secret{}
			if tt.secret != nil {
				secrets = []*corev1.Secret{tt.secret}
			}

			tdata := testclient.Data{
				Namespaces:   namespaces,
				Repositories: tt.repositories,
				Deployments: []*appsv1.Deployment{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "pipelines-as-code-controller",
							Labels:    tt.controllerLabels,
							Namespace: tt.controllerNs,
						},
					},
				},
				Secret: secrets,
			}
			apiURL := "http://github.url"
			ghAppJSON := `{
				"installations_count": 5,
				"description": "my beautiful app",
				"html_url": "http://github.url/app/myapp",
				"external_url": "http://myapp.url",
				"created_at": "2023-03-22T12:29:10Z",
				"name": "myapp"
			}`
			ghAppConfigJSON := `{
				"url": "https://anhook.url"
			}`
			httpTestClient := httptesting.MakeHTTPTestClient(map[string]map[string]string{
				apiURL + "/app": {
					"body": ghAppJSON,
					"code": "200",
				},
				apiURL + "/app/hook/config": {
					"body": ghAppConfigJSON,
					"code": "200",
				},
			})

			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreCurrentControllerName(ctx, "default")
			name := ""
			if tt.secret != nil {
				name = tt.secret.GetName()
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
					Kube:           stdata.Kube,
					HTTP:           *httpTestClient,
				},
				Info: info.Info{
					Controller: &info.ControllerInfo{
						Secret: name,
					},
				},
			}

			io, out := tcli.NewIOStream()
			err := install(ctx, cs, io, apiURL)
			assert.NilError(t, err)
			golden.Assert(t, out.String(), fmt.Sprintf("%s.golden", t.Name()))
		})
	}
}
