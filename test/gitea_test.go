//go:build e2e

package test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/google/go-github/v81/github"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/env"
	"gotest.tools/v3/golden"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"

	pacapi "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tknpacdelete "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/deleterepo"
	tknpacdesc "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/describe"
	tknpacgenerate "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/generate"
	tknpaclist "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/listcmd"
	tknpacresolve "github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/resolve"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/git"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	tknpactest "github.com/openshift-pipelines/pipelines-as-code/test/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/configmap"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	pacrepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/secret"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	rtesting "knative.dev/pkg/reconciler/testing"
)

// successRegexp will match a success text paac comment,sometime it includes html tags so we need to consider that.
var successRegexp = regexp.MustCompile(`.*Pipelines as Code CI.*has.*successfully.*validated your commit.*`)

func TestGiteaPullRequestTaskAnnotations(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipeline.yaml":                         "testdata/pipeline_in_tektondir.yaml",
			".other-tasks/task-referenced-internally.yaml":  "testdata/task_referenced_internally.yaml",
			".other-tasks/task2-referenced-internally.yaml": "testdata/task2_referenced_internally.yaml",
			".tekton/pr.yaml":                               "testdata/pipelinerun_remote_task_annotations.yaml",
		},
		CheckForStatus: "success",
		ExtraArgs: map[string]string{
			"RemoteTaskURL":  options.RemoteTaskURL,
			"RemoteTaskName": options.RemoteTaskName,
		},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

func TestGiteaUseDisplayName(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      regexp.MustCompile(`.*The Task name is Task.*`),
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun.yaml",
		},
		CheckForStatus: "success",
		ExtraArgs: map[string]string{
			"RemoteTaskURL":  options.RemoteTaskURL,
			"RemoteTaskName": options.RemoteTaskName,
		},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

func TestGiteaPullRequestPipelineAnnotations(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun_remote_pipeline_annotations.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "success",
		ExtraArgs: map[string]string{
			"RemoteTaskURL":  options.RemoteTaskURL,
			"RemoteTaskName": options.RemoteTaskName,
		},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

func TestGiteaPullRequestResolvePipelineOnlyAssociatedWithPipelineRunFilterted(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr1.yaml":       "testdata/pipelinerun1_remote_task_annotations.yaml",
			".tekton/pr2.yaml":       "testdata/pipelinerun2_remote_task_annotations.yaml",
			".tekton/pipeline1.yaml": "testdata/pipeline1_in_tektondir.yaml",
			".tekton/pipeline2.yaml": "testdata/pipeline2_in_tektondir.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "success",
		ExtraArgs: map[string]string{
			"RemoteTaskURL":  options.RemoteTaskURL,
			"RemoteTaskName": options.RemoteTaskName,
		},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

// TestGiteaPullRequestResolvedTektonParamsRemotePipeline see
// https://issues.redhat.com/browse/SRVKP-4070 for details
func TestGiteaPullRequestResolvedTektonParamsRemotePipeline(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml":       "testdata/pipelinerun_with_tekton_params.yaml",
			".tekton/pipeline.yaml": "testdata/pipeline_with_tekton_params.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "success",
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	// check the output of the PipelineRun logs
	err := twait.RegexpMatchingInPodLog(context.Background(),
		topts.ParamsRun,
		topts.TargetNS, "pipelinesascode.tekton.dev/event-type=pull_request", "step-task",
		*regexp.MustCompile("Hello " + topts.TargetRepoName), "", 2, nil)
	assert.NilError(t, err)
}

func TestGiteaPullRequestPrivateRepository(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipeline.yaml": "testdata/pipelinerun_git_clone_private-gitea.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "success",
	}
	ctx, f := tgitea.TestPR(t, topts)
	defer f()
	reg := regexp.MustCompile(".*successfully fetched git-clone task from default configured catalog Hub")
	maxLines := int64(1000)
	err := twait.RegexpMatchingInControllerLog(ctx, topts.ParamsRun, *reg, 20, "controller", &maxLines, nil)
	assert.NilError(t, err)
	tgitea.WaitForSecretDeletion(t, topts, topts.TargetRefName)
}

func TestGiteaStepActions(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipelinerun-stepaction.yaml": "testdata/pipelinerun-stepactions.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "success",
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	tgitea.WaitForSecretDeletion(t, topts, topts.TargetRefName)
}

// TestGiteaBadYamlReportingOnPR makes sure that we can catch a bad yaml file
// and report on PR, we only do updates and not creating a new comment all the
// time.
func TestGiteaBadYamlReportingOnPR(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pr-bad-validation.yaml": "testdata/failures/pipeline-validation.yaml"},
		ExpectEvents: true,
	}

	_, f := tgitea.TestPR(t, topts)
	defer f()
	topts.Regexp = regexp.MustCompile(`.*bad-valid | .json: cannot unmarshal array into Go struct field PipelineRunSpec.spec.pipelineSpec of type v1.PipelineSpec.*`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	comments, _, err := topts.GiteaCNX.Client().ListRepoIssueComments(topts.PullRequest.Base.Repository.Owner.UserName, topts.PullRequest.Base.Repository.Name, forgejo.ListIssueCommentOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(comments), 1, "should have only one comment")

	// sending a second time the comment should have been updated
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.DefaultBranch,
		PushForce:     true,
	}
	processed, err := payload.ApplyTemplate("testdata/failures/pipeline-validation.yaml", map[string]string{
		"TargetNamespace": topts.TargetNS,
		"TargetBranch":    topts.DefaultBranch,
		"TargetEvent":     topts.TargetEvent,
		"PipelineName":    "pr-a-second-time",
		"Command":         "sleep 10",
	})
	assert.NilError(t, err)
	entries := map[string]string{".tekton/pr-bad-validation.yaml": processed}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	comments, _, err = topts.GiteaCNX.Client().ListRepoIssueComments(topts.PullRequest.Base.Repository.Owner.UserName, topts.PullRequest.Base.Repository.Name, forgejo.ListIssueCommentOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(comments), 1, "should have only one comment")
}

