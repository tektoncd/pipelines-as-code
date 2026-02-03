//go:build e2e

package test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/google/go-github/v81/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGithubGHEPushRequestGitOpsCommentOnComment(t *testing.T) {
	opsComment := "/hello-world"
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github GitOps push/retest request",
		YamlFiles:     []string{"testdata/pipelinerun-on-comment-annotation.yaml"},
		NoStatusCheck: true,
		TargetRefName: options.MainBranch,
		GHE:           true,
	}
	g.RunPushRequest(ctx, t)
	defer g.TearDown(ctx, t)

	// let's make sure we didn't create any PipelineRuns since we only match on-comment here
	pruns, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), 0)

	g.Cnx.Clients.Log.Infof("Running ops comment %s as Push comment", opsComment)
	_, _, err = g.Provider.Client().Repositories.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.SHA,
		&github.RepositoryComment{Body: github.Ptr(opsComment)})
	assert.NilError(t, err)

	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: len(g.YamlFiles),
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	g.Cnx.Clients.Log.Info("Waiting for Repository to be updated")
	_, err = twait.UntilRepositoryUpdated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Check if we have the repository set as succeeded")
	repo, err := g.Cnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(g.TargetNamespace).Get(ctx, g.TargetNamespace, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, repo.Status[len(repo.Status)-1].Conditions[0].Status, corev1.ConditionTrue)

	pruns, err = g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), len(g.YamlFiles))
	lastPrName := pruns.Items[0].GetName()
	err = twait.RegexpMatchingInPodLog(
		context.Background(),
		g.Cnx,
		g.TargetNamespace,
		fmt.Sprintf("tekton.dev/pipelineRun=%s", lastPrName),
		"step-task",
		*regexp.MustCompile(opsComment),
		"",
		2,
		nil)

	assert.NilError(t, err)
}

func TestGithubGHEPushRequestGitOpsCommentRetest(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label: "Github GitOps push/retest request",
		YamlFiles: []string{
			"testdata/pipelinerun-on-push.yaml", "testdata/pipelinerun.yaml",
		},
		GHE: true,
	}
	g.RunPushRequest(ctx, t)
	defer g.TearDown(ctx, t)
	comment := "/retest branch:" + g.TargetNamespace

	pruns, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), 2)

	g.Cnx.Clients.Log.Infof("%s on Push Request", comment)
	_, _, err = g.Provider.Client().Repositories.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.SHA,
		&github.RepositoryComment{Body: github.Ptr(comment)})
	assert.NilError(t, err)

	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 4,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	g.Cnx.Clients.Log.Info("Waiting for Repository to be updated")
	_, err = twait.UntilRepositoryUpdated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Check if we have the repository set as succeeded")
	repo, err := g.Cnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(g.TargetNamespace).Get(ctx, g.TargetNamespace, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, repo.Status[len(repo.Status)-1].Conditions[0].Status, corev1.ConditionTrue)

	pruns, err = g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), 4)

	for i := range pruns.Items {
		sData, err := g.Cnx.Clients.Kube.CoreV1().Secrets(g.TargetNamespace).Get(ctx, pruns.Items[i].GetAnnotations()[keys.GitAuthSecret], metav1.GetOptions{})
		assert.NilError(t, err)
		assert.Assert(t, string(sData.Data["git-provider-token"]) != "")
		assert.Assert(t, string(sData.Data[".git-credentials"]) != "")
		assert.Assert(t, string(sData.Data[".gitconfig"]) != "")
	}
}

func TestGithubGHEPushRequestGitOpsCommentCancel(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "GitHub Gitops push/cancel request",
		YamlFiles: []string{"testdata/pipelinerun-on-push.yaml", "testdata/pipelinerun.yaml"},
		GHE:       true,
	}
	g.RunPushRequest(ctx, t)
	defer g.TearDown(ctx, t)

	ctx, err := cctx.GetControllerCtxInfo(ctx, g.Cnx)
	assert.NilError(t, err)

	pruns, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), 2)

	g.Cnx.Clients.Log.Info("/test pipelinerun-on-push on Push Request before canceling")
	_, _, err = g.Provider.Client().Repositories.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.SHA,
		&github.RepositoryComment{Body: github.Ptr("/test pipelinerun-on-push branch:" + g.TargetNamespace)})
	assert.NilError(t, err)
	numberOfStatus := 3
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: numberOfStatus,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	err = twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	comment := "/cancel pipelinerun-on-push branch:" + g.TargetNamespace
	g.Cnx.Clients.Log.Infof("%s on Push Request", comment)
	_, _, err = g.Provider.Client().Repositories.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.SHA,
		&github.RepositoryComment{Body: github.Ptr(comment)})
	assert.NilError(t, err)

	cancelWaitOpts := waitOpts
	cancelWaitOpts.MinNumberStatus = 1
	cancelWaitOpts.PollTimeout = 90 * time.Second

	// wait for the cancellation to propagate through PipelineRun status and repository status
	err = twait.UntilPipelineRunHasReason(ctx, g.Cnx.Clients, tektonv1.PipelineRunReasonCancelled, cancelWaitOpts)
	if err == nil {
		_, err = twait.UntilRepositoryHasStatusReason(ctx, g.Cnx.Clients, cancelWaitOpts, tektonv1.PipelineRunReasonCancelled.String())
	}
	if err != nil {
		numLines := int64(1000)
		reg := regexp.MustCompile(".*cancel-in-progress:.*pipelinerun.*on-push.*")
		logErr := twait.RegexpMatchingInControllerLog(ctx, g.Cnx, *reg, 10, "ghe-controller", &numLines, nil)
		if logErr != nil {
			t.Errorf("neither a cancelled pipelinerun in repo status or a cancellation request in the controller log was found: status wait error: %s, log wait error: %s", err.Error(), logErr.Error())
		}
		return
	}

	// make sure the number of items
	pruns, err = g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(pruns.Items), numberOfStatus)
	cancelled := false
	for _, pr := range pruns.Items {
		if len(pr.Status.Conditions) > 0 && pr.Status.Conditions[0].Reason == tektonv1.PipelineRunReasonCancelled.String() {
			cancelled = true
		}
	}
	assert.Assert(t, cancelled, "No cancelled pipeline run found")
}

