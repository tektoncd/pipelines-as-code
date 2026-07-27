//go:build e2e

package test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v85/github"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// don't test concurrency limit here, just parallel pipeline.
func TestGiteaMultiplesParallelPipelines(t *testing.T) {
	maxParallel := 10
	yamlFiles := map[string]string{}
	for i := 0; i < maxParallel; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun.yaml"
	}
	topts := &tgitea.TestOpts{
		Regexp:               tgitea.SuccessRegexp,
		TargetEvent:          triggertype.PullRequest.String(),
		YAMLFiles:            yamlFiles,
		CheckForStatus:       "success",
		CheckForNumberStatus: maxParallel,
		ExpectEvents:         false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

// multiple pipelineruns in the same .tekton directory and a concurrency of 1.
func TestGiteaConcurrencyExclusivenessMultiplePipelines(t *testing.T) {
	numPipelines := 10
	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun.yaml"
	}
	topts := &tgitea.TestOpts{
		Regexp:               tgitea.SuccessRegexp,
		TargetEvent:          triggertype.PullRequest.String(),
		YAMLFiles:            yamlFiles,
		CheckForStatus:       "success",
		CheckForNumberStatus: numPipelines,
		ConcurrencyLimit:     github.Ptr(1),
		ExpectEvents:         false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

// multiple push to the same  repo, concurrency should q them.
func TestGiteaConcurrencyExclusivenessMultipleRuns(t *testing.T) {
	numPipelines := 1
	topts := &tgitea.TestOpts{
		TargetEvent:          triggertype.PullRequest.String(),
		YAMLFiles:            map[string]string{".tekton/pr.yaml": "testdata/pipelinerun.yaml"},
		CheckForNumberStatus: numPipelines,
		ConcurrencyLimit:     github.Ptr(1),
		ExpectEvents:         false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.DefaultBranch,
		PushForce:     true,
	}
	processed, err := payload.ApplyTemplate("testdata/pipelinerun-alt.yaml", map[string]string{
		"TargetNamespace": topts.TargetNS,
		"TargetBranch":    topts.DefaultBranch,
		"TargetEvent":     topts.TargetEvent,
		"PipelineName":    "pr",
		"Command":         "sleep 10",
	})
	assert.NilError(t, err)
	entries := map[string]string{".tekton/pr.yaml": processed}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	processed, err = payload.ApplyTemplate("testdata/pipelinerun-alt.yaml", map[string]string{
		"TargetNamespace": topts.TargetNS,
		"TargetBranch":    topts.DefaultBranch,
		"TargetEvent":     topts.TargetEvent,
		"PipelineName":    "pr",
		"Command":         "echo SUCCESS",
	})
	assert.NilError(t, err)
	entries = map[string]string{".tekton/pr.yaml": processed}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	// loop until we get the status
	gotPipelineRunPending := false
	for i := 0; i < 30; i++ {
		prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
		assert.NilError(t, err)

		// range over prs
		for _, pr := range prs.Items {
			// check for status
			status := pr.Spec.Status
			if status == "PipelineRunPending" {
				gotPipelineRunPending = true
				break
			}
		}
		if gotPipelineRunPending {
			topts.ParamsRun.Clients.Log.Info("Found PipelineRunPending in PipelineRuns")
			break
		}
		time.Sleep(5 * time.Second)
	}
	if !gotPipelineRunPending {
		t.Fatalf("Did not find PipelineRunPending in PipelineRuns")
	}

	topts.CheckForStatus = "success"
	tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, "", false)

	topts.Regexp = tgitea.SuccessRegexp
	tgitea.WaitForPullRequestCommentMatch(t, topts)
}

func TestGiteaConcurrencyOrderedExecution(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelineruns-ordered-execution.yaml",
		},
		CheckForStatus:       "success",
		CheckForNumberStatus: 3,
		ConcurrencyLimit:     github.Ptr(1),
		ExpectEvents:         false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	prs, err := twait.UntilPipelineRunsFinished(context.Background(), topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 3,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.PullRequest.Head.Sha},
	})
	assert.NilError(t, err)

	sort.PipelineRunSortByCompletionTime(prs)
	assert.Assert(t, strings.HasPrefix(prs[len(prs)-1].Name, "abc"))
	assert.Assert(t, strings.HasPrefix(prs[len(prs)-2].Name, "pqr"))
	assert.Assert(t, strings.HasPrefix(prs[len(prs)-3].Name, "xyz"))
}

func TestGiteaConfigMaxKeepRun(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun-max-keep-run-1.yaml",
		},
		CheckForStatus: "success",
		ExpectEvents:   false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	tgitea.PostCommentOnPullRequest(t, topts, "/test")
	tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, "", false)

	waitOpts := twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1, // 1 means 2 🙃
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.PullRequest.Head.Sha},
	}
	_, err := twait.UntilPipelineRunHasReason(context.Background(), topts.ParamsRun.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)

	time.Sleep(15 * time.Second) // "Evil does not sleep. It waits." - Galadriel

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)

	assert.Equal(t, len(prs.Items), 1, "should have only one pipelinerun, but we have: %d", len(prs.Items))
}

// TestGiteaConfigCancelInProgress will test the pipelinerun annotation
// `pipelinesascode.tekton.dev/cancel-in-progress: "true", it will first start
// one Pull Request which will run a PipelineRun and then send a /retest which
// should cancel the in progress one.
// It will create a new branch and push a new Pull Request with a PipelineRun of
// the same name of the first PR and make sure PipelineRun of the same name only
// acts on the same Pull Request and not on the one of the others.
// TestGiteaGlobalRepoConcurrencyLimit tests the concurrency_limit feature of the PipelineRun.
// It fetches the PipelineRun definition from the default branch of the repository
// as configured on the git platform (e.g., main).
// In this test, the concurrency_limit is enabled using a global repository instead of a local repository.
func TestGiteaGlobalRepoConcurrencyLimit(t *testing.T) {
	numPipelines := 10
	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun.yaml"
	}
	topts := &tgitea.TestOpts{
		TargetEvent:          triggertype.PullRequest.String(),
		YAMLFiles:            yamlFiles,
		CheckForNumberStatus: numPipelines,
		CheckForStatus:       "success",
	}

	tgitea.VerifyConcurrency(t, topts, github.Ptr(2))
}

// TestGiteaGlobalAndLocalRepoConcurrencyLimit verifies the concurrency_limit feature of the PipelineRun,
// ensuring that when concurrency_limit is defined in both global and local repository,
// the local repository limit takes precedence. This end-to-end test confirms that behavior.
func TestGiteaGlobalAndLocalRepoConcurrencyLimit(t *testing.T) {
	numPipelines := 10
	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun.yaml"
	}
	topts := &tgitea.TestOpts{
		TargetEvent:          triggertype.PullRequest.String(),
		YAMLFiles:            yamlFiles,
		CheckForNumberStatus: numPipelines,
		ConcurrencyLimit:     github.Ptr(3),
		CheckForStatus:       "success",
	}

	tgitea.VerifyConcurrency(t, topts, github.Ptr(2))
}
