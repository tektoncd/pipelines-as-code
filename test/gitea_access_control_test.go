//go:build e2e

package test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/configmap"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const okToTestComment = "/ok-to-test"

// TestGiteaPolicyPullRequest tests the pull_request policy
// create a CRD which a policy allowing only users in the team pull_requester to allow a PR
// we create a org
// we create a team normal on org and add the user normal-$RANDOM onto it
// we create a pull request form a fork
// we test that it was denied
// we create a pull request from a fork with the user pull_requester which is in the allowed pull_requester team
// we test that it was allowed succeeded
//
// this test paths is mostly to test the logic in the pkg/policy package, there
// is not much gitea specifics compared to github.
func TestGiteaPolicyPullRequest(t *testing.T) {
	topts := &tgitea.TestOpts{
		OnOrg:                true,
		SkipEventsCheck:      true,
		CheckForNumberStatus: 2,
		TargetEvent:          triggertype.PullRequest.String(),
		Settings: &v1alpha1.Settings{
			Policy: &v1alpha1.Policy{
				PullRequest: []string{"pull_requester"},
			},
		},
		YAMLFiles: map[string]string{".tekton/pr.yaml": "testdata/pipelinerun.yaml"},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	adminCnx := topts.GiteaCNX

	topts.ParamsRun.Clients.Log.Infof("Repo CRD %s has been created with Policy: %+v", topts.TargetRefName, topts.Settings.Policy)

	orgName := "org-" + topts.TargetRefName
	topts.Opts.Organization = orgName

	// create normal team on org and add user normal onto it
	normalTeam, err := tgitea.CreateTeam(topts, orgName, "normal")
	assert.NilError(t, err)
	normalUserNamePasswd := fmt.Sprintf("normal-%s", topts.TargetRefName)
	normalUserCnx, normalUser, err := tgitea.CreateGiteaUserSecondCnx(topts, normalUserNamePasswd, normalUserNamePasswd)
	assert.NilError(t, err)
	_, err = topts.GiteaCNX.Client().AddTeamMember(normalTeam.ID, normalUser.UserName)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("User %s has been added to team %s", normalUser.UserName, normalTeam.Name)
	tgitea.CreateForkPullRequest(t, topts, normalUserCnx, "")
	topts.CheckForStatus = "Skipped"
	topts.CheckForNumberStatus = 1
	topts.Regexp = regexp.MustCompile(`.*Pipelines as Code CI is skipping this commit.*`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)
	tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, settings.PACApplicationNameDefaultValue, false)

	pullRequesterTeam, err := tgitea.CreateTeam(topts, orgName, "pull_requester")
	assert.NilError(t, err)
	pullRequesterUserNamePasswd := fmt.Sprintf("pullRequester-%s", topts.TargetRefName)
	pullRequesterUserCnx, pullRequesterUser, err := tgitea.CreateGiteaUserSecondCnx(topts, pullRequesterUserNamePasswd, pullRequesterUserNamePasswd)
	assert.NilError(t, err)
	_, err = topts.GiteaCNX.Client().AddTeamMember(pullRequesterTeam.ID, pullRequesterUser.UserName)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("User %s has been added to team %s", pullRequesterUser.UserName, pullRequesterTeam.Name)
	tgitea.CreateForkPullRequest(t, topts, pullRequesterUserCnx, "")
	topts.Regexp = successRegexp
	tgitea.WaitForPullRequestCommentMatch(t, topts)
	topts.GiteaCNX = adminCnx
}