func TestGiteaYamlReportingNotReportingNotTektonResources(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/randomcrd.yaml": "testdata/randomcrd.yaml"},
		ExpectEvents: true,
	}

	_, f := tgitea.TestPR(t, topts)
	defer f()
	comments, _, err := topts.GiteaCNX.Client().ListRepoIssueComments(topts.PullRequest.Base.Repository.Owner.UserName, topts.PullRequest.Base.Repository.Name, forgejo.ListIssueCommentOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(comments), 0, "should have zero comments")
}

// TestGiteaBadYaml we can't check pr status but this shows up in the
// controller, so let's dig ourself in there....  TargetNS is a random string, so
// it can only success if it matches it.
func TestGiteaBadYamlValidation(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pr-bad-format.yaml": "testdata/failures/bad-yaml.yaml"},
		ExpectEvents: true,
	}

	ctx, f := tgitea.TestPR(t, topts)
	defer f()
	maxLines := int64(1000)
	assert.NilError(t, twait.RegexpMatchingInControllerLog(ctx, topts.ParamsRun, *regexp.MustCompile(
		"cannot read the PipelineRun: pr-bad-format.yaml, error: yaml validation error: line 3: could not find expected ':'"),
		10, "controller", &maxLines, nil))
}

// TestGiteaInvalidSpecValues tests invalid field values of a PipelinRun and ensures that these
// validation errors are reported on UI.
func TestGiteaInvalidSpecValues(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:    triggertype.PullRequest.String(),
		YAMLFiles:      map[string]string{".tekton/pr-bad-format.yaml": "testdata/failures/invalid-timeouts-values-pipelinerun.yaml"},
		CheckForStatus: "failure",
		ExpectEvents:   true,
		Regexp:         regexp.MustCompile(options.InvalidYamlErrorPattern),
	}

	_, f := tgitea.TestPR(t, topts)
	defer f()
}

