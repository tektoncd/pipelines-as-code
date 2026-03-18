package adapter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	apincoming "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/incoming"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/env"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketdatacenter"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitlab"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
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

func Test_compareSecret(t *testing.T) {
	type args struct {
		incomingSecret string
		secretValue    string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "good/secret comparison",
			args: args{
				incomingSecret: "foo",
				secretValue:    "foo",
			},
			want: true,
		},
		{
			name: "bad/secret comparison",
			args: args{
				incomingSecret: "foo",
				secretValue:    "bar",
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareSecret(tt.args.incomingSecret, tt.args.secretValue); got != tt.want {
				t.Errorf("compareSecret() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_listener_detectIncoming(t *testing.T) {
	const goodURL = "https://matched/by/incoming"
	envRemove := env.PatchAll(t, map[string]string{"SYSTEM_NAMESPACE": "pipelinesascode"})
	defer envRemove()
	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: os.Getenv("SYSTEM_NAMESPACE"),
		},
	}

	type args struct {
		data             testclient.Data
		method           string
		queryURL         string
		queryRepository  string
		queryPipelineRun string
		querySecret      string
		queryBranch      string
		queryHeaders     http.Header
		queryNamespace   string
		incomingBody     string
		secretResult     map[string]string
	}
	tests := []struct {
		name          string
		args          args
		want          bool
		wantErr       bool
		wantSubstrErr string
	}{
		{
			name: "good/incoming",
			want: true,
			args: args{
				secretResult: map[string]string{"good-secret": "verysecrete"},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-good",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "good-secret",
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
				method:           "GET",
				queryURL:         "/incoming",
				queryRepository:  "test-good",
				querySecret:      "verysecrete",
				queryPipelineRun: "pipelinerun1",
				queryBranch:      "main",
			},
		},
		{
			name: "good/incoming with namespace",
			want: true,
			args: args{
				secretResult: map[string]string{"good-secret": "verysecrete"},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-good",
								Namespace: "bad-namespace",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "good-secret",
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "test-good",
								Namespace: "good-namespace",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "good-secret",
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
				method:           "GET",
				queryURL:         "/incoming",
				queryRepository:  "test-good",
				querySecret:      "verysecrete",
				queryPipelineRun: "pipelinerun1",
				queryBranch:      "main",
				queryNamespace:   "good-namespace",
			},
		},
		{
			name: "good/incoming with body",
			want: true,
			args: args{
				queryHeaders: http.Header{
					"Content-Type": []string{"application/json"},
				},
				secretResult: map[string]string{"good-secret": "verysecrete"},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-good",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "good-secret",
										},
										Params: []string{"the_best_superhero_is"},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
				method:           http.MethodPost,
				queryURL:         "/incoming",
				queryRepository:  "test-good",
				querySecret:      "verysecrete",
				queryPipelineRun: "pipelinerun1",
				queryBranch:      "main",
				incomingBody:     `{"params":{"the_best_superhero_is":"you"}}`,
			},
		},
		{
			name: "good/incoming with body partial params ",
			want: true,
			args: args{
				queryHeaders: http.Header{
					"Content-Type": []string{"application/json"},
				},
				secretResult: map[string]string{"good-secret": "verysecrete"},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-good",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "good-secret",
										},
										Params: []string{"the_best_superhero_is", "life_is_a_long_and_beautiful_journey"},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
				method:           http.MethodPost,
				queryURL:         "/incoming",
				queryRepository:  "test-good",
				querySecret:      "verysecrete",
				queryPipelineRun: "pipelinerun1",
				queryBranch:      "main",
				incomingBody:     `{"params":{"the_best_superhero_is":"you"}}`,
			},
		},
		{
			name: "good/incoming with default secret key",
			want: true,
			args: args{
				secretResult: map[string]string{"incoming-secret": "verysecrete"},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-default-key",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "incoming-secret",
											// Key is not specified, should default to "secret"
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
				method:           http.MethodPost,
				queryURL:         "/incoming",
				queryRepository:  "test-default-key",
				querySecret:      "verysecrete",
				queryPipelineRun: "pipelinerun1",
				queryBranch:      "main",
			},
		},
		{
			name: "invalid incoming body",
			args: args{
				method:           http.MethodPost,
				queryURL:         "/incoming",
				queryRepository:  "test-good",
				querySecret:      "verysecrete",
				queryPipelineRun: "pipelinerun1",
				queryBranch:      "main",
				incomingBody:     `foobar`,
			},
			want:    false,
			wantErr: true,
		},
		{
			name: "bad/noincomingurl",
			args: args{
				queryURL: "/nowhere",
			},
			want: false,
		},
		{
			name: "bad/no repository in query",
			args: args{
				queryURL:         "/incoming",
				querySecret:      "verysecrete",
				queryPipelineRun: "pipelinerun1",
				queryBranch:      "main",
			},
			wantErr: true,
		},
		{
			name: "bad/no repository in query",
			args: args{
				queryURL:         "/incoming",
				querySecret:      "verysecrete",
				queryPipelineRun: "pipelinerun1",
				queryBranch:      "main",
				queryRepository:  "",
			},
			wantErr: true,
		},
		{
			name: "bad/no secret in query",
			args: args{
				queryURL:         "/incoming",
				queryRepository:  "repo",
				queryPipelineRun: "pr",
				querySecret:      "",
				queryBranch:      "branch",
			},
			wantErr: true,
		},
		{
			name: "bad/no pr in query",
			args: args{
				queryURL:         "/incoming",
				queryRepository:  "repo",
				queryPipelineRun: "",
				querySecret:      "secret",
				queryBranch:      "branch",
			},
			wantErr: true,
		},
		{
			name: "bad/no branch in query",
			args: args{
				queryURL:         "/incoming",
				queryRepository:  "repo",
				queryPipelineRun: "pr",
				querySecret:      "secret",
				queryBranch:      "",
			},
			wantErr: true,
		},
		{
			name: "bad/no matched repo",
			args: args{
				queryURL:         "/incoming",
				queryRepository:  "repo",
				queryPipelineRun: "pr",
				querySecret:      "secret",
				queryBranch:      "branch",
			},
			wantErr: true,
		},
		{
			name: "bad/multiple repos with name",
			args: args{
				queryURL:         "/incoming",
				queryRepository:  "repo",
				queryPipelineRun: "pr",
				querySecret:      "secret",
				queryBranch:      "main",
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "repo",
								Namespace: "bad-namespace",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "good-secret",
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
						{
							ObjectMeta: metav1.ObjectMeta{
								Name:      "repo",
								Namespace: "good-namespace",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "good-secret",
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "bad/no incomings",
			args: args{
				queryURL:         "/incoming",
				queryRepository:  "repo",
				queryPipelineRun: "pr",
				querySecret:      "secret",
				queryBranch:      "main",
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "repo",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"notmain"},
										Secret: v1alpha1.Secret{
											Name: "good-secret",
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "bad/no matched branch in incoming",
			args: args{
				queryURL:         "/incoming",
				queryRepository:  "test-good",
				queryPipelineRun: "pr",
				querySecret:      "secret",
				queryBranch:      "branch",
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-good",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "bad/not git provider type provided",
			args: args{
				queryURL:         "/incoming",
				queryRepository:  "repo",
				queryPipelineRun: "pr",
				querySecret:      "secret",
				queryBranch:      "main",
				secretResult:     map[string]string{"secret": "secret"},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "repo",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "secret",
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{},
							},
						},
					},
					Secret: []*corev1.Secret{{
						ObjectMeta: metav1.ObjectMeta{
							Name:      info.DefaultPipelinesAscodeSecretName,
							Namespace: testNamespace.Name,
						},
						Data: map[string][]byte{
							"github-application-id": []byte("274799"),
							"github-private-key":    []byte(fakePrivateKey),
						},
					}},
				},
			},
			wantErr: true,
		},
		{
			name: "bad/no matched secret",
			args: args{
				secretResult:     map[string]string{"secret": "verysecrete"},
				queryURL:         "/incoming",
				queryRepository:  "repo",
				queryPipelineRun: "pr",
				querySecret:      "secret",
				queryBranch:      "main",
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "repo",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "secret",
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "bad/no matched secret",
			args: args{
				secretResult:     map[string]string{"secret": "verysecrete"},
				queryURL:         "/incoming",
				queryRepository:  "repo",
				queryPipelineRun: "pr",
				querySecret:      "secret",
				queryBranch:      "main",
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "repo",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "secret",
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "no git provider",
			args: args{
				queryURL:         "/incoming",
				queryRepository:  "repo",
				queryPipelineRun: "pr",
				querySecret:      "secret",
				queryBranch:      "main",
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "repo",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "secret",
										},
									},
								},
							},
						},
					},
					Secret: []*corev1.Secret{{
						ObjectMeta: metav1.ObjectMeta{
							Name:      info.DefaultPipelinesAscodeSecretName,
							Namespace: testNamespace.Name,
						},
						Data: map[string][]byte{
							"github-application-id": []byte("274799"),
							"github-private-key":    []byte(fakePrivateKey),
						},
					}},
				},
			},
			wantErr: true,
		},
		{
			name:          "bad/passed params is not in spec",
			wantSubstrErr: "is not allowed in incoming webhook CR",
			args: args{
				queryHeaders: http.Header{
					"Content-Type": []string{"application/json"},
				},
				secretResult: map[string]string{"good-secret": "verysecrete"},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-good",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "good-secret",
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
				method:           http.MethodPost,
				queryURL:         "/incoming",
				queryRepository:  "test-good",
				querySecret:      "verysecrete",
				queryPipelineRun: "pipelinerun1",
				queryBranch:      "main",
				incomingBody:     `{"params":{"the_best_superhero_is":"you"}}`,
			},
		},
		{
			name:          "bad/incoming with no accept",
			wantSubstrErr: "invalid content type, only application/json",
			args: args{
				secretResult: map[string]string{"good-secret": "verysecrete"},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-good",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "good-secret",
										},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
				method:           http.MethodPost,
				queryURL:         "/incoming",
				queryRepository:  "test-good",
				querySecret:      "verysecrete",
				queryBranch:      "main",
				queryPipelineRun: "pipelinerun1",
				incomingBody:     `{"params":{"the_best_superhero_is":"you"}}`,
			},
		},
		{
			name:          "bad/empty secret",
			wantSubstrErr: "is empty or key",
			args: args{
				queryHeaders: http.Header{
					"Content-Type": []string{"application/json"},
				},
				secretResult: map[string]string{"empty-secret": ""},
				data: testclient.Data{
					Repositories: []*v1alpha1.Repository{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: "test-good",
							},
							Spec: v1alpha1.RepositorySpec{
								URL: goodURL,
								Incomings: &[]v1alpha1.Incoming{
									{
										Targets: []string{"main"},
										Secret: v1alpha1.Secret{
											Name: "empty-secret",
										},
										Params: []string{"the_best_superhero_is"},
									},
								},
								GitProvider: &v1alpha1.GitProvider{
									Type: "github",
								},
							},
						},
					},
				},
				method:           http.MethodPost,
				queryURL:         "/incoming",
				queryRepository:  "test-good",
				querySecret:      "verysecrete",
				queryPipelineRun: "pipelinerun1",
				queryBranch:      "main",
				incomingBody:     `{"params":{"the_best_superhero_is":"you"}}`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = info.StoreCurrentControllerName(ctx, "default")
			ctx = info.StoreNS(ctx, testNamespace.GetName())
			cs, _ := testclient.SeedTestData(t, ctx, tt.args.data)
			client := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: cs.PipelineAsCode,
					Kube:           cs.Kube,
				},
				Info: info.Info{
					Controller: &info.ControllerInfo{
						Secret: info.DefaultPipelinesAscodeSecretName,
					},
				},
			}
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			kint := &kubernetestint.KinterfaceTest{
				GetSecretResult: tt.args.secretResult,
			}

			l := &listener{
				run:    client,
				logger: logger,
				kint:   kint,
			}
			event := info.NewEvent()

			// make a new request
			req := httptest.NewRequestWithContext(ctx, tt.args.method,
				fmt.Sprintf("http://localhost%s?repository=%s&secret=%s&pipelinerun=%s&branch=%s&namespace=%s", tt.args.queryURL,
					tt.args.queryRepository, tt.args.querySecret, tt.args.queryPipelineRun, tt.args.queryBranch, tt.args.queryNamespace),
				strings.NewReader(tt.args.incomingBody))
			req.Header = tt.args.queryHeaders
			got, _, err := l.detectIncoming(ctx, event, req, []byte(tt.args.incomingBody))
			if tt.wantSubstrErr != "" {
				assert.Assert(t, err != nil)
				assert.ErrorContains(t, err, tt.wantSubstrErr)
				return
			}
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.Equal(t, got, tt.want, "err = %v", err)
			assert.Equal(t, event.TargetPipelineRun, tt.args.queryPipelineRun)
		})
	}
}

