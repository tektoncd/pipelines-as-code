//go:build e2e

package test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/env"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pacapi "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tknpacdelete "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/deleterepo"
	tknpacdesc "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/describe"
	tknpacgenerate "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/generate"
	tknpaclist "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/listcmd"
	tknpacresolve "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/resolve"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/git"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	tknpactest "github.com/openshift-pipelines/pipelines-as-code/test/pkg/cli"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
)

func TestGiteaWithCLI(t *testing.T) {
	t.Parallel()
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: "pull_request, push",
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun.yaml",
		},
		CheckForStatus: "success",
		ExpectEvents:   false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	output, err := tknpactest.ExecCommand(topts.ParamsRun, tknpaclist.Root, "pipelinerun", "list", "-n", topts.TargetNS)
	assert.NilError(t, err)
	match, err := regexp.MatchString(".*(Running|Succeeded)", output)
	assert.NilError(t, err)
	assert.Assert(t, match, "should have a Running or Succeeded pipelinerun in CLI listing: %s", output)

	output, err = tknpactest.ExecCommand(topts.ParamsRun, tknpacdesc.Root, "-n", topts.TargetNS)
	assert.NilError(t, err)
	match, err = regexp.MatchString(".*(Running|Succeeded)", output)
	assert.NilError(t, err)
	assert.Assert(t, match, "should have a Succeeded or Running pipelinerun in CLI describe and auto select the first one: %s", output)

	output, err = tknpactest.ExecCommand(topts.ParamsRun, tknpacdelete.Root, "-n", topts.TargetNS, "repository", topts.TargetNS, "--cascade")
	assert.NilError(t, err)
	expectedOutput := fmt.Sprintf("secret %s has been deleted\nsecret webhook-secret has been deleted\nrepository %s has been deleted\n", topts.TargetNS, topts.TargetNS)
	assert.Assert(t, output == expectedOutput, topts.TargetRefName, fmt.Sprintf("delete command should have this output: %s received: %s", expectedOutput, output))
}

func TestGiteaWithCLIGeneratePipeline(t *testing.T) {
	tests := []struct {
		name                 string
		generateOutputRegexp string
		wantErr              bool
		fileToAdd            map[string]string
	}{
		// we are not testing Java cause pom.xml is weird to get a very simple test
		{
			name: "CLI generate nodejs",
			fileToAdd: map[string]string{
				"package.json": `{
					"name": "whatisthis",
					"version": "1.0.0",
					"description": "",
					"main": "index.js",
					"scripts": {
					  "test": "echo \"Hello Friend\""
					},
					"author": "",
					"license": "BSD"
				  }`,
			},
			generateOutputRegexp: `We detected your repository uses the.*[Nn]odejs.*programming language`,
		},
		{
			name:                 "CLI generate python",
			generateOutputRegexp: `We detected your repository uses the.*Python.*programming language`,
			fileToAdd: map[string]string{
				"setup.py":    "# setup.py\n",
				"__init__.py": "# __init__.py\n",
			},
		},
		{
			name:                 "CLI generate golang",
			generateOutputRegexp: `We detected your repository uses the.*Go.*programming language`,
			fileToAdd: map[string]string{
				"go.mod": "module github.com/mylady/ismybike",
				"main.go": `package main

	import "fmt"

	func main() {
		fmt.Println("Hello World")
	}
`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topts := &tgitea.TestOpts{
				Regexp:      tgitea.SuccessRegexp,
				TargetEvent: "pull_request, push",
				YAMLFiles: map[string]string{
					".tekton/pr.yaml": "testdata/pipelinerun.yaml",
				},
				CheckForStatus: "success",
				ExpectEvents:   false,
			}
			_, f := tgitea.TestPR(t, topts)
			defer f()
			tmpdir, dirCleanups := tgitea.InitGitRepo(t)
			defer dirCleanups()
			_, err := git.RunGit(tmpdir, "remote", "add", "-t", topts.TargetNS, "-f", "origin", topts.GitCloneURL)
			assert.NilError(t, err)
			_, err = git.RunGit(tmpdir, "checkout", "-B", topts.TargetNS, "origin/"+topts.TargetNS)
			assert.NilError(t, err)

			for k, v := range tt.fileToAdd {
				newFile, err2 := os.Create(filepath.Join(tmpdir, k))
				assert.NilError(t, err2)
				_, err2 = newFile.WriteString(v)
				assert.NilError(t, err2)
				_, err2 = git.RunGit(tmpdir, "add", k)
				assert.NilError(t, err2)
			}

			output, err := tknpactest.ExecCommand(topts.ParamsRun, tknpacgenerate.Command, "--event-type", topts.TargetEvent,
				"--branch", topts.DefaultBranch, "--file-name", ".tekton/pr.yaml", "--overwrite")
			assert.NilError(t, err)
			assert.Assert(t, regexp.MustCompile(tt.generateOutputRegexp).MatchString(output))

			envRemove := env.PatchAll(t, map[string]string{"PAC_PROVIDER_TOKEN": "NOWORRIESBEHAPPY"})
			defer envRemove()
			topts.ParamsRun.Info.Pac = &info.PacOpts{}
			topts.ParamsRun.Info.Pac.Settings = settings.Settings{}
			_, err = tknpactest.ExecCommand(topts.ParamsRun, tknpacresolve.Command, "-f", ".tekton/pr.yaml", "-p", "revision=main")
			assert.NilError(t, err)

			// edit .tekton/pr.yaml file
			pryaml, err := os.ReadFile(filepath.Join(tmpdir, ".tekton/pr.yaml"))
			// replace with regexp
			reg := regexp.MustCompile(`.*- name: url\n.*`)
			// we need this for gitea to work so we do what we have to do and life goes on until
			b := reg.ReplaceAllString(string(pryaml),
				fmt.Sprintf("          - name: url\n            value: %s/%s\n          - name: sslVerify\n            value: false",
					topts.InternalGiteaURL,
					topts.PullRequest.Base.Repository.FullName))
			assert.NilError(t, err)
			err = os.WriteFile(filepath.Join(tmpdir, ".tekton/pr.yaml"), []byte(b), 0o600)
			assert.NilError(t, err)

			_, err = git.RunGit(tmpdir, "add", ".tekton/pr.yaml")
			assert.NilError(t, err)

			_, err = git.RunGit(tmpdir, "commit", "-a", "-m", "it's a beautiful day")
			assert.NilError(t, err)

			_, err = git.RunGit(tmpdir, "push", "origin", topts.TargetRefName)
			assert.NilError(t, err)

			tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, "", false)

			prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{
				LabelSelector: pacapi.EventType + "=pull_request",
			})
			assert.NilError(t, err)
			assert.Assert(t, len(prs.Items) >= 1, "should have at least 1 pipelineruns")
		})
	}
}