// don't test concurrency limit here, just parallel pipeline.
func TestGiteaMultiplesParallelPipelines(t *testing.T) {
	maxParallel := 10
	yamlFiles := map[string]string{}
	for i := 0; i < maxParallel; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun.yaml"
	}
	topts := &tgitea.TestOpts{
		Regexp:               successRegexp,
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
		Regexp:               successRegexp,
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

	topts.Regexp = successRegexp
	tgitea.WaitForPullRequestCommentMatch(t, topts)
}

func TestGiteaRetestAfterPush(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      regexp.MustCompile(`.*has <b>failed</b>`),
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/failures/pipelinerun-exit-1.yaml",
		},
		CheckForStatus: "failure",
		ExpectEvents:   false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	newyamlFiles := map[string]string{".tekton/pr.yaml": "testdata/pipelinerun.yaml"}
	entries, err := payload.GetEntries(newyamlFiles, topts.TargetNS, topts.DefaultBranch, topts.TargetEvent, map[string]string{})
	assert.NilError(t, err)

	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.DefaultBranch,
		PushForce:     true,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)
	topts.CheckForStatus = "success"
	tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, "", false)
}

func TestGiteaConfigMaxKeepRun(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
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
		RepoName:        topts.TargetNS,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1, // 1 means 2 🙃
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       topts.PullRequest.Head.Sha,
	}
	_, err := twait.UntilRepositoryUpdated(context.Background(), topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)

	time.Sleep(15 * time.Second) // “Evil does not sleep. It waits.” - Galadriel

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
func TestGiteaConfigCancelInProgress(t *testing.T) {
	prmap := map[string]string{".tekton/pr.yaml": "testdata/pipelinerun-cancel-in-progress.yaml"}
	topts := &tgitea.TestOpts{
		TargetEvent:    triggertype.PullRequest.String(),
		YAMLFiles:      prmap,
		CheckForStatus: "",
		ExpectEvents:   false,
		Regexp:         nil,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	time.Sleep(3 * time.Second) // “Evil does not sleep. It waits.” - Galadriel

	// wait a bit that the pipelinerun had created, then trigger a new run to test cancel-in-progress
	tgitea.PostCommentOnPullRequest(t, topts, "/test")

	time.Sleep(2 * time.Second) // “Evil does not sleep. It waits.” - Galadriel

	targetRef := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("cancel-in-progress")
	entries, err := payload.GetEntries(prmap, topts.TargetNS, topts.DefaultBranch, topts.TargetEvent, map[string]string{})
	assert.NilError(t, err)
	topts.TargetRefName = topts.DefaultBranch
	scmOpts := &scm.Opts{
		GitURL:             topts.GitCloneURL,
		Log:                topts.ParamsRun.Clients.Log,
		WebURL:             topts.GitHTMLURL,
		TargetRefName:      targetRef,
		BaseRefName:        topts.DefaultBranch,
		NoCheckOutFromBase: true,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	pr, _, err := topts.GiteaCNX.Client().CreatePullRequest(topts.Opts.Organization, topts.Opts.Repo, forgejo.CreatePullRequestOption{
		Title: "Test Pull Request - " + targetRef,
		Head:  targetRef,
		Base:  topts.DefaultBranch,
	})
	assert.NilError(t, err)
	topts.PullRequest = pr
	topts.ParamsRun.Clients.Log.Infof("PullRequest %s has been created", pr.HTMLURL)
	topts.CheckForStatus = "success"
	tgitea.WaitForStatus(t, topts, "heads/"+targetRef, "", false)

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)

	sort.PipelineRunSortByStartTime(prs.Items)
	assert.Equal(t, len(prs.Items), 3, "should have 2 pipelineruns, but we have: %d", len(prs.Items))
	cancelledPr := 0
	for _, pr := range prs.Items {
		if pr.GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason() == "Cancelled" {
			cancelledPr++
		}
	}
	assert.Equal(t, cancelledPr, 1, "only one pr should have been cancelled")

	// Test that cancelling works with /retest - use specific PipelineRun name to bypass success check
	tgitea.PostCommentOnPullRequest(t, topts, "/retest pr-cancel-in-progress")
	topts.ParamsRun.Clients.Log.Info("Waiting 10 seconds before a new retest")
	time.Sleep(10 * time.Second) // "Evil does not sleep. It waits." - Galadriel
	tgitea.PostCommentOnPullRequest(t, topts, "/retest pr-cancel-in-progress")
	tgitea.WaitForStatus(t, topts, "heads/"+targetRef, "", false)

	for _, pr := range prs.Items {
		if pr.GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason() == "Cancelled" {
			cancelledPr++
		}
	}
	assert.Equal(t, cancelledPr, 2, "two pr should have been cancelled")
}

func TestGiteaConfigCancelInProgressAfterPRClosed(t *testing.T) {
	prmap := map[string]string{".tekton/pr.yaml": "testdata/pipelinerun-cancel-in-progress.yaml"}
	topts := &tgitea.TestOpts{
		TargetEvent:    triggertype.PullRequest.String(),
		YAMLFiles:      prmap,
		CheckForStatus: "",
		ExpectEvents:   false,
		Regexp:         nil,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	time.Sleep(3 * time.Second) // “Evil does not sleep. It waits.” - Galadriel
	waitOpts := twait.Opts{
		RepoName:        topts.TargetRefName,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       topts.SHA,
	}
	err := twait.UntilPipelineRunCreated(context.Background(), topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)

	closed := forgejo.StateClosed
	_, _, err = topts.GiteaCNX.Client().EditPullRequest(topts.Opts.Organization, topts.Opts.Repo, topts.PullRequest.Index, forgejo.EditPullRequestOption{
		State: &closed,
		Body:  &topts.PullRequest.Body,
	})
	assert.NilError(t, err)

	topts.ParamsRun.Clients.Log.Info("Waiting 10 seconds to check things has been cancelled")
	time.Sleep(10 * time.Second) // “Evil does not sleep. It waits.” - Galadriel

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 1, "should have only one pipelinerun, but we have: %d", len(prs.Items))

	assert.Equal(t, prs.Items[0].GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason(), "Cancelled", "should have been cancelled")
}

func TestGiteaPush(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
		TargetEvent: "pull_request, push",
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun.yaml",
		},
		CheckForStatus: "success",
		ExpectEvents:   false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	merged, resp, err := topts.GiteaCNX.Client().MergePullRequest(topts.Opts.Organization, topts.Opts.Repo, topts.PullRequest.Index,
		forgejo.MergePullRequestOption{
			Title: "Merged with Panache",
			Style: "merge",
		},
	)
	assert.NilError(t, err)
	assert.Assert(t, resp.StatusCode < 400, resp)
	assert.Assert(t, merged)
	tgitea.WaitForStatus(t, topts, topts.PullRequest.Head.Sha, "", false)
	time.Sleep(5 * time.Second)
	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{
		LabelSelector: pacapi.EventType + "=push",
	})
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 1, "should have only one push pipelinerun")
}

