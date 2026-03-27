//go:build e2e

package test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/go-github/v84/github"
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
	var g *tgithub.PRTest
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-tag")
	repoinfo, resp, err := ghcnx.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Errorf("Repository %s not found in %s", opts.Repo, opts.Organization)
	}
	err = tgithub.CreateCRD(ctx, t, repoinfo, runcnx, opts, ghcnx, targetNS)
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Repository %s has been created successfully", targetNS)

	tags := []string{"v1.0.0", "v1.0.0-lightweight"}
	for _, tag := range tags {
		comment := "/test tag:" + tag
		sha := ""
		targetRefName := "release-" + tag
		label := ""
		if tag == "v1.0.0" {
			label = "Testing GitHub commit comment on tag with GitHub apps integration"
		} else {
			label = "Testing GitHub commit comment on lightweight tag with GitHub apps integration"
		}

		numberOfPRs := 1

		ref, resp, err := ghcnx.Client().Git.GetRef(ctx, opts.Organization, opts.Repo, "refs/tags/"+tag)
		if err != nil && resp.StatusCode == http.StatusNotFound {
			runcnx.Clients.Log.Infof("Tag %s is not found on the repository", tag)
			runcnx.Clients.Log.Infof("Creating tag %s on the repository", tag)
			entries, err := payload.GetEntries(map[string]string{".tekton/pipelinerun-on-tag.yaml": "testdata/pipelinerun-on-tag.yaml"},
				targetNS, "refs/tags/*", "push", map[string]string{})
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

			sha = branch.GetCommit().GetSHA()

			// when we're testing a lightweight tag, we need to set the opts.LightweightTag to true
			// so that we don't create an annotated tag. on creating lightweight tag, ref contains the commit SHA directly.
			opts.LightweightTag = tag == "v1.0.0-lightweight"
			runcnx.Clients.Log.Infof("is Lightweight tag: %t", opts.LightweightTag)
			_, err = tgithub.CreateTag(ctx, t, runcnx, ghcnx, opts, sha, tag)
			assert.NilError(t, err)
			numberOfPRs++
		} else {
			// else if tag is already created, we need to get the tag object to get the commit SHA
			if tag == "v1.0.0" {
				// if tag is annotated tag, we need to get the tag object to get the commit SHA
				runcnx.Clients.Log.Infof("Tag %s is already created on the repository", tag)
				runcnx.Clients.Log.Infof("Getting tag %s", tag)
				tagRef, _, err := ghcnx.Client().Git.GetTag(ctx, opts.Organization, opts.Repo, ref.GetObject().GetSHA())
				assert.NilError(t, err)
				sha = tagRef.GetObject().GetSHA()
			} else {
				// else if tag is lightweight tag, we need to get the commit SHA from the ref
				runcnx.Clients.Log.Infof("Lightweight tag %s is already created on the repository", tag)
				sha = ref.GetObject().GetSHA()
			}
		}

		g = &tgithub.PRTest{
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

		runcnx.Clients.Log.Infof("%s on tag commit", comment)
		_, _, err = ghcnx.Client().Repositories.CreateComment(ctx,
			opts.Organization,
			opts.Repo, sha, // this is the commit sha of the tag v1.0.0
			&github.RepositoryComment{Body: github.Ptr(comment)})
		assert.NilError(t, err)

		waitOpts := twait.Opts{
			RepoName:        targetNS,
			Namespace:       targetNS,
			MinNumberStatus: numberOfPRs,
			PollTimeout:     twait.DefaultTimeout,
			TargetSHA:       sha, // this is the commit sha of the tag v1.0.0
		}
		runcnx.Clients.Log.Info("Waiting for PipelineRun to be created")
		err = twait.UntilPipelineRunCreated(ctx, runcnx.Clients, waitOpts)
		assert.NilError(t, err)
	}
}