func Test_listener_processIncoming(t *testing.T) {
	tests := []struct {
		name       string
		want       provider.Interface
		wantErr    bool
		targetRepo *v1alpha1.Repository
		wantOrg    string
		wantRepo   string
	}{
		{
			name:     "process/github",
			want:     github.New(),
			wantOrg:  "owner",
			wantRepo: "repo",
			targetRepo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: "https://forge/owner/repo",
					GitProvider: &v1alpha1.GitProvider{
						Type: "github",
					},
				},
			},
		},
		{
			name:     "process/gitlab",
			want:     &gitlab.Provider{},
			wantOrg:  "owner",
			wantRepo: "repo",
			targetRepo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: "https://forge/owner/repo",
					GitProvider: &v1alpha1.GitProvider{
						Type: "gitlab",
					},
				},
			},
		},
		{
			name:     "process/bitbucketcloud",
			want:     &bitbucketcloud.Provider{},
			wantOrg:  "owner",
			wantRepo: "repo",
			targetRepo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: "https://forge/owner/repo",
					GitProvider: &v1alpha1.GitProvider{
						Type: "bitbucket-cloud",
					},
				},
			},
		},
		{
			name:     "process/bitbucketdatacenter",
			want:     &bitbucketdatacenter.Provider{},
			wantOrg:  "owner",
			wantRepo: "repo",
			targetRepo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: "https://forge/owner/repo",
					GitProvider: &v1alpha1.GitProvider{
						Type: "bitbucket-datacenter",
					},
				},
			},
		},
		{
			name:     "process/gitea",
			want:     &gitea.Provider{},
			wantOrg:  "owner",
			wantRepo: "repo",
			targetRepo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: "https://forge/owner/repo",
					GitProvider: &v1alpha1.GitProvider{
						Type: "gitea",
					},
				},
			},
		},
		{
			name:     "process/forgejo",
			want:     &gitea.Provider{},
			wantOrg:  "owner",
			wantRepo: "repo",
			targetRepo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: "https://forge/owner/repo",
					GitProvider: &v1alpha1.GitProvider{
						Type: "forgejo",
					},
				},
			},
		},
		{
			name:    "error/unknown provider",
			wantErr: true,
			targetRepo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: "https://forge/owner/repo",
					GitProvider: &v1alpha1.GitProvider{
						Type: "unknown",
					},
				},
			},
		},
		{
			name:    "error/bad url",
			wantErr: true,
			targetRepo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: "hellomoto",
				},
			},
		},
		{
			name:    "error/not enough path in url",
			wantErr: true,
			targetRepo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL: "https://forge?owner=repo",
				},
			},
		},
		{
			name:     "No GitProvider is provided",
			want:     github.New(),
			wantOrg:  "owner",
			wantRepo: "repo",
			targetRepo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL:         "https://forge/owner/repo",
					GitProvider: nil,
				},
			},
		},
		{
			name:     "No GitProvider type is provided",
			want:     github.New(),
			wantOrg:  "owner",
			wantRepo: "repo",
			targetRepo: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					URL:         "https://forge/owner/repo",
					GitProvider: &v1alpha1.GitProvider{},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &params.Run{
				Info: info.Info{},
			}
			kint := &kubernetestint.KinterfaceTest{}
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			l := &listener{
				run: client, kint: kint, logger: logger,
			}
			event := info.NewEvent()
			pintf, _, err := l.processIncoming(event, tt.targetRepo)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.Assert(t, reflect.TypeOf(pintf).Elem() == reflect.TypeOf(tt.want).Elem())
			assert.Assert(t, event.Organization == tt.wantOrg)
			assert.Assert(t, event.Repository == tt.wantRepo)
		})
	}
}