func TestGiteaWithCLI(t *testing.T) {
	t.Parallel()
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
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
				Regexp:      successRegexp,
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

func TestGiteaConcurrencyOrderedExecution(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
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

	repo, err := topts.ParamsRun.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(topts.TargetNS).Get(context.Background(), topts.TargetNS, metav1.GetOptions{})
	assert.NilError(t, err)
	// check the last 3 update in RepositoryRunStatus are in order
	statusLen := len(repo.Status)
	assert.Assert(t, strings.HasPrefix(repo.Status[statusLen-3].PipelineRunName, "abc"))
	assert.Assert(t, strings.HasPrefix(repo.Status[statusLen-2].PipelineRunName, "pqr"))
	assert.Assert(t, strings.HasPrefix(repo.Status[statusLen-1].PipelineRunName, "xyz"))
	time.Sleep(time.Second * 10)
}

func TestGiteaOnPathChange(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml":       "testdata/pipelinerun-on-path-change.yaml",
			"doc/foo/bar/README.md": "README.md",
		},
		CheckForStatus: "success",
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

func TestGiteaBranchWithComma(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:   triggertype.PullRequest.String(),
		DefaultBranch: "branch,with,comma",
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun-target-branch-with-comma.yaml",
		},
		CheckForStatus: "success",
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

// TestGiteaOnPathChangeIgnore will test that pipelinerun is not triggered when
// a path is ignored but all other will do.
func TestGiteaOnPathChangeIgnore(t *testing.T) {
	// This should trigger a pipelinerun since we ignore the path
	// on-path-change-ignore: "[doc/foo/***.md]"
	// and we create a file doc/bar/README.md
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr2.yaml":  "testdata/pipelinerun-on-path-change-ignore.yaml",
			"doc/bar/README.md": "README.md",
		},
		CheckForStatus:       "success",
		CheckForNumberStatus: 1,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	// This should not trigger a pipelinerun since we have
	// on-path-change-ignore: "[doc/foo/***.md]"
	// and the file doc/foo/README.md is created
	topts2 := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr2.yaml":  "testdata/pipelinerun-on-path-change-ignore.yaml",
			"doc/foo/README.md": "README.md",
		},
		CheckForNumberStatus: 0,
	}
	_, f2 := tgitea.TestPR(t, topts2)
	defer f2()
}

