//go:build e2e

package test

import (
	"context"
	"os"
	"regexp"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	pacrepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
)

// TestGiteaProvenanceForDefaultBranch tests the provenance feature of the PipelineRun.
// It fetches the PipelineRun definition from the default branch of the repository
// as configured on the git platform (e.g., main).
func TestGiteaProvenanceForDefaultBranch(t *testing.T) {
	topts := &tgitea.TestOpts{
		SkipEventsCheck:       true,
		TargetEvent:           triggertype.PullRequest.String(),
		Settings:              &v1alpha1.Settings{PipelineRunProvenance: "default_branch"},
		NoPullRequestCreation: true,
	}
	verifyProvenance(t, topts, "HELLOMOTO", "step-task", false)
}

// TestGiteaProvenanceForSource tests the provenance feature of the PipelineRun.
// It fetches the PipelineRun definition from the source branch of where the event has been triggered.
func TestGiteaProvenanceForSource(t *testing.T) {
	topts := &tgitea.TestOpts{
		SkipEventsCheck:       true,
		TargetEvent:           triggertype.PullRequest.String(),
		Settings:              &v1alpha1.Settings{PipelineRunProvenance: "source"},
		NoPullRequestCreation: true,
	}
	verifyProvenance(t, topts, "testing provenance for source", "step-source-provenance-test", false)
}

// TestGiteaGlobalRepoProvenanceForDefaultBranch tests the provenance feature of the PipelineRun.
// It fetches the PipelineRun definition from the default branch of the repository
// as configured on the git platform (e.g., main).
// In this test, the provenance is enabled using a global repository instead of a local repository.
func TestGiteaGlobalRepoProvenanceForDefaultBranch(t *testing.T) {
	topts := &tgitea.TestOpts{
		SkipEventsCheck:       true,
		TargetEvent:           triggertype.PullRequest.String(),
		NoPullRequestCreation: true,
		Settings:              &v1alpha1.Settings{},
	}

	verifyProvenance(t, topts, "HELLOMOTO", "step-task", true)
}

// TestGiteaGlobalAndLocalRepoProvenance verifies the provenance feature of the PipelineRun,
// ensuring that when provenance is configured at both the global and local repository levels,
// the local repository settings take precedence. This end-to-end test confirms that behavior.
func TestGiteaGlobalAndLocalRepoProvenance(t *testing.T) {
	topts := &tgitea.TestOpts{
		SkipEventsCheck:       true,
		TargetEvent:           triggertype.PullRequest.String(),
		NoPullRequestCreation: true,
		Settings: &v1alpha1.Settings{
			PipelineRunProvenance: "source",
		},
	}

	verifyProvenance(t, topts, "testing provenance for source", "step-source-provenance-test", true)
}

func verifyProvenance(t *testing.T, topts *tgitea.TestOpts, expectedOutput, cName string, isGlobal bool) {
	if isGlobal {
		ctx := context.Background()
		topts.ParamsRun, topts.Opts, topts.GiteaCNX, _ = tgitea.Setup(ctx)
		assert.NilError(t, topts.ParamsRun.Clients.NewClients(ctx, &topts.ParamsRun.Info))
		topts.TargetRefName = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
		topts.TargetNS = topts.TargetRefName
		ctx, err := cctx.GetControllerCtxInfo(ctx, topts.ParamsRun)
		assert.NilError(t, err)
		assert.NilError(t, pacrepo.CreateNS(ctx, topts.TargetNS, topts.ParamsRun))

		globalNs := info.GetNS(ctx)
		err = tgitea.CreateCRD(ctx, topts,
			v1alpha1.RepositorySpec{
				Settings: &v1alpha1.Settings{
					PipelineRunProvenance: "default_branch",
				},
			},
			isGlobal)
		assert.NilError(t, err)

		defer func() {
			if os.Getenv("TEST_NOCLEANUP") != "true" {
				topts.ParamsRun.Clients.Log.Infof("Cleaning up global repo %s in %s", info.DefaultGlobalRepoName, globalNs)
				err = topts.ParamsRun.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(globalNs).Delete(
					context.Background(), info.DefaultGlobalRepoName, metav1.DeleteOptions{},
				)
				assert.NilError(t, err)
			}
		}()
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	targetRef := topts.TargetRefName
	prmap := map[string]string{".tekton/pr.yaml": "testdata/pipelinerun.yaml"}
	entries, err := payload.GetEntries(prmap, topts.TargetNS, topts.DefaultBranch, topts.TargetEvent, map[string]string{})
	assert.NilError(t, err)
	topts.TargetRefName = topts.DefaultBranch

	scmOpts := &scm.Opts{
		GitURL:             topts.GitCloneURL,
		Log:                topts.ParamsRun.Clients.Log,
		WebURL:             topts.GitHTMLURL,
		TargetRefName:      topts.DefaultBranch,
		BaseRefName:        topts.DefaultBranch,
		NoCheckOutFromBase: true,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)
	prmap = map[string]string{".tekton/notgonnatobetested.yaml": "testdata/pipelinerun-provenance-test.yaml"}
	entries, err = payload.GetEntries(prmap, topts.TargetNS, topts.DefaultBranch, topts.TargetEvent, map[string]string{})
	assert.NilError(t, err)
	scmOpts.TargetRefName = targetRef
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	pr, _, err := topts.GiteaCNX.Client().CreatePullRequest(topts.Opts.Organization, targetRef, forgejo.CreatePullRequestOption{
		Title: "Test Pull Request - " + targetRef,
		Head:  targetRef,
		Base:  options.MainBranch,
	})
	assert.NilError(t, err)
	topts.PullRequest = pr
	topts.ParamsRun.Clients.Log.Infof("PullRequest %s has been created", pr.HTMLURL)
	topts.CheckForStatus = "success"
	tgitea.WaitForStatus(t, topts, "heads/"+targetRef, "", false)

	// check the output of the PipelineRun logs
	err = twait.RegexpMatchingInPodLog(context.Background(), topts.ParamsRun, topts.TargetNS, "pipelinesascode.tekton.dev/event-type=pull_request",
		cName, *regexp.MustCompile(expectedOutput), "", 2, nil)
	assert.NilError(t, err)
}
