package bitbucketcloud

import (
	"fmt"
	"testing"

	"github.com/ktrysmt/go-bitbucket"
	"github.com/mitchellh/mapstructure"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud/types"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/fs"
)

func MakePR(t *testing.T, bprovider bitbucketcloud.Provider, runcnx *params.Run, bcrepo *bitbucket.Repository, opts options.E2E, title, targetRefName string,
	entries map[string]string,
) (*types.PullRequest, *bitbucket.RepositoryBranch) {
	repobranch, err := MakePush(t, bprovider, runcnx, bcrepo, opts, title, targetRefName, entries)
	assert.NilError(t, err)

	intf, err := bprovider.Client().Repositories.PullRequests.Create(&bitbucket.PullRequestsOptions{
		Owner:        opts.Organization,
		RepoSlug:     opts.Repo,
		Title:        title,
		Message:      "A new PR for testing",
		SourceBranch: targetRefName,
	})
	assert.NilError(t, err)

	pr := &types.PullRequest{}
	err = mapstructure.Decode(intf, pr)
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Created PR %s", pr.Links.HTML.HRef)

	return pr, repobranch
}

func MakePush(t *testing.T, bprovider bitbucketcloud.Provider, runcnx *params.Run, bcrepo *bitbucket.Repository, opts options.E2E, title, targetRefName string,
	entries map[string]string,
) (*bitbucket.RepositoryBranch, error) {
	commitAuthor := "OpenShift Pipelines E2E test"
	commitEmail := "e2e-pipelines@redhat.com"

	bbfiles := make([]bitbucket.File, 0, len(entries))
	for k, v := range entries {
		tmpfile := fs.NewFile(t, "pipelinerun", fs.WithContent(v))
		defer tmpfile.Remove()
		bbfiles = append(bbfiles, bitbucket.File{
			Name: k,
			Path: tmpfile.Path(),
		})
	}

	err := bprovider.Client().Workspaces.Repositories.Repository.WriteFileBlob(&bitbucket.RepositoryBlobWriteOptions{
		Owner:    opts.Organization,
		RepoSlug: opts.Repo,
		Files:    bbfiles,
		Message:  title,
		Branch:   targetRefName,
		Author:   fmt.Sprintf("%s <%s>", commitAuthor, commitEmail),
	})
	if err != nil {
		return nil, err
	}

	runcnx.Clients.Log.Infof("Using repo %s branch %s", bcrepo.Full_name, targetRefName)

	repobranch, err := bprovider.Client().Repositories.Repository.GetBranch(&bitbucket.RepositoryBranchOptions{
		Owner:      opts.Organization,
		RepoSlug:   opts.Repo,
		BranchName: targetRefName,
	})
	if err != nil {
		return nil, err
	}

	return repobranch, nil
}