// TestGiteaPolicyOkToTestRetest test the ok-to-test and retest policy
// create a CRD which a policy allowing only users in the team /ok-to-test to allow a PR
// we create a org
// we create a team ok-to-test on org and add the user ok-to-test-$RANDOM onto it
// we create a team normal on org and add the user normal-$RANDOM onto it
// we issue a /ok-to-test as user normal and check it was denied
// we delete the old pac comment to make the pac reliable checking it was denied.
// we issue a /retest as user normal and check it was denied
// we issue a /ok-to-test as user ok-to-test and check it was succeeded
//
// this test paths is mostly to test the logic in the pkg/policy package, there
// is not much gitea specifics compared to github.
func TestGiteaPolicyOkToTestRetest(t *testing.T) {
	topts := &tgitea.TestOpts{
		OnOrg:           true,
		SkipEventsCheck: true,
		TargetEvent:     triggertype.PullRequest.String(),
		Settings: &v1alpha1.Settings{
			Policy: &v1alpha1.Policy{
				OkToTest: []string{"ok-to-test"},
			},
		},
		YAMLFiles: map[string]string{".tekton/pr.yaml": "testdata/pipelinerun.yaml"},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	adminCnx := topts.GiteaCNX
	topts.ParamsRun.Clients.Log.Infof("Repo CRD %s has been created with Policy: %+v", topts.TargetRefName, topts.Settings.Policy)

	orgName := "org-" + topts.TargetRefName
	// create ok-to-test team on org and add user ok-to-test onto it
	oktotestTeam, err := tgitea.CreateTeam(topts, orgName, "ok-to-test")
	assert.NilError(t, err)
	okToTestUserNamePasswd := fmt.Sprintf("ok-to-test-%s", topts.TargetRefName)
	okToTestUserCnx, okToTestUser, err := tgitea.CreateGiteaUserSecondCnx(topts, okToTestUserNamePasswd, okToTestUserNamePasswd)
	assert.NilError(t, err)
	_, err = topts.GiteaCNX.Client().AddTeamMember(oktotestTeam.ID, okToTestUser.UserName)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("User %s has been added to team %s", okToTestUser.UserName, oktotestTeam.Name)

	// create normal team on org and add user normal onto it
	normalTeam, err := tgitea.CreateTeam(topts, orgName, "normal")
	assert.NilError(t, err)
	normalUserNamePasswd := fmt.Sprintf("normal-%s", topts.TargetRefName)
	normalUserCnx, normalUser, err := tgitea.CreateGiteaUserSecondCnx(topts, normalUserNamePasswd, normalUserNamePasswd)
	assert.NilError(t, err)
	_, err = topts.GiteaCNX.Client().AddTeamMember(normalTeam.ID, normalUser.UserName)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("User %s has been added to team %s", normalUser.UserName, normalTeam.Name)

	topts.ParamsRun.Clients.Log.Infof("Sending a /ok-to-test comment as a user not belonging to an allowed team in Repo CR policy but part of the organization")
	topts.GiteaCNX = normalUserCnx
	tgitea.PostCommentOnPullRequest(t, topts, okToTestComment)
	topts.CheckForStatus = "Skipped"
	topts.Regexp = regexp.MustCompile(fmt.Sprintf(".*User %s is not allowed to trigger CI via pull_request in this repo.", normalUser.UserName))
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	topts.ParamsRun.Clients.Log.Infof("Sending a /retest comment as a user not belonging to an allowed team in Repo CR policy but part of the organization")
	topts.GiteaCNX = normalUserCnx
	tgitea.PostCommentOnPullRequest(t, topts, "/retest")
	topts.CheckForStatus = "Skipped"
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	topts.GiteaCNX = okToTestUserCnx
	topts.ParamsRun.Clients.Log.Infof("Sending a /ok-to-test comment as a user belonging to an allowed team in Repo CR policy")
	tgitea.PostCommentOnPullRequest(t, topts, "/ok-to-test")
	topts.Regexp = successRegexp
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 1, "should have only one pipelinerun, but we have: %d", len(prs.Items))
	firstpr := prs.Items[0]
	generatename := strings.TrimSuffix(firstpr.GetGenerateName(), "-")

	topts.CheckForStatus = "success"
	// NOTE(chmouel): there is two status here, one old one which is the
	// failure without the prun and the new one with the prun on success same
	// bug we have github checkrun that we need to fix
	tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, fmt.Sprintf("%s / %s", settings.PACApplicationNameDefaultValue, generatename), true)
	topts.GiteaCNX = adminCnx
}

