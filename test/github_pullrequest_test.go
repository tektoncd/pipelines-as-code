//go:build e2e

package test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/configmap"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"

	"github.com/google/go-github/v81/github"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

func TestGithubGHEPullRequestBasic(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github PullRequest",
		YamlFiles: []string{"testdata/pipelinerun.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubGHEPullRequest(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github Rerequest",
		YamlFiles: []string{"testdata/pipelinerun.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubGHEPullRequestMultiples(t *testing.T) {
	if os.Getenv("NIGHTLY_E2E_TEST") != "true" {
		t.Skip("Skipping test since only enabled for nightly")
	}
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github multiple PullRequest",
		YamlFiles: []string{"testdata/pipelinerun.yaml", "testdata/pipelinerun-clone.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubGHEPullRequestMatchOnCEL(t *testing.T) {
	if os.Getenv("NIGHTLY_E2E_TEST") != "true" {
		t.Skip("Skipping test since only enabled for nightly")
	}
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github CEL Match",
		YamlFiles: []string{"testdata/pipelinerun-cel-annotation.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubGHEPullRequestOnLabel(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github On Label",
		YamlFiles:     []string{"testdata/pipelinerun-on-label.yaml"},
		NoStatusCheck: true,
		GHE:           true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	// wait a bit that GitHub processed or we will get double events
	time.Sleep(5 * time.Second)

	g.Cnx.Clients.Log.Infof("Creating a label bug on PullRequest")
	_, _, err := g.Provider.Client().Issues.AddLabelsToIssue(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		[]string{"bug"})
	assert.NilError(t, err)

	sopt := twait.SuccessOpt{
		Title:           g.CommitTitle,
		OnEvent:         triggertype.PullRequestLabeled.String(),
		TargetNS:        g.TargetNamespace,
		NumberofPRMatch: len(g.YamlFiles),
		SHA:             g.SHA,
	}
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)

	opt := github.ListOptions{}
	res := &github.ListCheckRunsResults{}
	resp := &github.Response{}
	counter := 0
	for {
		res, resp, err = g.Provider.Client().Checks.ListCheckRunsForRef(ctx, g.Options.Organization, g.Options.Repo, g.SHA, &github.ListCheckRunsOptions{
			AppID:       g.Provider.ApplicationID,
			ListOptions: opt,
		})
		assert.NilError(t, err)
		assert.Equal(t, resp.StatusCode, 200)
		if len(res.CheckRuns) > 0 {
			break
		}
		g.Cnx.Clients.Log.Infof("Waiting for the check run to be created")
		if counter > 10 {
			t.Errorf("Check run not created after 10 tries")
			break
		}
		time.Sleep(5 * time.Second)
	}
	assert.Equal(t, len(res.CheckRuns), 1)
	expected := fmt.Sprintf("%s / %s", settings.PACApplicationNameDefaultValue, "pipelinerun-on-label-")
	checkName := res.CheckRuns[0].GetName()
	assert.Assert(t, strings.HasPrefix(checkName, expected), "checkName %s != expected %s", checkName, expected)
}

func TestGithubGHEPullRequestCELMatchOnTitle(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github CEL Match on Title",
		YamlFiles: []string{"testdata/pipelinerun-cel-annotation-for-title-match.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)
}

func TestGithubGHEWebhookPullRequest(t *testing.T) {
	ctx := context.Background()

	g := &tgithub.PRTest{
		Label:     "Github GHE PullRequest onWebhook",
		YamlFiles: []string{"testdata/pipelinerun.yaml"},
		GHE:       true,
		Webhook:   true,
	}
	defer g.TearDown(ctx, t)
	g.RunPullRequest(ctx, t)
}

func TestGithubGHEInvalidCELExpressionReportingOnPR(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github PullRequest Invalid CEL expression",
		YamlFiles:     []string{"testdata/failures/pipelinerun-invalid-cel.yaml"},
		GHE:           true,
		NoStatusCheck: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	maxLoop := 10
	for i := 0; i < maxLoop; i++ {
		comments, _, err := g.Provider.Client().Issues.ListComments(
			ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
			&github.IssueListCommentsOptions{})
		assert.NilError(t, err)

		if len(comments) > 0 {
			assert.Assert(t, len(comments) == 1, "Should have only one comment created we got way too many: %+v", comments)
			commentBody := comments[0].GetBody()
			commentRegexp := regexp.MustCompile(`CEL expression evaluation error: failed to parse expression "event == \"pull request\" |": ERROR: <input>:1:25: Syntax error: token recognition error at: '|'  | event == "pull request" |  | ........................^`)
			assert.Check(t, commentRegexp.MatchString(commentBody), commentBody, t)
			g.Logger.Info("CEL expression validation comment has been matched")
			return
		}

		g.Cnx.Clients.Log.Infof("Looping %d/%d waiting for a comment to appear", i, maxLoop)
		time.Sleep(6 * time.Second)
	}

	t.Fatal("No comments with the pipelinerun error found on the pull request")
}

// TestGithubGHEPullRequestInvalidSpecValues tests invalid field values of a PipelinRun and
// ensures that these validation errors are reported on UI.
func TestGithubGHEPullRequestInvalidSpecValues(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github Invalid Yaml",
		YamlFiles:     []string{"testdata/failures/invalid-timeouts-values-pipelinerun.yaml"},
		GHE:           true,
		NoStatusCheck: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	opt := github.ListOptions{}
	res := &github.ListCheckRunsResults{}
	resp := &github.Response{}
	var err error
	counter := 0
	for {
		res, resp, err = g.Provider.Client().Checks.ListCheckRunsForRef(ctx, g.Options.Organization, g.Options.Repo, g.SHA, &github.ListCheckRunsOptions{
			AppID:       g.Provider.ApplicationID,
			Status:      github.Ptr("completed"),
			ListOptions: opt,
		})
		assert.NilError(t, err)
		assert.Equal(t, resp.StatusCode, 200)
		if len(res.CheckRuns) > 0 {
			break
		}
		g.Cnx.Clients.Log.Infof("Waiting for the check run to be created")
		if counter > 10 {
			t.Errorf("Check run not created after 10 tries")
			break
		}
		time.Sleep(5 * time.Second)
		counter++
	}

	assert.Equal(t, len(res.CheckRuns), 1)
	assert.Equal(t, res.CheckRuns[0].GetOutput().GetTitle(), "pipelinerun start failure")
	reg := regexp.MustCompile("Pipelines as Code.* has <b>failed</b>.")
	assert.Assert(t, cmp.Regexp(reg, res.CheckRuns[0].GetOutput().GetSummary()))
	reg = regexp.MustCompile(options.InvalidYamlErrorPattern)
	assert.Assert(t, cmp.Regexp(reg, res.CheckRuns[0].GetOutput().GetText()))
}

func TestGithubGHETestExplicitlyNoMatchedPipelineRun(t *testing.T) {
	ctx := context.Background()
	g := tgithub.PRTest{
		Label:         "Github test implicit comment",
		YamlFiles:     []string{"testdata/pipelinerun-nomatch.yaml"},
		GHE:           true,
		NoStatusCheck: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Creating /test no-match on PullRequest")
	_, _, err := g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/test no-match")})
	assert.NilError(t, err)
	sopt := twait.SuccessOpt{
		Title:           fmt.Sprintf("Testing %s with Github APPS integration on %s", g.Label, g.TargetNamespace),
		OnEvent:         opscomments.TestSingleCommentEventType.String(),
		TargetNS:        g.TargetNamespace,
		NumberofPRMatch: len(g.YamlFiles),
	}
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)
}

func TestGithubGHECancelInProgress(t *testing.T) {
	ctx := context.Background()
	g := tgithub.PRTest{
		Label:         "Github cancel in progress",
		YamlFiles:     []string{"testdata/pipelinerun-cancel-in-progress.yaml"},
		GHE:           true,
		NoStatusCheck: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Waiting for one pipelinerun to be created")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	err := twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)
	time.Sleep(10 * time.Second)

	g.Cnx.Clients.Log.Infof("Creating /test on PullRequest to create a second run")
	_, _, err = g.Provider.Client().Issues.CreateComment(ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/test")})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Waiting for the two pipelinerun to be created")
	waitOpts = twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	err = twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Sleeping for 10 seconds to let the pipelinerun to be cancelled")

	i := 0
	foundCancelled := false
	for i < 10 {
		prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
		})
		assert.NilError(t, err)

		for _, pr := range prs.Items {
			if pr.GetStatusCondition() == nil {
				continue
			}
			if len(pr.Status.Conditions) == 0 {
				continue
			}
			if pr.Status.Conditions[0].Reason == "Cancelled" {
				g.Cnx.Clients.Log.Infof("PipelineRun %s has been cancelled", pr.Name)
				foundCancelled = true
				break
			}
		}
		if foundCancelled {
			break
		}

		time.Sleep(5 * time.Second)
		i++
	}
	assert.Assert(t, foundCancelled, "No Pipelines has been found cancedl in NS %s", g.TargetNamespace)
}

func TestGithubGHECancelInProgressPRClosed(t *testing.T) {
	ctx := context.Background()
	g := tgithub.PRTest{
		Label:         "Github cancel in progress while pr is closed",
		YamlFiles:     []string{"testdata/pipelinerun-cancel-in-progress.yaml"},
		GHE:           true,
		NoStatusCheck: true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Waiting for the two pipelinerun to be created")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	err := twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Closing the PullRequest")
	_, _, err = g.Provider.Client().PullRequests.Edit(ctx, g.Options.Organization, g.Options.Repo, g.PRNumber, &github.PullRequest{
		State: github.Ptr("closed"),
	})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Sleeping for 10 seconds to let the pipelinerun to be cancelled")
	time.Sleep(10 * time.Second)

	g.Cnx.Clients.Log.Infof("Checking that the pipelinerun has been cancelled")

	prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 1, "should have only one pipelinerun, but we have: %d", len(prs.Items))

	assert.Equal(t, prs.Items[0].GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason(), "Cancelled", "should have been cancelled")

	res, resp, err := g.Provider.Client().Checks.ListCheckRunsForRef(ctx, g.Options.Organization, g.Options.Repo, g.SHA, &github.ListCheckRunsOptions{
		AppID:       g.Provider.ApplicationID,
		ListOptions: github.ListOptions{},
	})
	assert.NilError(t, err)
	assert.Equal(t, resp.StatusCode, 200)

	assert.Equal(t, res.CheckRuns[0].GetConclusion(), "cancelled")
}

func TestGithubGHEPullRequestNoOnLabelAnnotation(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github PullRequest",
		YamlFiles: []string{"testdata/pipelinerun-pr-cel-expression.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Creating a label bug on PullRequest")
	_, _, err := g.Provider.Client().Issues.AddLabelsToIssue(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		[]string{"bug"})
	assert.NilError(t, err)

	// let's wait 10 secs and check every second that a PipelineRun is created or not.
	for i := 0; i < 10; i++ {
		prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
		})
		assert.NilError(t, err)
		// after adding a label on the PR we need to make sure that it doesn't trigger another PipelineRun.
		assert.Equal(t, len(prs.Items), 1)
		time.Sleep(1 * time.Second)
	}
}

