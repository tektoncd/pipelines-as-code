package bootstrap

import (
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestTrustProviderHostname(t *testing.T) {
	const namespace = "pipelines-as-code"

	tests := []struct {
		name string
		// existingHosts is the value of the trusted-provider-hostnames key the
		// ConfigMap already holds; noConfigMap skips creating it entirely.
		existingHosts       string
		noConfigMap         bool
		githubAPIURL        string
		controllerConfigMap string
		wantHosts           string
		wantOutSub          string
		wantErrOutSub       string
	}{
		{
			name:         "adds the hostname to an empty allowlist",
			githubAPIURL: "https://ghe.example.com",
			wantHosts:    "ghe.example.com",
			wantOutSub:   "has been added",
		},
		{
			name:          "appends to the hosts already trusted",
			existingHosts: "other.example.com",
			githubAPIURL:  "https://ghe.example.com",
			wantHosts:     "other.example.com,ghe.example.com",
			wantOutSub:    "has been added",
		},
		{
			name:          "an already trusted hostname is not duplicated",
			existingHosts: "ghe.example.com",
			githubAPIURL:  "https://ghe.example.com",
			wantHosts:     "ghe.example.com",
			wantOutSub:    "has been added",
		},
		{
			name:                "targets the configmap of the controller",
			controllerConfigMap: "ghe-configmap",
			githubAPIURL:        "https://ghe.example.com",
			wantHosts:           "ghe.example.com",
			wantOutSub:          "ghe-configmap",
		},
		{
			// The App exists by the time this runs: a hostname that cannot be
			// trusted must warn with the corrective command, never fail the
			// bootstrap and leave a half provisioned install.
			name:          "an unusable hostname warns instead of failing",
			githubAPIURL:  "https://user@ghe.example.com",
			wantHosts:     "",
			wantErrOutSub: "kubectl -n pipelines-as-code patch configmap",
		},
		{
			name:          "a missing configmap warns instead of failing",
			noConfigMap:   true,
			githubAPIURL:  "https://ghe.example.com",
			wantErrOutSub: "refuses to send credentials to a host it does not trust",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			configMapName := info.DefaultPipelinesAscodeConfigmapName
			if tt.controllerConfigMap != "" {
				configMapName = tt.controllerConfigMap
			}
			tdata := testclient.Data{}
			if !tt.noConfigMap {
				tdata.ConfigMap = []*corev1.ConfigMap{{
					ObjectMeta: metav1.ObjectMeta{
						Name:      configMapName,
						Namespace: namespace,
					},
					Data: map[string]string{
						settings.TrustedProviderHostnamesKey: tt.existingHosts,
					},
				}}
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			run := &params.Run{
				Clients: clients.Clients{Kube: stdata.Kube},
			}
			if tt.controllerConfigMap != "" {
				run.Info.Controller = &info.ControllerInfo{Configmap: tt.controllerConfigMap}
			}
			ios, _, out, errOut := cli.IOTest()
			opts := &bootstrapOpts{
				targetNamespace: namespace,
				GithubAPIURL:    tt.githubAPIURL,
				ioStreams:       ios,
			}

			err := trustProviderHostname(ctx, run, opts)
			assert.NilError(t, err)

			if tt.wantOutSub != "" {
				assert.Assert(t, strings.Contains(out.String(), tt.wantOutSub),
					"expected %q in stdout, got %q", tt.wantOutSub, out.String())
			}
			if tt.wantErrOutSub != "" {
				assert.Assert(t, strings.Contains(errOut.String(), tt.wantErrOutSub),
					"expected %q in stderr, got %q", tt.wantErrOutSub, errOut.String())
			}
			if tt.noConfigMap {
				return
			}
			cm, err := stdata.Kube.CoreV1().ConfigMaps(namespace).Get(ctx, configMapName, metav1.GetOptions{})
			assert.NilError(t, err)
			assert.Equal(t, cm.Data[settings.TrustedProviderHostnamesKey], tt.wantHosts)
		})
	}
}
