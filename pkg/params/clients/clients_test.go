package clients

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	httptesthelper "github.com/tektoncd/pipelines-as-code/pkg/test/http"
	"gotest.tools/v3/assert"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	rtesting "knative.dev/pkg/reconciler/testing"
)

const testKubeConfig = `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: http://localhost:8080
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
    namespace: test-ns
  name: test-context
current-context: test-context
users:
- name: test-user
  user: {}
`

func writeTestKubeConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	assert.NilError(t, os.WriteFile(path, []byte(testKubeConfig), 0o600))
	return path
}

func TestClientsGetURL(t *testing.T) {
	tests := []struct {
		name       string
		remoteURLS map[string]map[string]string
		want       string
		wantErr    bool
		url        string
	}{
		{
			name: "good",
			remoteURLS: map[string]map[string]string{
				"http://blahblah": {
					"body": "hellomoto",
					"code": "200",
				},
			},
			want: "hellomoto",
			url:  "http://blahblah",
		},
		{
			name: "bad",
			remoteURLS: map[string]map[string]string{
				"http://blahblah": {
					"code": "404",
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			httpTestClient := httptesthelper.MakeHTTPTestClient(tt.remoteURLS)
			c := &Clients{
				HTTP: *httpTestClient,
			}
			got, err := c.GetURL(ctx, tt.url)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err, "Clients.GetURL() error = %v, wantErr %v", err, tt.wantErr)
			assert.Equal(t, string(got), tt.want)
		})
	}
}

func TestInitClients(t *testing.T) {
	c := &Clients{}
	c.InitClients()
	assert.Assert(t, c.consoleUIMutex != nil)
}

func TestKubeClient(t *testing.T) {
	c := &Clients{}
	cs, err := c.kubeClient(&rest.Config{Host: "http://localhost:8080"})
	assert.NilError(t, err)
	assert.Assert(t, cs != nil)

	_, err = c.kubeClient(&rest.Config{
		Host: "http://localhost:8080",
		TLSClientConfig: rest.TLSClientConfig{
			CAFile: "/does/not/exist",
		},
	})
	assert.Assert(t, err != nil)
}

func TestDynamicClient(t *testing.T) {
	c := &Clients{}
	dc, err := c.dynamicClient(&rest.Config{Host: "http://localhost:8080"})
	assert.NilError(t, err)
	assert.Assert(t, dc != nil)

	_, err = c.dynamicClient(&rest.Config{
		Host: "http://localhost:8080",
		TLSClientConfig: rest.TLSClientConfig{
			CAFile: "/does/not/exist",
		},
	})
	assert.Assert(t, err != nil)
}

func TestTektonClient(t *testing.T) {
	c := &Clients{}
	tc, err := c.tektonClient(&rest.Config{Host: "http://localhost:8080"})
	assert.NilError(t, err)
	assert.Assert(t, tc != nil)

	_, err = c.tektonClient(&rest.Config{
		Host: "http://localhost:8080",
		TLSClientConfig: rest.TLSClientConfig{
			CAFile: "/does/not/exist",
		},
	})
	assert.Assert(t, err != nil)
}

func TestPacClient(t *testing.T) {
	c := &Clients{}
	pc, err := c.pacClient(&rest.Config{Host: "http://localhost:8080"})
	assert.NilError(t, err)
	assert.Assert(t, pc != nil)

	_, err = c.pacClient(&rest.Config{
		Host: "http://localhost:8080",
		TLSClientConfig: rest.TLSClientConfig{
			CAFile: "/does/not/exist",
		},
	})
	assert.Assert(t, err != nil)
}

func TestKubeConfig(t *testing.T) {
	c := &Clients{}
	configPath := writeTestKubeConfig(t)

	cfg, err := c.kubeConfig(&info.Info{Kube: &info.KubeOpts{ConfigPath: configPath}})
	assert.NilError(t, err)
	assert.Assert(t, cfg != nil)
	assert.Equal(t, cfg.Host, "http://localhost:8080")

	_, err = c.kubeConfig(&info.Info{Kube: &info.KubeOpts{ConfigPath: "/does/not/exist"}})
	assert.Assert(t, err != nil)
}

func TestConsoleUIClient(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	c := &Clients{}
	dynClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	cui := c.consoleUIClient(ctx, dynClient, &info.Info{})
	assert.Assert(t, cui != nil)
}

func TestConsoleUIGetSet(t *testing.T) {
	c := &Clients{}
	c.SetConsoleUI(nil)
	assert.Equal(t, c.ConsoleUI(), nil)
}

func TestNewClientsAlreadyInitialized(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	c := &Clients{ClientInitialized: true}
	err := c.NewClients(ctx, &info.Info{Kube: &info.KubeOpts{}})
	assert.NilError(t, err)
}

func TestNewClients(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	configPath := writeTestKubeConfig(t)
	c := &Clients{}
	err := c.NewClients(ctx, &info.Info{Kube: &info.KubeOpts{ConfigPath: configPath}})
	assert.NilError(t, err)
	assert.Assert(t, c.ClientInitialized)
	assert.Assert(t, c.Kube != nil)
	assert.Assert(t, c.Tekton != nil)
	assert.Assert(t, c.PipelineAsCode != nil)
	assert.Assert(t, c.Dynamic != nil)
	assert.Assert(t, c.ConsoleUI() != nil)

	// invalid kubeconfig path should bubble up an error
	c2 := &Clients{}
	err = c2.NewClients(ctx, &info.Info{Kube: &info.KubeOpts{ConfigPath: "/does/not/exist"}})
	assert.Assert(t, err != nil)
}