// TestGiteaACLOrgAllowed tests that the policy check works when the user is part of an allowed org.
func TestGiteaACLOrgAllowed(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun.yaml",
		},
		ExpectEvents:         false,
		CheckForNumberStatus: 2,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	adminCnx := topts.GiteaCNX
	secondcnx, _, err := tgitea.CreateGiteaUserSecondCnx(topts, topts.TargetRefName, topts.GiteaPassword)
	assert.NilError(t, err)

	tgitea.CreateForkPullRequest(t, topts, secondcnx, "read")
	topts.CheckForStatus = "success"
	tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, "", false)
	topts.GiteaCNX = adminCnx
}

// TestGiteaACLOrgPendingApproval tests when non authorized user sends a PR the status of CI shows as pending.
func TestGiteaACLOrgPendingApproval(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun.yaml",
		},
		ExpectEvents: false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	adminCnx := topts.GiteaCNX
	secondcnx, _, err := tgitea.CreateGiteaUserSecondCnx(topts, topts.TargetRefName, topts.GiteaPassword)
	assert.NilError(t, err)

	topts.PullRequest = tgitea.CreateForkPullRequest(t, topts, secondcnx, "")
	topts.CheckForStatus = "Skipped"
	tgitea.WaitForStatus(t, topts, topts.PullRequest.Head.Sha, "", false)
	topts.Regexp = regexp.MustCompile(`.*is skipping this commit.*`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)
	topts.GiteaCNX = adminCnx
}

func TestGiteaSuccessStatusAfterOkToTest(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun.yaml",
		},
		ExpectEvents: false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	adminCnx := topts.GiteaCNX
	secondcnx, _, err := tgitea.CreateGiteaUserSecondCnx(topts, topts.TargetRefName, topts.GiteaPassword)
	assert.NilError(t, err)
	topts.GiteaCNX = secondcnx
	topts.PullRequest = tgitea.CreateForkPullRequest(t, topts, secondcnx, "")
	topts.GiteaCNX = adminCnx
	_, _, err = topts.GiteaCNX.Client().CreateIssueComment(topts.Opts.Organization, topts.Opts.Repo, topts.PullRequest.Index, forgejo.CreateIssueCommentOption{Body: okToTestComment})
	assert.NilError(t, err)
	// there will be 3 statuses, when pull request is opened, a pending status is created and when the /ok-to-test comment is added,
	// that status transitions to success and one for pipeline run, gitea doesn't update the previous one it just creates a new one.
	topts.CheckForNumberStatus = 3
	tgitea.WaitForStatus(t, topts, topts.PullRequest.Head.Sha, "", false)
	statuses, _, err := topts.GiteaCNX.Client().ListStatuses(topts.Opts.Organization, topts.Opts.Repo, topts.PullRequest.Head.Sha, forgejo.ListStatusesOption{})
	assert.NilError(t, err)
	foundSuccess := false
	for _, status := range statuses {
		if status.State == forgejo.StatusSuccess && status.Context == "Pipelines as Code CI" {
			foundSuccess = true
			break
		}
	}
	assert.Assert(t, foundSuccess, "success status for \"Pipelines as Code CI\" context not found")
}

// TestGiteaACLCommentsAllowing tests that the gitops comment commands work.
func TestGiteaACLCommentsAllowing(t *testing.T) {
	tests := []struct {
		name, comment string
	}{
		{
			name:    "OK to Test",
			comment: okToTestComment,
		},
		{
			name:    "Retest",
			comment: "/retest",
		},
		{
			name:    "Test PR",
			comment: "/test pr-gitops-comment",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topts := &tgitea.TestOpts{
				TargetEvent: triggertype.PullRequest.String(),
				YAMLFiles: map[string]string{
					".tekton/pipelinerun-gitops.yaml": "testdata/pipelinerun-gitops.yaml",
				},
				ExpectEvents: false,
			}
			_, f := tgitea.TestPR(t, topts)
			defer f()
			secondcnx, _, err := tgitea.CreateGiteaUserSecondCnx(topts, topts.TargetRefName, topts.GiteaPassword)
			assert.NilError(t, err)

			topts.PullRequest = tgitea.CreateForkPullRequest(t, topts, secondcnx, "")
			topts.CheckForStatus = "Skipped"
			tgitea.WaitForStatus(t, topts, topts.PullRequest.Head.Sha, "", false)
			topts.Regexp = regexp.MustCompile(`.*is skipping this commit.*`)
			tgitea.WaitForPullRequestCommentMatch(t, topts)

			tgitea.PostCommentOnPullRequest(t, topts, tt.comment)
			topts.Regexp = successRegexp
			tgitea.WaitForPullRequestCommentMatch(t, topts)
			tgitea.WaitForStatus(t, topts, topts.PullRequest.Head.Sha, "", false)
			// checking the pod log to make sure /test <prname> works
			err = twait.RegexpMatchingInPodLog(context.Background(), topts.ParamsRun, topts.TargetNS, "pipelinesascode.tekton.dev/event-type=pull_request", "step-task", *regexp.MustCompile(".*MOTO"), "", 2, nil)
			assert.NilError(t, err, "Error while checking the logs of the pods")
		})
	}
}