// TestGiteaOnPathChangeAndOnPathChangeIgnore will test that
// on-path-change and on-path-change-ignore both work together.
func TestGiteaOnPathChangeAndOnPathChangeIgnore(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml":       "testdata/pipelinerun-on-path-change.yaml",
			".tekton/pr2.yaml":      "testdata/pipelinerun-on-path-change-and-ignore.yaml",
			"doc/foo/bar/README.md": "README.md",
		},
		CheckForStatus:       "success",
		CheckForNumberStatus: 1,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

func TestGiteaErrorSnippet(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun-error-snippet.yaml",
		},
		CheckForStatus: "failure",
		ExpectEvents:   false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	topts.Regexp = regexp.MustCompile(`Hey man i just wanna to say i am not such a failure, i am useful in my failure`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)
}

func TestGiteaErrorSnippetCustomLines(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun-error-snippet.yaml",
		},
		CheckForStatus:   "failure",
		ExpectEvents:     false,
		SkipEventsCheck:  true,
		StatusOnlyLatest: true,
	}
	topts.TargetRefName = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
	topts.TargetNS = topts.TargetRefName
	topts.ParamsRun, topts.Opts, topts.GiteaCNX, _ = tgitea.Setup(ctx)
	assert.NilError(t, topts.ParamsRun.Clients.NewClients(ctx, &topts.ParamsRun.Info))
	ctx, err := cctx.GetControllerCtxInfo(ctx, topts.ParamsRun)
	assert.NilError(t, err)
	assert.NilError(t, pacrepo.CreateNS(ctx, topts.TargetNS, topts.ParamsRun))
	cfgMapData := map[string]string{
		"error-log-snippet-number-of-lines": "5",
	}
	defer configmap.ChangeGlobalConfig(ctx, t, topts.ParamsRun, "pipelines-as-code", cfgMapData)()

	_, f := tgitea.TestPR(t, topts)
	defer f()

	topts.Regexp = regexp.MustCompile(`Hey man i just wanna to say i am not such a failure, i am useful in my failure`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	comments, _, err := topts.GiteaCNX.Client().ListRepoIssueComments(topts.PullRequest.Base.Repository.Owner.UserName, topts.PullRequest.Base.Repository.Name, forgejo.ListIssueCommentOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(comments) > 0)
	lastComment := comments[len(comments)-1]
	body := lastComment.Body

	// Keep only the content from `<h4>Failure snippet:</h4>` onwards, if present; otherwise, we cannot perform a comparison due to the random e2e test name.
	const marker = "<h4>Failure snippet:</h4>"
	if idx := strings.Index(body, marker); idx != -1 {
		body = body[idx:]
	}
	golden.Assert(t, body, strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
}

func TestGiteaOnPullRequestLabels(t *testing.T) {
	prName := "on-label"
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			fmt.Sprintf(".tekton/%s.yaml", prName): "testdata/pipelinerun-on-label.yaml",
		},
		ExpectEvents:         false,
		CheckForNumberStatus: 0,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	tgitea.AddLabelToIssue(t, topts, "bug")

	waitOpts := twait.Opts{
		RepoName:        topts.TargetNS,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1, // 1 means 2 🙃
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       topts.PullRequest.Head.Sha,
	}
	_, err := twait.UntilRepositoryUpdated(context.Background(), topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)

	topts.CheckForStatus = "success"
	tgitea.WaitForStatus(t, topts, topts.TargetRefName, "", true)

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)

	assert.Equal(t, len(prs.Items), 1, "should have only one pipelinerun, but we have: %d", len(prs.Items))

	repo, err := topts.ParamsRun.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(topts.TargetNS).Get(context.Background(), topts.TargetNS, metav1.GetOptions{})
	assert.NilError(t, err)
	twait.GoldenPodLog(context.Background(), t, topts.ParamsRun, topts.TargetNS,
		fmt.Sprintf("tekton.dev/pipelineRun=%s,tekton.dev/pipelineTask=task", repo.Status[0].PipelineRunName),
		"step-success", strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"), 2, nil)

	// Make sure the on-label pr has triggered and post status
	topts.Regexp = regexp.MustCompile(fmt.Sprintf("Pipelines as Code CI/%s.* has <b>successfully</b> validated your commit", prName))
	tgitea.WaitForPullRequestCommentMatch(t, topts)
}

