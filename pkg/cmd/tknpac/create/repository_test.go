package create

import (
	"fmt"
	"os"
	"strings"
	"testing"

	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/generate"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/git"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"gotest.tools/v3/assert"
	testfs "gotest.tools/v3/fs"
	"gotest.tools/v3/golden"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestGetRepoURL(t *testing.T) {
	tests := []struct {
		name       string
		wantErrStr string
		askStubs   func(*prompt.AskStubber)
		tdata      testclient.Data
		runInfo    info.Info
		gitinfo    git.Info
		repo       apipac.Repository
		wantStdout string
		event      info.Event
		wantURL    string
	}{
		{
			name:       "URL already set",
			wantStdout: "",
			event: info.Event{
				URL: "http://knockknock",
			},
			wantURL: "http://knockknock",
		},
		{
			name: "default to gitinfo",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("") // use default
			},
			gitinfo: git.Info{
				URL: "https://url/tartanpion",
			},
			wantURL: "https://url/tartanpion",
		},
		{
			name: "set from question",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("https://url/tartanpion")
			},
			wantURL: "https://url/tartanpion",
		},
		{
			name: "no url has been provided",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("")
			},
			wantErrStr: "no url has been provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tt.tdata)

			as, teardown := prompt.InitAskStubber()
			defer teardown()
			if tt.askStubs != nil {
				tt.askStubs(as)
			}
			io, _, _, _ := cli.IOTest()
			err := getRepoURL(&RepoOptions{
				Event:      &tt.event,
				Repository: &tt.repo,
				GitInfo:    &tt.gitinfo,
				IoStreams:  io,
				cliOpts:    &cli.PacCliOpts{},
				Run: &params.Run{
					Clients: clients.Clients{
						Kube: stdata.Kube,
					},
					Info: tt.runInfo,
				},
			})
			assert.Equal(t, tt.wantURL, tt.event.URL)

			if tt.wantErrStr != "" {
				assert.Equal(t, err.Error(), tt.wantErrStr)
				return
			}

			assert.NilError(t, err)
		})
	}
}

func TestGetNamespace(t *testing.T) {
	nshere := "tartanpion"
	nsnothere := "fantomas"

	tests := []struct {
		name       string
		wantErrStr string
		askStubs   func(*prompt.AskStubber)
		tdata      testclient.Data
		runInfo    info.Info
		gitinfo    git.Info
		repo       apipac.Repository
		wantStdout string
	}{
		{
			name: "ns already set",
			repo: apipac.Repository{
				ObjectMeta: metav1.ObjectMeta{Namespace: nshere},
			},
			wantStdout: "",
		},
		{
			name:       "change default to current git basename and suffix pipelines",
			wantStdout: "! Namespace tartanpion-pipelines is not found",
			gitinfo: git.Info{
				URL: "https://url/tartanpion",
			},
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("") // use default
				as.StubOne(true)
			},
			runInfo: info.Info{
				Kube: &info.KubeOpts{
					Namespace: "default",
				},
			},
		},
		{
			name: "error you need to create the namespace first",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("pasenvie") // use default
				as.StubOne(false)
			},
			wantStdout: "! Namespace pasenvie is not found",
			wantErrStr: "you need to create the target namespace first",
			runInfo: info.Info{
				Kube: &info.KubeOpts{},
			},
		},
		{
			name: "create ns here",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("") // use default
			},
			tdata: testclient.Data{
				Namespaces: []*corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: nshere,
						},
					},
				},
			},
			runInfo: info.Info{
				Kube: &info.KubeOpts{
					Namespace: nshere,
				},
			},
			wantStdout: "",
		},
		{
			name: "create ns not here",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("fantomas") // use default
				as.StubOne(true)
			},
			wantStdout: fmt.Sprintf("! Namespace %s is not found", nsnothere),
			runInfo: info.Info{
				Kube: &info.KubeOpts{},
			},
		},
		{
			name: "error on openshift-pipelines namespace via prompt",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("openshift-pipelines")
			},
			wantErrStr: "namespace openshift-pipelines is not supported as a target for repositories",
			runInfo: info.Info{
				Kube: &info.KubeOpts{},
			},
		},
		{
			name: "error on tekton-pipelines namespace via prompt",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("tekton-pipelines")
			},
			wantErrStr: "namespace tekton-pipelines is not supported as a target for repositories",
			runInfo: info.Info{
				Kube: &info.KubeOpts{},
			},
		},
		{
			name: "error on openshift system namespace prefix via prompt",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("openshift-foo")
			},
			wantErrStr: "cannot create a repository in namespace openshift-foo as system namespaces are not supported",
			runInfo: info.Info{
				Kube: &info.KubeOpts{},
			},
		},
		{
			name: "warn on kube system namespace via prompt",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("kube-system")
				as.StubOne(true)
			},
			wantStdout: "! Warning: namespace kube-system has prefix kube- which is a system namespace\n! Namespace kube-system is not found",
			runInfo: info.Info{
				Kube: &info.KubeOpts{},
			},
		},
		{
			name: "error on openshift-pipelines namespace via flag",
			repo: apipac.Repository{
				ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-pipelines"},
			},
			wantErrStr: "namespace openshift-pipelines is not supported as a target for repositories",
		},
		{
			name: "warn on kube system namespace via flag",
			repo: apipac.Repository{
				ObjectMeta: metav1.ObjectMeta{Namespace: "kube-system"},
			},
			wantStdout: "! Warning: namespace kube-system has prefix kube- which is a system namespace",
		},
		{
			name: "error on tekton-pipelines namespace via flag",
			repo: apipac.Repository{
				ObjectMeta: metav1.ObjectMeta{Namespace: "tekton-pipelines"},
			},
			wantErrStr: "namespace tekton-pipelines is not supported as a target for repositories",
		},
		{
			name: "error on openshift system namespace prefix via flag",
			repo: apipac.Repository{
				ObjectMeta: metav1.ObjectMeta{Namespace: "openshift-foo"},
			},
			wantErrStr: "cannot create a repository in namespace openshift-foo as system namespaces are not supported",
		},
		{
			name: "warn on tekton system namespace prefix via prompt",
			askStubs: func(as *prompt.AskStubber) {
				as.StubOne("tekton-foo")
				as.StubOne(true)
			},
			wantStdout: "! Warning: namespace tekton-foo has prefix tekton- which is a system namespace\n! Namespace tekton-foo is not found",
			runInfo: info.Info{
				Kube: &info.KubeOpts{},
			},
		},
		{
			name: "warn on tekton system namespace prefix via flag",
			repo: apipac.Repository{
				ObjectMeta: metav1.ObjectMeta{Namespace: "tekton-foo"},
			},
			wantStdout: "! Warning: namespace tekton-foo has prefix tekton- which is a system namespace",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tt.tdata)

			as, teardown := prompt.InitAskStubber()
			defer teardown()
			if tt.askStubs != nil {
				tt.askStubs(as)
			}
			io, _, stdout, _ := cli.IOTest()
			err := getOrCreateNamespace(ctx, &RepoOptions{
				Event:      info.NewEvent(),
				Repository: &tt.repo,
				GitInfo:    &tt.gitinfo,
				IoStreams:  io,
				cliOpts:    &cli.PacCliOpts{},
				Run: &params.Run{
					Clients: clients.Clients{
						Kube: stdata.Kube,
					},
					Info: tt.runInfo,
				},
			})
			assert.Assert(t, strings.TrimSpace(stdout.String()) == strings.TrimSpace(tt.wantStdout),
				"\"%s\" not equal to \"%s\"", strings.TrimSpace(stdout.String()), tt.wantStdout)

			if tt.wantErrStr != "" {
				assert.Equal(t, err.Error(), tt.wantErrStr)
				return
			}

			assert.NilError(t, err)
		})
	}
}