// TestGiteaACLCommentsAllowingRememberOkToTestFalse tests when unauthorized user sends a PR the status shows as pending
// unless the authorized user adds a comment like /ok-to-test, When authorized user adds those comments
// the status of CI shows as success. Now non authorized user pushes to PR, the CI will again go to pending
// and require /ok-to-test again from authorized user.
func TestGiteaACLCommentsAllowingRememberOkToTestFalse(t *testing.T) {
	t.Skip("Skipping test changing the global config map for now")

	ctx := context.Background()
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun.yaml",
		},
		ExpectEvents: false,
	}

	topts.ParamsRun, topts.Opts, topts.GiteaCNX, _ = tgitea.Setup(ctx)
	assert.NilError(t, topts.ParamsRun.Clients.NewClients(ctx, &topts.ParamsRun.Info))

	ctx, err := cctx.GetControllerCtxInfo(ctx, topts.ParamsRun)
	assert.NilError(t, err)

	cfgMapData := map[string]string{
		"remember-ok-to-test": "false",
	}
	defer configmap.ChangeGlobalConfig(ctx, t, topts.ParamsRun, "pipelines-as-code", cfgMapData)()

	_, f := tgitea.TestPR(t, topts)
	defer f()
	adminCnx := topts.GiteaCNX

	secondcnx, _, err := tgitea.CreateGiteaUserSecondCnx(topts, topts.TargetRefName, topts.GiteaPassword)
	assert.NilError(t, err)

	topts.PullRequest = tgitea.CreateForkPullRequest(t, topts, secondcnx, "")
	// status of CI is pending because PR sent by unauthorized user
	topts.CheckForStatus = "Skipped"
	tgitea.WaitForStatus(t, topts, topts.PullRequest.Head.Sha, "", false)
	topts.Regexp = regexp.MustCompile(`.*is skipping this commit.*`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	tgitea.PostCommentOnPullRequest(t, topts, okToTestComment)
	// status of CI is success because comment /ok-to-test added by authorized user
	topts.Regexp = successRegexp
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	// push to PR
	tgitea.PushToPullRequest(t, topts, secondcnx, "echo Hello from user "+topts.TargetRefName)

	// get the latest PR for the new sha
	pr, _, err := topts.GiteaCNX.Client().GetPullRequest("pac", topts.PullRequest.Head.Name, topts.PullRequest.Index)
	assert.NilError(t, err)

	// status of CI is pending because pushed to PR and remember-ok-to-test is false
	topts.CheckForStatus = "Skipped"
	tgitea.WaitForStatus(t, topts, pr.Head.Sha, "", false)
	topts.Regexp = regexp.MustCompile(`.*is skipping this commit.*`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	tgitea.PostCommentOnPullRequest(t, topts, okToTestComment)

	// status of CI is success because comment /ok-to-test added by authorized user
	topts.Regexp = successRegexp
	tgitea.WaitForPullRequestCommentMatch(t, topts)
	topts.GiteaCNX = adminCnx
}

// TestGiteaACLCommentsAllowingRememberOkToTestTrue tests when unauthorized user sends a PR the status shows as pending
// unless the authorized user adds a comment like /ok-to-test, When authorized user adds those comments
// the status of CI shows as success. Now non authorized user pushes to PR, the CI will run without
// requiring /ok-to-test again from authorized user.
func TestGiteaACLCommentsAllowingRememberOkToTestTrue(t *testing.T) {
	ctx := context.Background()
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun.yaml",
		},
		ExpectEvents: false,
	}

	topts.ParamsRun, topts.Opts, topts.GiteaCNX, _ = tgitea.Setup(ctx)
	assert.NilError(t, topts.ParamsRun.Clients.NewClients(ctx, &topts.ParamsRun.Info))
	_, f := tgitea.TestPR(t, topts)
	defer f()
	adminCnx := topts.GiteaCNX
	secondcnx, _, err := tgitea.CreateGiteaUserSecondCnx(topts, topts.TargetRefName, topts.GiteaPassword)
	assert.NilError(t, err)

	topts.PullRequest = tgitea.CreateForkPullRequest(t, topts, secondcnx, "")
	// status of CI is pending because PR sent by unauthorized user
	topts.CheckForStatus = "Skipped"
	tgitea.WaitForStatus(t, topts, topts.PullRequest.Head.Sha, "", false)
	topts.Regexp = regexp.MustCompile(`.*is skipping this commit.*`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	tgitea.PostCommentOnPullRequest(t, topts, okToTestComment)
	// status of CI is success because comment /ok-to-test added by authorized user
	topts.Regexp = successRegexp
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	// push to PR
	tgitea.PushToPullRequest(t, topts, secondcnx, "echo Hello from user "+topts.TargetRefName)

	// status of CI is success because comment /ok-to-test added by authorized user before
	topts.Regexp = successRegexp
	tgitea.WaitForPullRequestCommentMatch(t, topts)
	topts.GiteaCNX = adminCnx
}

