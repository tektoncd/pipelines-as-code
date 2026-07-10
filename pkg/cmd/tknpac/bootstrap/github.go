package bootstrap

import (
	"encoding/json"
	"fmt"

	"github.com/google/go-github/scrape"
	githubv84 "github.com/google/go-github/v84/github"
	"github.com/google/go-github/v85/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
)

// generateManifest generate manifest from the given options.
func generateManifest(opts *bootstrapOpts) ([]byte, error) {
	sc := scrape.AppManifest{
		Name:           github.Ptr(opts.GithubApplicationName),
		URL:            github.Ptr(opts.GithubApplicationURL),
		HookAttributes: map[string]string{"url": opts.RouteName},
		RedirectURL:    github.Ptr(fmt.Sprintf("http://localhost:%d", opts.webserverPort)),
		Description:    github.Ptr("Pipeline as Code Application"),
		Public:         github.Ptr(true),
		DefaultEvents: []string{
			"check_run",
			"check_suite",
			"issue_comment",
			"commit_comment",
			triggertype.PullRequest.String(),
			triggertype.PullRequestReview.String(),
			"push",
		},
		DefaultPermissions: &githubv84.InstallationPermissions{
			Checks:       githubv84.Ptr("write"),
			Contents:     githubv84.Ptr("write"),
			Issues:       githubv84.Ptr("write"),
			Members:      githubv84.Ptr("read"),
			Metadata:     githubv84.Ptr("read"),
			PullRequests: githubv84.Ptr("write"),
		},
	}
	return json.Marshal(sc)
}

// getGHClient get github client.
func getGHClient(opts *bootstrapOpts) (*github.Client, error) {
	if opts.GithubAPIURL == defaultPublicGithub {
		return github.NewClient(nil), nil
	}

	gprovider, err := github.NewClient(nil).WithEnterpriseURLs(opts.GithubAPIURL, "")
	if err != nil {
		return nil, err
	}
	return gprovider, nil
}