func TestCleanUpURL(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		wantURL string
	}{
		{
			name:    "normal url",
			repoURL: "https://github.com/openshift-pipelines/pipelines-as-code",
			wantURL: "https://github.com/openshift-pipelines/pipelines-as-code",
		},
		{
			name:    "url with creds",
			repoURL: "https://username:password@github.com/openshift-pipelines/pipelines-as-code",
			wantURL: "https://github.com/openshift-pipelines/pipelines-as-code",
		},
		{
			name:    "url with creds",
			repoURL: "http://username@github.com/openshift-pipelines/pipelines-as-code",
			wantURL: "http://github.com/openshift-pipelines/pipelines-as-code",
		},
		{
			name:    "url with creds and port",
			repoURL: "https://username:password@githubenteprise.company.com:8080/openshift-pipelines/pipelines-as-code",
			wantURL: "https://githubenteprise.company.com:8080/openshift-pipelines/pipelines-as-code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cleanupGitURL(tt.repoURL)
			assert.NilError(t, err)
			assert.Equal(t, got, tt.wantURL)
		})
	}
}

func TestGenerateTemplate(t *testing.T) {
	tests := []struct {
		name     string
		gitInfo  *git.Info
		eventURL string
	}{
		{
			name: "WithGitInfo",
			gitInfo: &git.Info{
				URL: "https://url/tartanpion",
			},
			eventURL: "",
		},
		{
			name:     "WithoutGitInfo",
			gitInfo:  &git.Info{},
			eventURL: "https://url/tartanpion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			askStubs := func(as *prompt.AskStubber) {
				as.StubOne("Yes")
			}
			//nolint
			io, _, _, _ := cli.IOTest()
			createOpts := &RepoOptions{
				GitInfo:   tt.gitInfo,
				IoStreams: io,
				cliOpts:   &cli.PacCliOpts{},
				Event: &info.Event{
					URL: tt.eventURL,
				},
			}
			as, teardown := prompt.InitAskStubber()
			defer teardown()
			askStubs(as)
			gopt := generate.MakeOpts()
			tmpfile := testfs.NewFile(t, t.Name())
			defer tmpfile.Remove()
			assert.NilError(t, os.Remove(tmpfile.Path()))
			gopt.FileName = tmpfile.Path()
			err := createOpts.generateTemplate(gopt)
			assert.NilError(t, err)
			content, err := os.ReadFile(tmpfile.Path())
			assert.NilError(t, err)
			golden.Assert(t, string(content), strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
		})
	}
}
