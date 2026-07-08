//go:build e2e

package test

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitlab "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitlab"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGitlabGitOpsCommandTestOnPush(t *testing.T) {
	topts := &tgitlab.TestOpts{
		NoMRCreation: true,
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-on-push.yaml": "testdata/pipelinerun-on-push.yaml",
	}, topts.TargetNS, topts.TargetNS, triggertype.Push.String(), map[string]string{})
	assert.NilError(t, err)

	title := "Test GitOps Commands on Push - " + topts.TargetNS
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetNS,
		BaseRefName:   topts.DefaultBranch,
		CommitTitle:   title,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)
	topts.ParamsRun.Clients.Log.Infof("Branch %s has been created and pushed with files", topts.TargetNS)

	branch, _, err := topts.GLProvider.Client().Branches.GetBranch(topts.ProjectID, topts.TargetNS)
	assert.NilError(t, err)

	waitOpts := wait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     wait.DefaultTimeout,
		TargetSHA:       []string{branch.Commit.ID},
	}

	_, err = wait.UntilPipelineRunCreated(ctx, topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)

	commentOpts := &gitlab.PostCommitCommentOptions{
		Note: gitlab.Ptr("/test branch:" + topts.TargetNS),
	}
	cc, _, err := topts.GLProvider.Client().Commits.PostCommitComment(topts.ProjectID, branch.Commit.ID, commentOpts)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Commit comment %s has been created", cc.Note)

	sopt := wait.SuccessOpt{
		Title:           title,
		OnEvent:         opscomments.TestSingleCommentEventType.String(),
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 2,
	}
	wait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)
	prsNew, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prsNew.Items) == 2)
}

func TestGitlabGitOpsCommandCancelOnPush(t *testing.T) {
	topts := &tgitlab.TestOpts{
		NoMRCreation: true,
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-on-push.yaml": "testdata/pipelinerun-on-push.yaml",
	}, topts.TargetNS, topts.TargetNS, triggertype.Push.String(), map[string]string{})
	assert.NilError(t, err)

	title := "Test GitOps Commands on Push - " + topts.TargetNS
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetNS,
		BaseRefName:   topts.DefaultBranch,
		CommitTitle:   title,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)
	topts.ParamsRun.Clients.Log.Infof("Branch %s has been created and pushed with files", topts.TargetNS)

	branch, _, err := topts.GLProvider.Client().Branches.GetBranch(topts.ProjectID, topts.TargetNS)
	assert.NilError(t, err)

	commentOpts := &gitlab.PostCommitCommentOptions{
		Note: gitlab.Ptr("/test branch:" + topts.TargetNS),
	}
	cc, _, err := topts.GLProvider.Client().Commits.PostCommitComment(topts.ProjectID, branch.Commit.ID, commentOpts)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Commit comment %s has been created", cc.Note)

	numberOfStatus := 2
	waitOpts := wait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: numberOfStatus,
		PollTimeout:     wait.DefaultTimeout,
		TargetSHA:       []string{branch.Commit.ID},
	}

	_, err = wait.UntilPipelineRunCreated(ctx, topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)

	prsNew, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prsNew.Items) == numberOfStatus)

	commentOpts = &gitlab.PostCommitCommentOptions{
		Note: gitlab.Ptr("/cancel pipelinerun-on-push branch:" + topts.TargetNS),
	}
	cc, _, err = topts.GLProvider.Client().Commits.PostCommitComment(topts.ProjectID, branch.Commit.ID, commentOpts)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Commit comment %s has been created", cc.Note)

	_, err = wait.UntilPipelineRunHasReason(ctx, topts.ParamsRun.Clients, v1.PipelineRunReasonCancelled, waitOpts)
	assert.NilError(t, err)
}

func TestGitlabGitOpsCommandTestOnTag(t *testing.T) {
	topts := &tgitlab.TestOpts{
		NoMRCreation: true,
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	tagName := "v1.0.0"
	comment := "/test tag:" + tagName
	targetBranch := "release-" + tagName
	numberOfPRs := 2

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-on-tag.yaml": "testdata/pipelinerun-on-tag.yaml",
	}, topts.TargetNS, "refs/tags/*", triggertype.Push.String(), map[string]string{})
	assert.NilError(t, err)

	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: targetBranch,
		BaseRefName:   topts.DefaultBranch,
		CommitTitle:   "Test GitOps Commands on Tag - " + topts.TargetNS,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	branch, _, err := topts.GLProvider.Client().Branches.GetBranch(topts.ProjectID, targetBranch)
	assert.NilError(t, err)
	sha := branch.Commit.ID

	_, _, err = topts.GLProvider.Client().Tags.CreateTag(topts.ProjectID, &gitlab.CreateTagOptions{
		TagName: gitlab.Ptr(tagName),
		Ref:     gitlab.Ptr(targetBranch),
	})
	assert.NilError(t, err)

	defer tgitlab.CleanTag(topts.GLProvider.Client(), topts.ProjectID, tagName)

	cc, _, err := topts.GLProvider.Client().Commits.PostCommitComment(topts.ProjectID, sha, &gitlab.PostCommitCommentOptions{
		Note: gitlab.Ptr(comment),
	})
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Commit comment %s has been created", cc.Note)

	waitOpts := wait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: numberOfPRs,
		PollTimeout:     wait.DefaultTimeout,
		TargetSHA:       []string{sha},
	}

	_, err = wait.UntilPipelineRunCreated(ctx, topts.ParamsRun.Clients, waitOpts)
	assert.NilError(t, err)

	prsNew, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prsNew.Items) == numberOfPRs)
}