func TestApplyIncomingParams(t *testing.T) {
	tests := []struct {
		name           string
		contentType    string
		payloadBody    []byte
		params         []string
		expected       apincoming.Payload
		expectedErrStr string
	}{
		{
			name:           "Invalid content type",
			contentType:    "text/plain",
			payloadBody:    []byte(`{"params": {"key": "value"}}`),
			params:         []string{"key"},
			expected:       apincoming.Payload{},
			expectedErrStr: "invalid content type, only application/json is accepted when posting a body",
		},
		{
			name:           "Invalid payload format",
			contentType:    "application/json",
			payloadBody:    []byte(`invalid json`),
			params:         []string{"key"},
			expected:       apincoming.Payload{},
			expectedErrStr: "error parsing incoming payload, not the expected format?: invalid character 'i' looking for beginning of value",
		},
		{
			name:           "Param not allowed",
			contentType:    "application/json",
			payloadBody:    []byte(`{"params": {"key": "value", "other": "value"}}`),
			params:         []string{"key"},
			expected:       apincoming.Payload{},
			expectedErrStr: "param other is not allowed in incoming webhook CR",
		},
		{
			name:        "All params allowed",
			contentType: "application/json",
			payloadBody: []byte(`{"params": {"key": "value", "other": "value"}}`),
			params:      []string{"key", "other"},
			expected:    apincoming.Payload{Params: map[string]any{"key": "value", "other": "value"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{Header: http.Header{"Content-Type": []string{tt.contentType}}}
			actual, err := applyIncomingParams(req, tt.payloadBody, tt.params)
			assert.DeepEqual(t, tt.expected, actual)
			if tt.expectedErrStr != "" {
				assert.ErrorContains(t, err, tt.expectedErrStr)
			}
		})
	}
}

func Test_detectIncoming_legacy_warning(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipelinesascode",
		},
	}
	ctx = info.StoreCurrentControllerName(ctx, "default")
	ctx = info.StoreNS(ctx, testNamespace.GetName())
	cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "test-good"},
				Spec: v1alpha1.RepositorySpec{
					URL: "https://matched/by/incoming",
					Incomings: &[]v1alpha1.Incoming{{
						Targets: []string{"main"},
						Secret:  v1alpha1.Secret{Name: "good-secret"},
						Params:  []string{"foo", "bar"},
					}},
					GitProvider: &v1alpha1.GitProvider{Type: "github"},
				},
			},
		},
	})
	client := &params.Run{
		Clients: clients.Clients{
			PipelineAsCode: cs.PipelineAsCode,
			Kube:           cs.Kube,
		},
		Info: info.Info{
			Controller: &info.ControllerInfo{Secret: info.DefaultPipelinesAscodeSecretName},
		},
	}
	tests := []struct {
		name          string
		req           *http.Request
		body          []byte
		expectWarning bool
	}{
		{
			name: "legacy mode - params in URL",
			req: httptest.NewRequestWithContext(ctx, http.MethodPost,
				"http://localhost/incoming?repository=test-good&secret=verysecrete&pipelinerun=pipelinerun1&branch=main",
				strings.NewReader("")),
			body:          nil,
			expectWarning: true,
		},
		{
			name: "new mode - params in JSON body",
			req: func() *http.Request {
				payload := `{
					"repository": "test-good",
					"branch": "main",
					"pipelinerun": "pipelinerun2",
					"secret": "verysecrete",
					"params": {"foo": "bar"}
				}`
				r := httptest.NewRequestWithContext(ctx, http.MethodPost,
					"http://localhost/incoming",
					strings.NewReader(payload))
				r.Header.Set("Content-Type", "application/json")
				return r
			}(),
			body:          []byte(`{"repository":"test-good","branch":"main","pipelinerun":"pipelinerun2","secret":"verysecrete","params":{"foo":"bar"}}`),
			expectWarning: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, observedLogs := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			kint := &kubernetestint.KinterfaceTest{GetSecretResult: map[string]string{"good-secret": "verysecrete"}}
			l := &listener{
				run:    client,
				logger: logger,
				kint:   kint,
			}
			event := info.NewEvent()
			got, _, err := l.detectIncoming(ctx, event, tt.req, tt.body)
			assert.NilError(t, err)
			assert.Assert(t, got)
			found := false
			for _, entry := range observedLogs.All() {
				if strings.Contains(entry.Message, "[SECURITY] Incoming webhook used legacy URL-based secret passing") {
					found = true
					break
				}
			}
			if tt.expectWarning {
				assert.Assert(t, found, "expected security warning log for legacy URL-based secret passing")
			} else {
				assert.Assert(t, !found, "did not expect security warning log for new mode")
			}
		})
	}
}

