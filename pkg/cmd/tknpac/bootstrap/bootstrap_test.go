package bootstrap

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tektoncd/pipelines-as-code/pkg/cli"
	"github.com/tektoncd/pipelines-as-code/pkg/cli/prompt"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	"github.com/tektoncd/pipelines-as-code/pkg/test/logger"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func newIOStream() (*cli.IOStreams, *bytes.Buffer) {
	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &cli.IOStreams{
		In:     io.NopCloser(in),
		Out:    out,
		ErrOut: errOut,
	}, out
}

func TestInstall(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
	logger, _ := logger.GetLogger()

	run := &params.Run{
		Clients: clients.Clients{
			PipelineAsCode: cs.PipelineAsCode,
			Log:            logger,
			Kube:           cs.Kube,
		},
		Info: info.Info{},
	}
	io, out := newIOStream()
	opts := &bootstrapOpts{ioStreams: io}
	err := install(ctx, run, opts)
	// get an error because i need to figure out how to fake dynamic client
	assert.Assert(t, err != nil)
	assert.Equal(t, "=> Checking if Pipelines-as-Code is installed.\n", out.String())
}

func TestDetectPacInstallation(t *testing.T) {
	testParams := []struct {
		name                  string
		namespace             string
		userProvidedNamespace string
		configMap             *corev1.ConfigMap
		wantInstalled         bool
		wantNamespace         string
		wantError             bool
		errorMsg              string
	}{
		{
			name:          "get configmap in pipeline-as-code namespace",
			namespace:     pacNS,
			configMap:     getConfigMapData(pacNS, "v0.17.2"),
			wantNamespace: pacNS,
			wantInstalled: true,
		}, {
			name:          "get configmap in openshift-pipelines namespace",
			namespace:     "openshift-pipelines",
			configMap:     getConfigMapData("openshift-pipelines", "v0.17.2"),
			wantNamespace: "openshift-pipelines",
			wantInstalled: true,
		}, {
			name:                  "get configmap present in different namespace other than default namespaces",
			namespace:             "test",
			userProvidedNamespace: "test",
			configMap:             getConfigMapData("test", "dev"),
			wantNamespace:         "test",
			wantInstalled:         true,
		}, {
			name:                  "configmap not in default namespace",
			namespace:             "test",
			userProvidedNamespace: "",
			configMap:             getConfigMapData("test", "v0.17.2"),
			wantError:             true,
			errorMsg:              "could not detect Pipelines as Code configmap on the cluster, please specify the namespace in which pac is installed: ConfigMap not found in default namespaces (\"openshift-pipelines\", \"pipelines-as-code\")",
			wantInstalled:         false,
		}, {
			name:                  "configmap not in default namespace with user provided namespace",
			namespace:             "test",
			userProvidedNamespace: "test1",
			configMap:             getConfigMapData("test", "v0.17.2"),
			wantError:             true,
			errorMsg:              "could not detect Pipelines as Code configmap in test1 namespace : configmaps \"pipelines-as-code-info\" not found, please reinstall",
			wantInstalled:         false,
		},
	}
	for _, tp := range testParams {
		t.Run(tp.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			logger, _ := logger.GetLogger()

			run := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: cs.PipelineAsCode,
					Log:            logger,
					Kube:           cs.Kube,
				},
				Info: info.Info{},
			}
			if tp.configMap != nil {
				if _, err := run.Clients.Kube.CoreV1().ConfigMaps(tp.namespace).Create(ctx, tp.configMap, metav1.CreateOptions{}); err != nil {
					t.Errorf("failed to create configmap: %v", err)
				}
			}
			installed, ns, err := DetectPacInstallation(ctx, tp.userProvidedNamespace, run)
			if err != nil {
				if !tp.wantError {
					t.Errorf("Not expecting error but got: %v", err)
				} else {
					assert.Equal(t, err.Error(), tp.errorMsg)
				}
			} else {
				assert.Equal(t, tp.wantInstalled, installed)
				assert.Equal(t, tp.wantNamespace, ns)
			}
		})
	}
}

func getConfigMapData(namespace, version string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      infoConfigMap,
			Namespace: namespace,
		},
		Data: map[string]string{
			"version": version,
		},
	}
}