func TestGithubGHEPullRequestCELLabelEvent(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github CEL Label Event",
		YamlFiles:     []string{"testdata/pipelinerun-cel-label-event.yaml"},
		NoStatusCheck: true,
		GHE:           true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Verifying no PipelineRun created on PR creation")
	prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
	})
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 0, "No PipelineRun should be created on PR creation")

	// wait a bit that GitHub processed or we will get double events
	time.Sleep(5 * time.Second)

	g.Cnx.Clients.Log.Infof("Creating a label 'bug' on PullRequest")
	_, _, err = g.Provider.Client().Issues.AddLabelsToIssue(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		[]string{"bug"})
	assert.NilError(t, err)

	sopt := twait.SuccessOpt{
		Title:           g.CommitTitle,
		OnEvent:         triggertype.PullRequestLabeled.String(),
		TargetNS:        g.TargetNamespace,
		NumberofPRMatch: len(g.YamlFiles),
		SHA:             g.SHA,
	}
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)

	opt := github.ListOptions{}
	res := &github.ListCheckRunsResults{}
	resp := &github.Response{}
	counter := 0
	for {
		res, resp, err = g.Provider.Client().Checks.ListCheckRunsForRef(ctx, g.Options.Organization, g.Options.Repo, g.SHA, &github.ListCheckRunsOptions{
			AppID:       g.Provider.ApplicationID,
			ListOptions: opt,
		})
		assert.NilError(t, err)
		assert.Equal(t, resp.StatusCode, 200)
		if len(res.CheckRuns) > 0 {
			break
		}
		g.Cnx.Clients.Log.Infof("Waiting for the check run to be created")
		if counter > 10 {
			t.Errorf("Check run not created after 10 tries")
			break
		}
		time.Sleep(5 * time.Second)
		counter++
	}
	assert.Equal(t, len(res.CheckRuns), 1)
	expected := fmt.Sprintf("%s / %s", settings.PACApplicationNameDefaultValue, "pipelinerun-cel-label-event-")
	checkName := res.CheckRuns[0].GetName()
	assert.Assert(t, strings.HasPrefix(checkName, expected), "checkName %s != expected %s", checkName, expected)
}

