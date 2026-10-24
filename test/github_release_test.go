//go:build e2e

package test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
)

func TestGithubGHEReleaseAnnotatedTag(t *testing.T) {
	runGithubReleaseTest(t, false, false, "v0.1.0", "release-", "Testing GitHub release event with annotated tag")
}

func TestGithubGHEReleaseLightweightTag(t *testing.T) {
	runGithubReleaseTest(t, false, true, "v0.2.0", "release-lw-", "Testing GitHub release event with lightweight tag")
}

func TestGithubGHEWebhookReleaseAnnotatedTag(t *testing.T) {
	runGithubReleaseTest(t, true, false, "v0.1.0", "release-wh-", "Testing GitHub webhook release event with annotated tag")
}

func TestGithubGHEWebhookReleaseLightweightTag(t *testing.T) {
	runGithubReleaseTest(t, true, true, "v0.2.0", "release-wh-lw-", "Testing GitHub webhook release event with lightweight tag")
}

func runGithubReleaseTest(t *testing.T, webhook, lightweight bool, tagPrefix, branchPrefix, label string) {
	t.Helper()
	ctx := context.Background()
	ctx, runcnx, opts, ghcnx, err := tgithub.Setup(ctx, true, webhook)
	assert.NilError(t, err)

	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-release")

	repoinfo, resp, err := ghcnx.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Fatalf("Repository %s not found in %s", opts.Repo, opts.Organization)
	}

	err = tgithub.CreateCRD(ctx, t, repoinfo, runcnx, opts, ghcnx, targetNS)
	assert.NilError(t, err)

	tagName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix(tagPrefix)
	targetRefName := branchPrefix + tagName

	yamlFiles := map[string]string{
		filepath.Join(".tekton", "pipelinerun.yaml"): "testdata/pipelinerun.yaml",
	}
	entries, err := payload.GetEntries(yamlFiles,
		targetNS, "refs/tags/*", triggertype.Release.String(), map[string]string{})
	assert.NilError(t, err)

	cloneURL, err := scm.MakeGitCloneURL(repoinfo.GetCloneURL(), "git", *ghcnx.Token)
	assert.NilError(t, err)
	scmOpts := scm.Opts{
		GitURL:        cloneURL,
		TargetRefName: targetRefName,
		BaseRefName:   repoinfo.GetDefaultBranch(),
		WebURL:        repoinfo.GetHTMLURL(),
		Log:           runcnx.Clients.Log,
		CommitTitle:   label,
	}
	scm.PushFilesToRefGit(t, &scmOpts, entries)

	branch, _, err := ghcnx.Client().Repositories.GetBranch(ctx, opts.Organization, opts.Repo, targetRefName, 1)
	assert.NilError(t, err)
	sha := branch.GetCommit().GetSHA()

	opts.LightweightTag = lightweight
	_, err = tgithub.CreateTag(ctx, t, runcnx, ghcnx, opts, sha, tagName)
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Tag %s created on commit %s (lightweight=%v)", tagName, sha, lightweight)

	release, err := tgithub.CreateRelease(ctx, ghcnx, opts, tagName)
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Release %s created for tag %s", release.GetName(), tagName)

	defer func() {
		runcnx.Clients.Log.Infof("Cleaning up release %s and tag %s", release.GetName(), tagName)
		assert.NilError(t, tgithub.DeleteRelease(ctx, ghcnx, opts, release.GetID()))
		assert.NilError(t, tgithub.DeleteTag(ctx, ghcnx, opts, tagName))
		_, _ = ghcnx.Client().Git.DeleteRef(ctx, opts.Organization, opts.Repo, "refs/heads/"+targetRefName)
	}()

	g := &tgithub.PRTest{
		Label:           label,
		Cnx:             runcnx,
		Logger:          runcnx.Clients.Log,
		Options:         opts,
		Provider:        ghcnx,
		TargetNamespace: targetNS,
		PRNumber:        -1,
		SHA:             sha,
	}
	defer g.TearDown(ctx, t)

	waitOpts := twait.Opts{
		Namespace:       targetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{sha},
	}
	runcnx.Clients.Log.Info("Waiting for release PipelineRun to be created")
	_, err = twait.UntilPipelineRunCreated(ctx, runcnx.Clients, waitOpts)
	assert.NilError(t, err)

	sopt := twait.SuccessOpt{
		Title:           label,
		OnEvent:         triggertype.Release.String(),
		TargetNS:        targetNS,
		NumberofPRMatch: 1,
		SHA:             sha,
	}
	twait.Succeeded(ctx, t, runcnx, opts, sopt)
}