func TestGetDashboardURL(t *testing.T) {
	testParams := []struct {
		name           string
		ingresses      []networkingv1.Ingress
		askYNResponse  bool
		surveyResponse string
		wantURL        string
		wantError      bool
		errorMsg       string
		askStubs       func(*prompt.AskStubber)
	}{
		{
			name: "detect dashboard in ingress with http",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(true)
			},
			ingresses: []networkingv1.Ingress{
				{
					Spec: networkingv1.IngressSpec{
						Rules: []networkingv1.IngressRule{
							{
								Host: "tekton.example.com",
								IngressRuleValue: networkingv1.IngressRuleValue{
									HTTP: &networkingv1.HTTPIngressRuleValue{
										Paths: []networkingv1.HTTPIngressPath{
											{
												Backend: networkingv1.IngressBackend{
													Service: &networkingv1.IngressServiceBackend{
														Name: tektonDashboardServiceName,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			askYNResponse: true,
			wantURL:       "http://tekton.example.com",
		},
		{
			name: "detect dashboard in ingress with https",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(true)
			},
			ingresses: []networkingv1.Ingress{
				{
					Spec: networkingv1.IngressSpec{
						TLS: []networkingv1.IngressTLS{{
							Hosts: []string{"tekton.example.com"},
						}},
						Rules: []networkingv1.IngressRule{
							{
								Host: "tekton.example.com",
								IngressRuleValue: networkingv1.IngressRuleValue{
									HTTP: &networkingv1.HTTPIngressRuleValue{
										Paths: []networkingv1.HTTPIngressPath{
											{
												Backend: networkingv1.IngressBackend{
													Service: &networkingv1.IngressServiceBackend{
														Name: tektonDashboardServiceName,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			askYNResponse: true,
			wantURL:       "https://tekton.example.com",
		},
		{
			name: "detect dashboard but user rejects it",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne(false)
				as.StubOne("https://blah.com")
			},
			ingresses: []networkingv1.Ingress{
				{
					Spec: networkingv1.IngressSpec{
						Rules: []networkingv1.IngressRule{
							{
								Host: "tekton.example.com",
								IngressRuleValue: networkingv1.IngressRuleValue{
									HTTP: &networkingv1.HTTPIngressRuleValue{
										Paths: []networkingv1.HTTPIngressPath{
											{
												Backend: networkingv1.IngressBackend{
													Service: &networkingv1.IngressServiceBackend{
														Name: tektonDashboardServiceName,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			askYNResponse: false,
			wantURL:       "https://blah.com",
		},
		{
			name: "no dashboard detected, user provides url",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("https://my-dashboard.example.org")
			},
			ingresses: []networkingv1.Ingress{},
			wantURL:   "https://my-dashboard.example.org",
		},
		{
			name:      "no dashboard detected, user provides empty url",
			ingresses: []networkingv1.Ingress{},
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("")
			},
			wantURL: "",
		},
		{
			name:      "no dashboard detected, user provides invalid url",
			ingresses: []networkingv1.Ingress{},
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("invalid url")
			},
			wantError: true,
			errorMsg:  "invalid url:",
		},
	}

	for _, tt := range testParams {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			cs, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			logger, _ := logger.GetLogger()

			run := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: cs.PipelineAsCode,
					Log:            logger,
					Kube:           cs.Kube,
				},
				Info: info.Info{},
			}

			// Create test ingresses
			for _, ing := range tt.ingresses {
				if _, err := run.Clients.Kube.NetworkingV1().Ingresses("").Create(ctx, &ing, metav1.CreateOptions{}); err != nil {
					t.Errorf("failed to create ingress: %v", err)
				}
			}

			as, teardown := prompt.InitAskStubber()
			defer teardown()

			if tt.askStubs != nil {
				tt.askStubs(as)
			}

			io, _ := newIOStream()
			opts := &bootstrapOpts{ioStreams: io}

			err := getDashboardURL(ctx, opts, run)
			if tt.wantError {
				assert.Assert(t, err != nil)
				assert.Assert(t, strings.Contains(err.Error(), tt.errorMsg))
			} else {
				assert.NilError(t, err)
				assert.Equal(t, tt.wantURL, opts.dashboardURL)
			}
		})
	}
}

func TestAddGithubAppFlag(t *testing.T) {
	opts := &bootstrapOpts{}
	cmd := &cobra.Command{Use: "test"}
	addGithubAppFlag(cmd, opts)

	assert.NilError(t, cmd.PersistentFlags().Set("github-organization-name", "myorg"))
	assert.Equal(t, opts.GithubOrganizationName, "myorg")

	assert.NilError(t, cmd.PersistentFlags().Set("github-application-name", "myapp"))
	assert.Equal(t, opts.GithubApplicationName, "myapp")

	assert.NilError(t, cmd.PersistentFlags().Set("route-url", "https://route.example.com"))
	assert.Equal(t, opts.RouteName, "https://route.example.com")

	assert.NilError(t, cmd.PersistentFlags().Set("webserver-port", "9090"))
	assert.Equal(t, opts.webserverPort, 9090)

	assert.NilError(t, cmd.PersistentFlags().Set("install-type", "github-enterprise-app"))
	assert.Equal(t, opts.providerType, "github-enterprise-app")

	assert.NilError(t, cmd.PersistentFlags().Set("force-configure", "true"))
	assert.Equal(t, opts.forceGitHubApp, true)

	flag := cmd.PersistentFlags().Lookup("github-api-url")
	assert.Assert(t, flag != nil)
}

func TestAddCommonFlags(t *testing.T) {
	io, _ := newIOStream()
	io.SetColorEnabled(true)
	cmd := &cobra.Command{Use: "test"}
	addCommonFlags(cmd, io)

	flag := cmd.PersistentFlags().Lookup("no-color")
	assert.Assert(t, flag != nil)
	assert.Equal(t, flag.DefValue, "false")
}