func TestGithubGHEPullRequestRetestPullRequestNumberSubstitution(t *testing.T) {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	ctx := context.Background()
	g := &tgithub.PRTest{GHE: true}

	ctx, runcnx, opts, ghcnx, err := tgithub.Setup(ctx, g.GHE, g.Webhook)
	assert.NilError(t, err)
	g.Logger = runcnx.Clients.Log

	g.CommitTitle = fmt.Sprintf("Testing %s with Github APPS integration on %s", g.Label, targetNS)
	g.Logger.Info(g.CommitTitle)

	repoinfo, resp, err := ghcnx.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Errorf("Repository %s not found in %s", opts.Organization, opts.Repo)
	}

	if g.Options.Settings != nil && g.Options.Settings.Github != nil {
		opts.Settings = g.Options.Settings
	}
	err = tgithub.CreateCRD(ctx, t, repoinfo, runcnx, opts, ghcnx, targetNS)
	assert.NilError(t, err)

	tempBaseBranch := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("temp-base-branch")
	tempBaseBranchRef := fmt.Sprintf("refs/heads/%s", tempBaseBranch)

	// Get the SHA of the default branch
	defaultBranchRef, _, err := ghcnx.Client().Git.GetRef(ctx, opts.Organization, opts.Repo, fmt.Sprintf("refs/heads/%s", repoinfo.GetDefaultBranch()))
	assert.NilError(t, err)

	// Create the temporary base branch
	_, _, err = ghcnx.Client().Git.CreateRef(ctx, opts.Organization, opts.Repo, github.CreateRef{
		Ref: tempBaseBranchRef,
		SHA: *defaultBranchRef.Object.SHA,
	})
	assert.NilError(t, err)

	yamlEntries := map[string]string{".tekton/pipelinerun-pr-number-variable.yaml": "testdata/pipelinerun-pr-number-variable.yaml"}

	entries, err := payload.GetEntries(yamlEntries, targetNS, tempBaseBranchRef, triggertype.Push.String(), map[string]string{})
	assert.NilError(t, err)

	targetRefName := fmt.Sprintf("refs/heads/%s",
		names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test"))

	sha, vref, err := tgithub.PushFilesToRef(ctx, ghcnx.Client(), g.CommitTitle, tempBaseBranchRef, targetRefName,
		opts.Organization, opts.Repo, entries)
	assert.NilError(t, err)

	g.Logger.Infof("Commit %s has been created and pushed to %s", sha, vref.GetURL())
	number, err := tgithub.PRCreate(ctx, runcnx, ghcnx, opts.Organization,
		opts.Repo, targetRefName, tempBaseBranchRef, g.CommitTitle)
	assert.NilError(t, err)

	g.Cnx = runcnx
	g.Options = opts
	g.Provider = ghcnx
	g.TargetNamespace = targetNS
	g.TargetRefName = targetRefName
	g.PRNumber = number
	g.SHA = sha

	defer g.TearDown(ctx, t)
	defer func() {
		if os.Getenv("TEST_NOCLEANUP") != "true" {
			g.Logger.Infof("Deleting Ref %s", tempBaseBranchRef)
			_, err := ghcnx.Client().Git.DeleteRef(ctx, opts.Organization, opts.Repo, tempBaseBranchRef)
			assert.NilError(t, err)
		}
	}()

	mergeResult, _, err := g.Provider.Client().PullRequests.Merge(ctx, opts.Organization, opts.Repo, g.PRNumber, g.CommitTitle, &github.PullRequestOptions{
		MergeMethod: "rebase",
		SHA:         sha,
	})
	assert.NilError(t, err)
	g.Logger.Infof("Pull request %d has been merged", g.PRNumber)

	// wait for API to reflect this PR in response
	time.Sleep(10 * time.Second)

	mergedSHA := mergeResult.GetSHA()

	_, _, err = g.Provider.Client().Repositories.CreateComment(ctx, opts.Organization, opts.Repo, mergedSHA, &github.RepositoryComment{Body: github.Ptr("/retest pipelinerun-pr-number-variable branch:" + tempBaseBranch)})
	assert.NilError(t, err)
	g.Logger.Infof("Comment %s has been created", mergedSHA)

	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       mergedSHA,
	}

	err = twait.UntilPipelineRunHasReason(ctx, g.Cnx.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)

	regex := regexp.MustCompile(fmt.Sprintf("I don't know my PR number is %d", g.PRNumber))

	// because there are two pipeline runs were created due to pull request merge, we need the one which is triggered on retest comment.
	err = twait.RegexpMatchingInPodLog(ctx, g.Cnx, g.TargetNamespace,
		fmt.Sprintf("pipelinesascode.tekton.dev/event-type=%s",
			opscomments.RetestSingleCommentEventType.String()),
		"step-task", *regex, "", 2, nil)
	assert.NilError(t, err)
}