func TestGiteaErrorSnippetWithSecret(t *testing.T) {
	var err error
	ctx := context.Background()
	topts := &tgitea.TestOpts{
		TargetRefName: names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test"),
	}
	topts.TargetNS = topts.TargetRefName
	topts.ParamsRun, topts.Opts, topts.GiteaCNX, err = tgitea.Setup(ctx)
	assert.NilError(t, err, fmt.Errorf("cannot do gitea setup: %w", err))
	ctx, err = cctx.GetControllerCtxInfo(ctx, topts.ParamsRun)
	assert.NilError(t, err)
	assert.NilError(t, pacrepo.CreateNS(ctx, topts.TargetNS, topts.ParamsRun))
	assert.NilError(t, secret.Create(ctx, topts.ParamsRun, map[string]string{"secret": "SHHHHHHH"}, topts.TargetNS, "pac-secret"))
	topts.TargetEvent = triggertype.PullRequest.String()
	topts.YAMLFiles = map[string]string{
		".tekton/pr.yaml": "testdata/pipelinerun-error-snippet-with-secret.yaml",
	}
	topts.CheckForStatus = "failure"
	_, f := tgitea.TestPR(t, topts)
	defer f()

	topts.Regexp = regexp.MustCompile(`I WANT TO SAY \*\*\*\*\* OUT LOUD BUT NOBODY UNDERSTAND ME`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)
}

// TestGiteaBadLinkOfTask checks that we fail properly with the error from the
// tekton pipelines controller. We check on the UI interface that we display
// and inside the pac controller.
func TestGiteaBadLinkOfTask(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/failures/bad-runafter-task.yaml",
		},
		CheckForStatus: "failure",
		ExpectEvents:   true,
		Regexp:         regexp.MustCompile(".*There was an error creating the PipelineRun*"),
	}
	ctx, f := tgitea.TestPR(t, topts)
	defer f()
	errre := regexp.MustCompile("There was an error starting the PipelineRun")
	maxLines := int64(1000)
	assert.NilError(t, twait.RegexpMatchingInControllerLog(ctx, topts.ParamsRun, *errre, 10, "controller", &maxLines, nil))
}

