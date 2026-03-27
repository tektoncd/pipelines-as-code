package bootstrap

import (
	"encoding/json"
	"fmt"

	"github.com/google/go-github/scrape"
	"github.com/google/go-github/v84/github"
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
			"push",
		},
		DefaultPermissions: &github.InstallationPermissions{
			Checks:       github.Ptr("write"),
			Contents:     github.Ptr("write"),
			Issues:       github.Ptr("write"),
			Members:      github.Ptr("read"),
			Metadata:     github.Ptr("read"),
			PullRequests: github.Ptr("write"),
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