func Test_detectIncoming_body_params_are_parsed(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipelinesascode",
		},
	}
	ctx = info.StoreCurrentControllerName(ctx, "default")
	ctx = info.StoreNS(ctx, testNamespace.GetName())
	cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "test-good"},
				Spec: v1alpha1.RepositorySpec{
					URL: "https://matched/by/incoming",
					Incomings: &[]v1alpha1.Incoming{{
						Targets: []string{"main"},
						Secret:  v1alpha1.Secret{Name: "good-secret"},
						Params:  []string{"foo", "bar"},
					}},
					GitProvider: &v1alpha1.GitProvider{Type: "github"},
				},
			},
		},
	})
	client := &params.Run{
		Clients: clients.Clients{
			PipelineAsCode: cs.PipelineAsCode,
			Kube:           cs.Kube,
		},
		Info: info.Info{
			Controller: &info.ControllerInfo{Secret: info.DefaultPipelinesAscodeSecretName},
		},
	}
	payload := `{
		"repository": "test-good",
		"branch": "main",
		"pipelinerun": "pipelinerun2",
		"secret": "verysecrete",
		"params": {"foo": "bar", "bar": "baz"}
	}`
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"http://localhost/incoming",
		strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	kint := &kubernetestint.KinterfaceTest{GetSecretResult: map[string]string{"good-secret": "verysecrete"}}
	l := &listener{
		run:    client,
		logger: zap.NewNop().Sugar(),
		kint:   kint,
	}
	event := info.NewEvent()
	got, _, err := l.detectIncoming(ctx, event, req, []byte(payload))
	assert.NilError(t, err)
	assert.Assert(t, got)
}