// TestGiteaPipelineRunWithSameName checks that we fail properly with the error from the
// tekton pipelines controller. We check on the UI interface that we display
// and inside the pac controller.
func TestGiteaPipelineRunWithSameName(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr1.yaml": "testdata/failures/pipelinerun_same_name_on_pull.yaml",
			".tekton/pr2.yaml": "testdata/failures/pipelinerun_same_name_on_push.yaml",
		},
		CheckForStatus: "failure",
		ExpectEvents:   true,
		Regexp:         regexp.MustCompile(".*found multiple pipelinerun in .tekton with the same name*"),
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	// Wait for any webhook feedback loop to settle, then verify only 1 failure
	// comment was posted (not duplicates from re-triggered no-op comment events).
	time.Sleep(10 * time.Second)

	comments, _, err := topts.GiteaCNX.Client().ListRepoIssueComments(
		topts.PullRequest.Base.Repository.Owner.UserName,
		topts.PullRequest.Base.Repository.Name,
		forgejo.ListIssueCommentOptions{})
	assert.NilError(t, err)

	failureRe := regexp.MustCompile("found multiple pipelinerun in .tekton with the same name")
	var failureCount int
	for _, comment := range comments {
		if failureRe.MatchString(comment.Body) {
			failureCount++
		}
	}
	assert.Equal(t, failureCount, 1,
		"expected 1 failure comment but found %d", failureCount)
}

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

		defer (func() {
			if os.Getenv("TEST_NOCLEANUP") != "true" {
				topts.ParamsRun.Clients.Log.Infof("Cleaning up global repo %s in %s", info.DefaultGlobalRepoName, globalNs)
				err = topts.ParamsRun.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(globalNs).Delete(
					context.Background(), info.DefaultGlobalRepoName, metav1.DeleteOptions{})
				assert.NilError(t, err)
			}
		})()
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

func TestGiteaPushToTagGreedy(t *testing.T) {
	topts := &tgitea.TestOpts{
		SkipEventsCheck:       true,
		TargetEvent:           triggertype.Push.String(),
		NoPullRequestCreation: true,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	prmap := map[string]string{".tekton/pr.yaml": "testdata/pipelinerun.yaml"}
	entries, err := payload.GetEntries(prmap, topts.TargetNS, "refs/tags/*", topts.TargetEvent, map[string]string{})
	assert.NilError(t, err)
	topts.TargetRefName = topts.DefaultBranch

	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.DefaultBranch,
		BaseRefName:   topts.DefaultBranch,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	scmOpts.TargetRefName = "refs/tags/v1.0.0"
	_ = scm.PushFilesToRefGit(t, scmOpts, map[string]string{"README.md": "hello new version from tag"})
	waitOpts := twait.Opts{
		RepoName:  topts.TargetNS,
		Namespace: topts.TargetNS,
		// 0 means 1 🙃 (we test for >, while we actually should do >=, but i
		// need to go all over the code to verify it's not going to break
		// anything else)
		MinNumberStatus: 0,
		PollTimeout:     twait.DefaultTimeout,
	}
	_, err = twait.UntilRepositoryUpdated(context.Background(), topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)
}

// TestGiteaHubTaskNotFound tests that we fail gracefully when a task is not
// found in the hub, or a specific version of a task is not found.
func TestGiteaHubTaskNotFound(t *testing.T) {
	tests := []struct {
		name     string
		yamlFile string
		errMsg   string
	}{
		{
			name:     "task not found",
			yamlFile: "testdata/pipelinerun-hub-task-not-found.yaml",
			errMsg:   ".*could not fetch remote task.*i-am-a-task-that-does-not-exist-i-hope.*",
		},
		{
			name:     "task version not found",
			yamlFile: "testdata/pipelinerun-hub-task-version-not-found.yaml",
			errMsg:   ".*could not fetch remote task.*git-clone:99.99.99.*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topts := &tgitea.TestOpts{
				TargetEvent:  triggertype.PullRequest.String(),
				YAMLFiles:    map[string]string{".tekton/pr.yaml": tt.yamlFile},
				ExpectEvents: true,
			}
			_, f := tgitea.TestPR(t, topts)
			defer f()
			topts.Regexp = regexp.MustCompile(tt.errMsg)
			tgitea.WaitForPullRequestCommentMatch(t, topts)
		})
	}
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -run TestGiteaPush ."
// End:
