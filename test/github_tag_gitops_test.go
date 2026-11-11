//go:build e2e

package test

import (
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/google/go-github/v93/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
)

func TestGithubGHEGitOpsCommentOnTag(t *testing.T) {
	ctx := context.Background()
	ctx, runcnx, opts, ghcnx, err := tgithub.Setup(ctx, true, false)
	assert.NilError(t, err)
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-tag")
	repoinfo, resp, err := ghcnx.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Errorf("Repository %s not found in %s", opts.Repo, opts.Organization)
	}
	err = tgithub.CreateCRD(ctx, t, repoinfo, runcnx, opts, ghcnx, targetNS)
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Repository %s has been created successfully", targetNS)

	g := &tgithub.PRTest{
		Label:           "Testing GitHub commit comment on tag with GitHub apps integration",
		Cnx:             runcnx,
		Logger:          runcnx.Clients.Log,
		Options:         opts,
		Provider:        ghcnx,
		TargetNamespace: targetNS,
		PRNumber:        -1,
	}
	defer g.TearDown(ctx, t)

	tags := []struct {
		name        string
		lightweight bool
	}{
		{
			name: names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("v1.0.0"),
		},
		{
			name:        names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("v1.0.0-lightweight"),
			lightweight: true,
		},
	}

	entries, err := payload.GetEntries(map[string]string{".tekton/pipelinerun-on-tag.yaml": "testdata/pipelinerun-on-tag.yaml"},
		targetNS, "refs/tags/*", triggertype.Push.String(), map[string]string{})
	assert.NilError(t, err)

	cloneURL, err := scm.MakeGitCloneURL(repoinfo.GetCloneURL(), "git", *ghcnx.Token)
	assert.NilError(t, err)

	for _, tt := range tags {
		comment := "/test tag:" + tt.name
		targetRefName := "release-" + tt.name
		commitTitle := g.Label
		if tt.lightweight {
			commitTitle += " (lightweight)"
		}

		scmOpts := scm.Opts{
			GitURL:        cloneURL,
			TargetRefName: targetRefName,
			BaseRefName:   repoinfo.GetDefaultBranch(),
			WebURL:        repoinfo.GetHTMLURL(),
			Log:           runcnx.Clients.Log,
			CommitTitle:   commitTitle,
		}
		sha := scm.PushFilesToRefGit(t, &scmOpts, entries)
		g.SHA = sha
		t.Cleanup(func() {
			if os.Getenv("TEST_NOCLEANUP") == "true" {
				return
			}
			if _, err := ghcnx.Client().Git.DeleteRef(ctx, opts.Organization, opts.Repo, "heads/"+targetRefName); err != nil {
				t.Logf("failed to delete branch %s: %v", targetRefName, err)
			}
		})

		// When testing a lightweight tag, set opts.LightweightTag so that we
		// don't create an annotated tag; the ref then contains the commit SHA
		// directly.
		opts.LightweightTag = tt.lightweight
		_, err = tgithub.CreateTag(ctx, t, runcnx, ghcnx, opts, sha, tt.name)
		assert.NilError(t, err)
		t.Cleanup(func() {
			if os.Getenv("TEST_NOCLEANUP") == "true" {
				return
			}
			if err := tgithub.DeleteTag(ctx, ghcnx, opts, tt.name); err != nil {
				t.Logf("failed to delete tag %s: %v", tt.name, err)
			}
		})

		runcnx.Clients.Log.Infof("%s on tag commit", comment)
		_, _, err = ghcnx.Client().Repositories.CreateComment(ctx,
			opts.Organization,
			opts.Repo, sha, // this is the commit sha of the tag
			github.CreateCommitCommentRequest{Body: comment})
		assert.NilError(t, err)

		// Two PipelineRuns are expected on this SHA: one from the tag push
		// event and one from the /test comment.
		waitOpts := twait.Opts{
			Namespace:       targetNS,
			MinNumberStatus: 2,
			PollTimeout:     twait.DefaultTimeout,
			TargetSHA:       []string{sha}, // this is the commit sha of the tag
		}
		runcnx.Clients.Log.Info("Waiting for PipelineRun to be created")
		_, err = twait.UntilPipelineRunCreated(ctx, runcnx.Clients, waitOpts)
		assert.NilError(t, err)
	}
}