func Test_parseIncomingPayload(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		url           string
		headers       http.Header
		body          string
		wantPayload   incomingPayload
		wantErr       bool
		wantErrSubstr string
	}{
		{
			name:   "legacy mode with valid query params",
			method: http.MethodGet,
			url:    "http://localhost/incoming?repository=test-repo&branch=main&pipelinerun=my-pr&secret=mysecret&namespace=ns1",
			body:   "",
			wantPayload: incomingPayload{
				legacyMode:  true,
				RepoName:    "test-repo",
				Branch:      "main",
				PipelineRun: "my-pr",
				Secret:      "mysecret",
				Namespace:   "ns1",
			},
			wantErr: false,
		},
		{
			name:   "new mode with valid JSON body",
			method: http.MethodPost,
			url:    "http://localhost/incoming",
			headers: http.Header{
				"Content-Type": []string{"application/json"},
			},
			body: `{"repository":"test-repo","branch":"feature","pipelinerun":"pr-123","secret":"topsecret","namespace":"team-a","params":{"foo":"bar"}}`,
			wantPayload: incomingPayload{
				legacyMode:  false,
				RepoName:    "test-repo",
				Branch:      "feature",
				PipelineRun: "pr-123",
				Secret:      "topsecret",
				Namespace:   "team-a",
				Params:      map[string]any{"foo": "bar"},
			},
			wantErr: false,
		},
		{
			name:   "malformed JSON error",
			method: http.MethodPost,
			url:    "http://localhost/incoming",
			headers: http.Header{
				"Content-Type": []string{"application/json"},
			},
			body:          `{invalid json syntax`,
			wantErr:       true,
			wantErrSubstr: "invalid JSON body for incoming webhook",
		},
		{
			name:   "missing required fields in JSON",
			method: http.MethodPost,
			url:    "http://localhost/incoming",
			headers: http.Header{
				"Content-Type": []string{"application/json"},
			},
			body:          `{"repository":"test-repo","branch":"main"}`,
			wantErr:       true,
			wantErrSubstr: "missing required fields",
		},
		{
			name:   "fallback from invalid query params to JSON body",
			method: http.MethodPost,
			url:    "http://localhost/incoming?repository=incomplete",
			headers: http.Header{
				"Content-Type": []string{"application/json"},
			},
			body: `{"repository":"test-repo","branch":"develop","pipelinerun":"pr-456","secret":"secret123"}`,
			wantPayload: incomingPayload{
				legacyMode:  false,
				RepoName:    "test-repo",
				Branch:      "develop",
				PipelineRun: "pr-456",
				Secret:      "secret123",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), tt.method, tt.url, strings.NewReader(tt.body))
			if tt.headers != nil {
				req.Header = tt.headers
			}

			got, err := parseIncomingPayload(req, []byte(tt.body))

			if tt.wantErr {
				assert.Assert(t, err != nil, "expected error but got nil")
				if tt.wantErrSubstr != "" {
					assert.ErrorContains(t, err, tt.wantErrSubstr)
				}
				return
			}

			assert.NilError(t, err)
			assert.Equal(t, tt.wantPayload.legacyMode, got.legacyMode)
			assert.Equal(t, tt.wantPayload.RepoName, got.RepoName)
			assert.Equal(t, tt.wantPayload.Branch, got.Branch)
			assert.Equal(t, tt.wantPayload.PipelineRun, got.PipelineRun)
			assert.Equal(t, tt.wantPayload.Secret, got.Secret)
			assert.Equal(t, tt.wantPayload.Namespace, got.Namespace)

			if tt.wantPayload.Params != nil {
				assert.DeepEqual(t, tt.wantPayload.Params, got.Params)
			}
		})
	}
}
