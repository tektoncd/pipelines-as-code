//go:build e2e

package test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tknpacdesc "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/describe"
	tknpaclist "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/listcmd"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	cli2 "github.com/openshift-pipelines/pipelines-as-code/test/pkg/cli"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	trepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGithubGHEPacCli(t *testing.T) {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	ctx := context.Background()
	ctx, runcnx, opts, ghprovider, err := tgithub.Setup(ctx, true, false)
	assert.NilError(t, err)

	entries := map[string]string{
		".tekton/info.yaml": fmt.Sprintf(`---
apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: pipeline
  annotations:
    pipelinesascode.tekton.dev/target-namespace: "%s"
    pipelinesascode.tekton.dev/on-target-branch: "[%s]"
    pipelinesascode.tekton.dev/on-event: "[%s]"
spec:
  pipelineSpec:
    tasks:
      - name: task
        taskSpec:
          steps:
            - name: task
              image: registry.access.redhat.com/ubi10/ubi-micro
              command: ["/bin/echo", "HELLOMOTO"]
`, targetNS, options.MainBranch, triggertype.PullRequest.String()),
	}

	repoinfo, resp, err := ghprovider.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Errorf("Repository %s not found in %s", opts.Organization, opts.Repo)
	}

	repository := &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: targetNS,
		},
		Spec: pacv1alpha1.RepositorySpec{
			URL: repoinfo.GetHTMLURL(),
		},
	}

	err = trepo.CreateNS(ctx, targetNS, runcnx)
	assert.NilError(t, err)

	err = trepo.CreateRepo(ctx, targetNS, runcnx, repository)
	assert.NilError(t, err)

	output, err := cli2.ExecCommand(runcnx, tknpacdesc.Root, "-n", targetNS, targetNS)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(output, "No runs has started."))

	output, err = cli2.ExecCommand(runcnx, tknpaclist.Root, "-n", targetNS)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(output, "NoRun"), "we could not detect NoRun in output: %s", output)

	targetRefName := fmt.Sprintf("refs/heads/%s",
		names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test"))

	sha, vref, err := tgithub.PushFilesToRef(ctx, ghprovider.Client(), "TestPacCli - "+targetRefName, repoinfo.GetDefaultBranch(), targetRefName, opts.Organization, opts.Repo, entries)
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Commit %s has been created and pushed to %s", sha, vref.GetURL())

	title := "TestPacCli - " + targetRefName
	number, err := tgithub.PRCreate(ctx, runcnx, ghprovider, opts.Organization, opts.Repo, targetRefName, repoinfo.GetDefaultBranch(), title)
	assert.NilError(t, err)

	g := tgithub.PRTest{
		Cnx:             runcnx,
		Options:         opts,
		Provider:        ghprovider,
		TargetNamespace: targetNS,
		TargetRefName:   targetRefName,
		PRNumber:        number,
		SHA:             sha,
		Logger:          runcnx.Clients.Log,
	}
	defer g.TearDown(ctx, t)

	runcnx.Clients.Log.Infof("Waiting for Repository to be updated")
	waitOpts := twait.Opts{
		RepoName:        targetNS,
		Namespace:       targetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{sha},
	}
	_, err = twait.UntilPipelineRunHasReason(ctx, runcnx.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)

	runcnx.Clients.Log.Infof("Check if we have the repository set as succeeded")

	counter := 0
	maxVal := 5
	for {
		output, err = cli2.ExecCommand(runcnx, tknpaclist.Root, "-n", targetNS)
		if err == nil && strings.Contains(output, "Succeeded") {
			runcnx.Clients.Log.Infof("We have the repository set as succeeded: %s", output)
			break
		}
		counter++
		if counter > maxVal {
			runcnx.Clients.Log.Errorf("We have waited for 5 minutes and we still do not have the repository set as succeeded: %s", output)
			t.Fail()
			break
		}
		runcnx.Clients.Log.Infof("Waiting 30s for tkn pac show success, %d/%d", counter, maxVal)
		time.Sleep(30 * time.Second)
	}
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -run TestPacCli$ ."
// End:
