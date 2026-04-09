package bitbucketdatacenter

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/setup"

	"github.com/jenkins-x/go-scm/scm"
	"github.com/jenkins-x/go-scm/scm/driver/stash"
	"github.com/jenkins-x/go-scm/scm/transport/oauth2"
	"gotest.tools/v3/assert"
)

func Setup(ctx context.Context) (context.Context, *params.Run, options.E2E, *scm.Client, error) {
	bitbucketDataCenterUser := os.Getenv("TEST_BITBUCKET_DATA_CENTER_USER")
	bitbucketDataCenterToken := os.Getenv("TEST_BITBUCKET_DATA_CENTER_TOKEN")
	bitbucketWSOwner := os.Getenv("TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY")
	bitbucketDataCenterAPIURL := os.Getenv("TEST_BITBUCKET_DATA_CENTER_API_URL")

	if err := setup.RequireEnvs(
		"TEST_BITBUCKET_DATA_CENTER_USER",
		"TEST_BITBUCKET_DATA_CENTER_TOKEN",
		"TEST_BITBUCKET_DATA_CENTER_E2E_REPOSITORY",
		"TEST_BITBUCKET_DATA_CENTER_API_URL",
		"TEST_BITBUCKET_DATA_CENTER_WEBHOOK_SECRET",
	); err != nil {
		return ctx, nil, options.E2E{}, nil, err
	}

	split := strings.Split(bitbucketWSOwner, "/")

	run := params.New()
	if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
		return ctx, nil, options.E2E{}, nil, err
	}
	e2eoptions := options.E2E{
		Organization: split[0],
		Repo:         split[1],
		UserName:     bitbucketDataCenterUser,
		Password:     bitbucketDataCenterToken,
	}

	event := info.NewEvent()
	event.Provider = &info.Provider{
		Token: bitbucketDataCenterToken,
		URL:   bitbucketDataCenterAPIURL,
		User:  bitbucketDataCenterUser,
	}

	client, err := stash.New(bitbucketDataCenterAPIURL)
	if err != nil {
		return ctx, nil, options.E2E{}, nil, err
	}

	client.Client = &http.Client{
		Transport: &oauth2.Transport{
			Source: oauth2.StaticTokenSource(
				&scm.Token{
					Token: bitbucketDataCenterToken,
				},
			),
		},
	}

	return ctx, run, e2eoptions, client, nil
}

func TearDownNs(ctx context.Context, t *testing.T, runcnx *params.Run, targetNS string) {
	if os.Getenv("TEST_NOCLEANUP") == "true" {
		runcnx.Clients.Log.Infof("Not cleaning up and closing PR since TEST_NOCLEANUP is set")
		return
	}

	repository.NSTearDown(ctx, t, runcnx, targetNS)
}

func TearDown(ctx context.Context, t *testing.T, runcnx *params.Run, client *scm.Client, prID int, orgAndRepo, ref string) {
	if os.Getenv("TEST_NOCLEANUP") == "true" {
		runcnx.Clients.Log.Infof("Not cleaning up and closing PR since TEST_NOCLEANUP is set")
		return
	}

	if prID != -1 {
		runcnx.Clients.Log.Infof("Deleting PR #%d", prID)
		_, err := client.PullRequests.DeletePullRequest(ctx, orgAndRepo, prID)
		assert.NilError(t, err)
	}

	if ref != "" {
		runcnx.Clients.Log.Infof("Deleting Branch %s", ref)
		_, err := client.Git.DeleteRef(ctx, orgAndRepo, ref)
		assert.NilError(t, err)
	}
}