func TestGiteaPolicyAllowedOwnerFiles(t *testing.T) {
	topts := &tgitea.TestOpts{
		OnOrg:                 true,
		NoPullRequestCreation: true,
		TargetEvent:           triggertype.PullRequest.String(),
		Settings: &v1alpha1.Settings{
			Policy: &v1alpha1.Policy{
				PullRequest: []string{"normal"},
			},
		},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	adminCnx := topts.GiteaCNX
	targetRef := topts.TargetRefName
	orgName := "org-" + topts.TargetRefName
	topts.Opts.Organization = orgName

	normalTeam, err := tgitea.CreateTeam(topts, orgName, "normal")
	assert.NilError(t, err)
	normalUserNamePasswd := fmt.Sprintf("normal-%s", topts.TargetRefName)
	_, normalUser, err := tgitea.CreateGiteaUserSecondCnx(topts, normalUserNamePasswd, normalUserNamePasswd)
	assert.NilError(t, err)
	_, err = topts.GiteaCNX.Client().AddTeamMember(normalTeam.ID, normalUser.UserName)
	assert.NilError(t, err)

	// create an allowed user w
	allowedUserNamePasswd := fmt.Sprintf("allowed-%s", topts.TargetRefName)
	allowedCnx, allowedUser, err := tgitea.CreateGiteaUserSecondCnx(topts, allowedUserNamePasswd, allowedUserNamePasswd)
	assert.NilError(t, err)

	prmap := map[string]string{
		"OWNERS":         "testdata/OWNERS",
		"OWNERS_ALIASES": "testdata/OWNERS_ALIASES",
	}
	entries, err := payload.GetEntries(prmap, topts.TargetNS, topts.DefaultBranch, topts.TargetEvent, map[string]string{
		"Approver": allowedUser.UserName,
	})
	assert.NilError(t, err)

	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.DefaultBranch,
		BaseRefName:   topts.DefaultBranch,
	}
	// push OWNERS file to main
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)
	scmOpts.TargetRefName = targetRef

	newyamlFiles := map[string]string{".tekton/pr.yaml": "testdata/pipelinerun.yaml"}
	newEntries, err := payload.GetEntries(newyamlFiles, topts.TargetNS, topts.DefaultBranch, topts.TargetEvent, map[string]string{})
	assert.NilError(t, err)
	_ = scm.PushFilesToRefGit(t, scmOpts, newEntries)

	npr := tgitea.CreateForkPullRequest(t, topts, allowedCnx, "")
	waitOpts := twait.Opts{
		RepoName:        topts.TargetNS,
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       npr.Head.Sha,
	}
	_, err = twait.UntilRepositoryUpdated(context.Background(), topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)
	time.Sleep(5 * time.Second) // “Evil does not sleep. It waits.” - Galadriel

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 1, "should have only one pipelinerun, but we have: %d", len(prs.Items))

	firstpr := prs.Items[0]
	topts.CheckForStatus = "success"
	generatename := strings.TrimSuffix(firstpr.GetGenerateName(), "-")
	tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, fmt.Sprintf("%s / %s", settings.PACApplicationNameDefaultValue, generatename), false)
	topts.GiteaCNX = adminCnx
}