func TestGithubGHEPullRequestNoPipelineRunCancelledOnPRClosed(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github PullRequest",
		YamlFiles:     []string{"testdata/pipelinerun-gitops.yaml"},
		NoStatusCheck: true,
		GHE:           true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Waiting for the two pipelinerun to be created")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	err := twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Closing the PullRequest")
	_, _, err = g.Provider.Client().PullRequests.Edit(ctx, g.Options.Organization, g.Options.Repo, g.PRNumber, &github.PullRequest{
		State: github.Ptr("closed"),
	})
	assert.NilError(t, err)

	isCancelled := false
	var prReason string

	for range 10 {
		prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
		})
		if err != nil {
			t.Logf("failed to list PipelineRuns: %v", err)
			time.Sleep(1 * time.Second)
			continue // try again
		}
		if len(prs.Items) != 1 {
			t.Logf("expected 1 PipelineRun, got %d", len(prs.Items))
			time.Sleep(1 * time.Second)
			continue // try again
		}
		// Check all conditions, get the right one
		conditions := prs.Items[0].Status.GetConditions()
		for _, c := range conditions {
			if c.Type == apis.ConditionSucceeded {
				prReason = c.Reason
				if prReason != string(tektonv1.PipelineRunReasonRunning) {
					isCancelled = true
					t.Logf("expected PipelineRun `Running`, got %s", prReason)
					break
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
	assert.Equal(t, false, isCancelled, fmt.Sprintf("PipelineRun got cancelled while we wanted it `Running`, last reason: %v", prReason))
}

func TestGithubGHECancelInProgressSettingFromConfigMapOnPR(t *testing.T) {
	ctx := context.Background()
	ctx, runcnx, _, _, err := tgithub.Setup(ctx, true, false)
	assert.NilError(t, err)

	patchData := map[string]string{
		"enable-cancel-in-progress-on-pull-requests": "true",
	}

	configMapTearDown := configmap.ChangeGlobalConfig(ctx, t, runcnx, "ghe-configmap", patchData)
	defer configMapTearDown()

	g := &tgithub.PRTest{
		Label:         "Github PullRequest",
		YamlFiles:     []string{"testdata/pipelinerun-gitops.yaml"},
		NoStatusCheck: true,
		GHE:           true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	_, _, err = g.Provider.Client().Issues.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/test")})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Waiting for the two pipelinerun to be created")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}

	err = twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	// we want one PipelineRun to be cancelled
	waitOpts.MinNumberStatus = 1

	err = twait.UntilPipelineRunHasReason(ctx, g.Cnx.Clients, tektonv1.PipelineRunReasonCancelled, waitOpts)
	assert.NilError(t, err)
}

func TestGithubGHECancelInProgressSettingFromConfigMapOnPush(t *testing.T) {
	ctx := context.Background()
	ctx, runcnx, _, _, err := tgithub.Setup(ctx, true, false)
	assert.NilError(t, err)

	patchData := map[string]string{
		"enable-cancel-in-progress-on-push": "true",
	}

	configMapTearDown := configmap.ChangeGlobalConfig(ctx, t, runcnx, "ghe-configmap", patchData)
	defer configMapTearDown()

	g := &tgithub.PRTest{
		Label:         "Github PullRequest",
		YamlFiles:     []string{"testdata/pipelinerun-gitops.yaml"},
		NoStatusCheck: true,
		GHE:           true,
	}
	g.RunPushRequest(ctx, t)
	defer g.TearDown(ctx, t)

	comment := fmt.Sprintf("/retest branch:%s", g.TargetNamespace)
	_, _, err = g.Provider.Client().Repositories.CreateComment(ctx,
		g.Options.Organization,
		g.Options.Repo, g.SHA,
		&github.RepositoryComment{Body: github.Ptr(comment)})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Waiting for the two pipelinerun to be created")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}

	err = twait.UntilPipelineRunCreated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	// we want one PipelineRun to be cancelled
	waitOpts.MinNumberStatus = 1

	err = twait.UntilPipelineRunHasReason(ctx, g.Cnx.Clients, tektonv1.PipelineRunReasonCancelled, waitOpts)
	assert.NilError(t, err)
}

func TestGithubGHEPullandPushMatchTriggerOnlyPull(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github PullRequest",
		YamlFiles: []string{"testdata/pipelinerun-match-push-pullr.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	globalNs, _, err := params.GetInstallLocation(ctx, g.Cnx)
	assert.NilError(t, err)
	ctx = info.StoreNS(ctx, globalNs)

	reg := regexp.MustCompile(fmt.Sprintf("Skipping push event for commit.*as it belongs to pull request #%d", g.PRNumber))
	maxLines := int64(1000)
	err = twait.RegexpMatchingInControllerLog(ctx, g.Cnx, *reg, 20, "ghe-controller", &maxLines, nil)
	assert.NilError(t, err)
}

func TestGithubGHEWebhookDisableCommentsOnPR(t *testing.T) {
	ctx := context.Background()

	g := &tgithub.PRTest{
		Label:     "Github GHE PullRequest onWebhook",
		YamlFiles: []string{"testdata/pipelinerun.yaml"},
		GHE:       true,
		Webhook:   true,
	}

	commentStrategy := &v1alpha1.Settings{
		Github: &v1alpha1.GithubSettings{
			CommentStrategy: "disable_all",
		},
	}

	g.Options.Settings = commentStrategy
	defer g.TearDown(ctx, t)
	g.RunPullRequest(ctx, t)

	comments, _, _ := g.Provider.Client().Issues.ListComments(ctx, g.Options.Organization, g.Options.Repo, g.PRNumber, nil)
	commentRegexp := regexp.MustCompile(`.*Pipelines as Code CI/*`)
	successCommentsPost := 0
	for _, n := range comments {
		if commentRegexp.MatchString(*n.Body) {
			successCommentsPost++
		}
	}
	assert.Equal(t, 0, successCommentsPost)
}

func TestGithubGHEIgnoreTagPushCommitsFromSkipPushEventsSetting(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github PullRequest",
		YamlFiles:     []string{"testdata/pipelinerun.yaml"},
		NoStatusCheck: true,
		GHE:           true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	tag := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("v1.0")

	_, err := tgithub.CreateTag(ctx, t, g.Cnx, g.Provider, g.Options, g.SHA, tag)
	assert.NilError(t, err)
	defer tgithub.DeleteTag(ctx, g.Provider, g.Options, tag) //nolint:errcheck

	globalNs, _, err := params.GetInstallLocation(ctx, g.Cnx)
	assert.NilError(t, err)
	ctx = info.StoreNS(ctx, globalNs)

	reg := regexp.MustCompile(fmt.Sprintf("Processing tag push event for commit %s despite skip-push-events-for-pr-commits being enabled.*", g.SHA))
	maxLines := int64(1000)
	err = twait.RegexpMatchingInControllerLog(ctx, g.Cnx, *reg, 20, "ghe-controller", &maxLines, nil)
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Deleting tag %s", tag)
	err = tgithub.DeleteTag(ctx, g.Provider, g.Options, tag)
	assert.NilError(t, err)
	g.Cnx.Clients.Log.Infof("Tag %s has been deleted", tag)
}

// TestGithubPullRequestCelPrefix tests the cel: prefix for arbitrary CEL expressions.
// The cel: prefix allows evaluating full CEL expressions with access to body, headers, files, and pac namespaces.
func TestGithubGHEPullRequestCelPrefix(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github CEL Prefix",
		YamlFiles: []string{"testdata/pipelinerun-cel-prefix-github.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	// Wait for repository status to be updated
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	_, err := twait.UntilRepositoryUpdated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prs.Items) >= 1, "Expected at least one PipelineRun")

	// Verify cel: prefix expressions evaluated correctly using golden file
	// Expected output:
	// cel_ternary: new-pr (body.action == "opened" for a new PR)
	// cel_pac_branch: matched (pac.target_branch matches the target branch)
	// cel_has_function: has-pr (body.pull_request exists)
	// cel_string_concat: Build on <target_branch>
	// cel_files_check: has-files (files.all.size() > 0 since we have changed files)
	// cel_github_header: pull_request (X-Github-Event header value)
	// cel_error_handling: (empty string - cel: prefix returns empty on error)
	err = twait.RegexpMatchingInPodLog(
		ctx,
		g.Cnx,
		g.TargetNamespace,
		fmt.Sprintf("tekton.dev/pipelineRun=%s,tekton.dev/pipelineTask=cel-prefix-test", prs.Items[0].Name),
		"step-test-cel-prefix-values",
		regexp.Regexp{},
		t.Name(),
		2,
		nil,
	)
	assert.NilError(t, err)
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -info TestGithubPullRequest$ ."
// End:
