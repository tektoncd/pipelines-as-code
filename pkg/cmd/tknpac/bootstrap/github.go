package bootstrap

import (
	"encoding/json"
	"fmt"

	"github.com/google/go-github/scrape"
	"github.com/google/go-github/v91/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
)

// generateManifest generate manifest from the given options.
func generateManifest(opts *bootstrapOpts) ([]byte, error) {
	sc := scrape.AppManifest{
		Name:           new(opts.GithubApplicationName),
		URL:            new(opts.GithubApplicationURL),
		HookAttributes: map[string]string{"url": opts.RouteName},
		RedirectURL:    new(fmt.Sprintf("http://localhost:%d", opts.webserverPort)),
		Description:    new("Pipeline as Code Application"),
		Public:         new(true),
		DefaultEvents: []string{
			"check_run",
			"check_suite",
			"issue_comment",
			"commit_comment",
			triggertype.PullRequest.String(),
			"push",
		},
		DefaultPermissions: &github.InstallationPermissions{
			Checks:       new("write"),
			Contents:     new("write"),
			Issues:       new("write"),
			Members:      new("read"),
			Metadata:     new("read"),
			PullRequests: new("write"),
		},
	}
	return json.Marshal(sc)
}

// getGHClient get github client.
func getGHClient(opts *bootstrapOpts) (*github.Client, error) {
	if opts.GithubAPIURL == defaultPublicGithub {
		return github.NewClient()
	}

	gprovider, err := github.NewClient(github.WithEnterpriseURLs(opts.GithubAPIURL, opts.GithubAPIURL))
	if err != nil {
		return nil, err
	}
	return gprovider, nil
}