// TestGiteaPolicyOnComment tests that on-comments annotation respect the pull_requests policy.
func TestGiteaPolicyOnComment(t *testing.T) {
	topts := &tgitea.TestOpts{
		OnOrg:                true,
		SkipEventsCheck:      true,
		CheckForNumberStatus: 2,
		TargetEvent:          triggertype.PullRequest.String(),
		Settings: &v1alpha1.Settings{
			Policy: &v1alpha1.Policy{
				PullRequest: []string{"pull_requester"},
			},
		},
		YAMLFiles: map[string]string{".tekton/pr.yaml": "testdata/pipelinerun-on-comment-annotation.yaml"},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	adminCnx := topts.GiteaCNX
	topts.ParamsRun.Clients.Log.Infof("Repo CRD %s has been created with Policy: %+v", topts.TargetRefName, topts.Settings.Policy)
	orgName := "org-" + topts.TargetRefName
	topts.Opts.Organization = orgName

	// create normal team on org and add user normal onto it
	normalTeam, err := tgitea.CreateTeam(topts, orgName, "normal")
	assert.NilError(t, err)
	normalUserNamePasswd := fmt.Sprintf("normal-%s", topts.TargetRefName)
	normalUserCnx, normalUser, err := tgitea.CreateGiteaUserSecondCnx(topts, normalUserNamePasswd, normalUserNamePasswd)
	assert.NilError(t, err)
	_, err = topts.GiteaCNX.Client().AddTeamMember(normalTeam.ID, normalUser.UserName)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("User %s has been added to team %s", normalUser.UserName, normalTeam.Name)
	tgitea.CreateForkPullRequest(t, topts, normalUserCnx, "")

	topts.GiteaCNX = normalUserCnx
	tgitea.PostCommentOnPullRequest(t, topts, "/hello-world")
	topts.CheckForStatus = "Skipped"
	topts.CheckForNumberStatus = 1
	topts.Regexp = regexp.MustCompile(`.*Pipelines as Code CI is skipping this commit.*`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)
	tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, settings.PACApplicationNameDefaultValue, false)

	topts.GiteaCNX = adminCnx
	pullRequesterTeam, err := tgitea.CreateTeam(topts, orgName, "pull_requester")
	assert.NilError(t, err)
	pullRequesterUserNamePasswd := fmt.Sprintf("pullRequester-%s", topts.TargetRefName)
	pullRequesterUserCnx, pullRequesterUser, err := tgitea.CreateGiteaUserSecondCnx(topts, pullRequesterUserNamePasswd, pullRequesterUserNamePasswd)
	assert.NilError(t, err)
	_, err = topts.GiteaCNX.Client().AddTeamMember(pullRequesterTeam.ID, pullRequesterUser.UserName)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("User %s has been added to team %s", pullRequesterUser.UserName, pullRequesterTeam.Name)
	topts.GiteaCNX = pullRequesterUserCnx
	tgitea.PostCommentOnPullRequest(t, topts, "/hello-world")
	topts.Regexp = successRegexp
	tgitea.WaitForPullRequestCommentMatch(t, topts)
	topts.GiteaCNX = adminCnx
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -run TestGiteaPush ."
// End:
