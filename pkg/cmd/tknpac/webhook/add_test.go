package webhook

import (
	"bytes"
	"io"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/cli"
	"github.com/tektoncd/pipelines-as-code/pkg/cli/prompt"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
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

func TestWebhookAdd(t *testing.T) {
	namespace1 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace1",
		},
	}
	repo1 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo1",
			Namespace: namespace1.GetName(),
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: nil,
			URL:         "https://anurl.com/owner/repo",
		},
	}

	namespace2 := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "namespace2",
		},
	}
	secret2 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret2",
			Namespace: namespace2.GetName(),
		},
		Data: map[string][]byte{
			"hub.token":      []byte(`Yzg5NzhlYmNkNTQwNzYzN2E2ZGExYzhkMTc4NjU0MjY3ZmQ2NmMeZg==`),
			"webhook.secret": []byte(`NTA3MDc=`),
		},
	}
	repo2 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo2",
			Namespace: namespace2.GetName(),
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: &v1alpha1.GitProvider{
				Secret: &v1alpha1.Secret{
					Name: "secret2",
					Key:  "hub.token",
				},
				WebhookSecret: &v1alpha1.Secret{
					Name: "repo2",
					Key:  "webhook.secret",
				},
			},
			URL: "https://github.com/owner/repo",
		},
	}

	repo3 := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "repo3",
			Namespace: namespace2.GetName(),
		},
		Spec: v1alpha1.RepositorySpec{
			GitProvider: &v1alpha1.GitProvider{},
			URL:         "https://github.com/owner/repo",
		},
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipelines-as-code-info",
			Namespace: namespace2.GetName(),
			Labels: map[string]string{
				"app.kubernetes.io/part-of": "pipelines-as-code",
			},
		},
		Data: map[string]string{
			"version":        "devel",
			"controller-url": "https://hook.pipelinesascode.com/WKR2cP3ug5K6A92T",
		},
	}

	tests := []struct {
		name         string
		askStubs     func(*prompt.AskStubber)
		namespaces   []*corev1.Namespace
		repositories []*v1alpha1.Repository
		secrets      []*corev1.Secret
		configMaps   []*corev1.ConfigMap
		repoName     string
		opts         *cli.PacCliOpts
		wantErr      bool
		wantMsg      string
		pacNamespace string
	}{{
		name: "Use webhook add command to add Github webhook when GithubApp is configured",
		askStubs: func(as *prompt.AskStubber) {
			as.StubOne("github")
			as.StubOne("true")
			as.StubOne("yes")
			as.StubOne("c8978ebcd5407637a6da1c8d178654267fd66a3b")
			as.StubOne(keys.PublicGithubAPIURL)
		},
		namespaces:   []*corev1.Namespace{namespace1},
		repositories: []*v1alpha1.Repository{repo1},
		repoName:     "repo1",
		opts: &cli.PacCliOpts{
			Namespace: namespace1.GetName(),
		},
		pacNamespace: namespace2.GetName(),
		configMaps:   []*corev1.ConfigMap{configMap},
		wantErr:      true, // returning error while creating webhook because it requires actual personal access token in order to connect github
		wantMsg:      "✓ Setting up GitHub Webhook for Repository https://anurl.com/owner/repo\n👀 I have detected a controller url: https://hook.pipelinesascode.com/WKR2cP3ug5K6A92T\nℹ ️You now need to create a GitHub personal access token, please checkout the docs at https://is.gd/KJ1dDH for the required scopes\n",
	}, {
		name:         "failed to configure webhook when git_provider secret is empty",
		namespaces:   []*corev1.Namespace{namespace2},
		repositories: []*v1alpha1.Repository{repo3},
		repoName:     "repo3",
		opts: &cli.PacCliOpts{
			Namespace: namespace2.GetName(),
		},
		wantErr:      false,
		wantMsg:      "! Can not configure webhook as git_provider secret is empty",
		pacNamespace: namespace2.GetName(),
	}, {
		name:         "list all repositories",
		namespaces:   []*corev1.Namespace{namespace2},
		repositories: []*v1alpha1.Repository{repo2},
		secrets:      []*corev1.Secret{secret2},
		configMaps:   []*corev1.ConfigMap{configMap},
		askStubs: func(as *prompt.AskStubber) {
			as.StubOne("true")
			as.StubOne("yes")
		},
		pacNamespace: namespace2.GetName(),
		repoName:     "",
		opts: &cli.PacCliOpts{
			Namespace: "test",
		},
		wantErr: true, // error out here saying no repo found because no repo created in test ns
	}, {
		name:         "list all repository in a namespace where none repo exist",
		namespaces:   []*corev1.Namespace{namespace1},
		repositories: []*v1alpha1.Repository{repo1},
		repoName:     "",
		opts: &cli.PacCliOpts{
			Namespace: "default",
		},
		pacNamespace: namespace2.GetName(),
		wantErr:      true, // error out here saying no repo found because no repo created in default ns
	}, {
		name:         "invalid repository",
		namespaces:   []*corev1.Namespace{namespace1},
		repositories: []*v1alpha1.Repository{repo1},
		repoName:     "invalidRepo",
		opts: &cli.PacCliOpts{
			Namespace: namespace1.GetName(),
		},
		pacNamespace: namespace2.GetName(),
		wantErr:      true, // error out with invalidRepo not found because it was not created
	}, {
		name: "Update secret token for existing github webhook",
		askStubs: func(as *prompt.AskStubber) {
			as.StubOne("github")
			as.StubOne("Yes")
			as.StubOne("https://hook.pipelinesascode.com/WKR2cP3ug5K6A92T")
			as.StubOne("53353")
			as.StubOne("https://github.com")
		},
		namespaces:   []*corev1.Namespace{namespace2},
		repositories: []*v1alpha1.Repository{repo2},
		secrets:      []*corev1.Secret{secret2},
		configMaps:   []*corev1.ConfigMap{configMap},
		repoName:     "repo2",
		opts: &cli.PacCliOpts{
			Namespace: namespace2.GetName(),
		},
		pacNamespace: namespace2.GetName(),
		wantMsg:      "✓ Setting up GitHub Webhook for Repository https://github.com/owner/repo\n👀 I have detected a controller url: https://hook.pipelinesascode.com/WKR2cP3ug5K6A92T\n",
		wantErr:      true, // error out because creating webhook in a repository requires valid provider token

	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			as, teardown := prompt.InitAskStubber()
			defer teardown()
			if tt.askStubs != nil {
				tt.askStubs(as)
			}
			tdata := testclient.Data{
				Namespaces:   tt.namespaces,
				Repositories: tt.repositories,
				Secret:       tt.secrets,
				ConfigMap:    tt.configMaps,
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
					Kube:           stdata.Kube,
				},
				Info: info.Info{Kube: &info.KubeOpts{Namespace: tt.opts.Namespace}},
			}
			io, out := newIOStream()
			err := add(ctx, tt.opts, cs, io, tt.repoName, tt.pacNamespace)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, tt.wantMsg, out.String())
			}
		})
	}
}
