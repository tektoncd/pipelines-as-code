package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	gl "gitlab.com/gitlab-org/api/client-go"
)

type gitLabInfo struct {
	Scheme      string
	Host        string
	GroupOrUser string
	Repository  string
	Revision    string
	FilePath    string
}

// extractGitLabInfo generated with chatGPT https://chatgpt.com/share/e3c06a7e-3f16-4891-85c7-832b3e7f25c5
func extractGitLabInfo(gitlabURL string) (*gitLabInfo, error) {
	parsedURL, err := url.Parse(gitlabURL)
	if err != nil {
		return nil, err
	}

	// Regular expression to match the specific GitLab URL pattern
	re := regexp.MustCompile(`^/([^/]+(?:/[^/]+)*)/([^/]+)/-/blob/([^/]+)(/.*)?|^/([^/]+(?:/[^/]+)*)/([^/]+)/-/raw/([^/]+)(/.*)?`)
	matches := re.FindStringSubmatch(parsedURL.Path)

	if len(matches) == 0 {
		return nil, fmt.Errorf("URL does not match the expected GitLab pattern")
	}

	groupOrUser := ""
	repoName := ""
	revision := ""
	filePath := ""

	if matches[1] != "" { // For /blob/ URLs
		groupOrUser = matches[1]
		repoName = matches[2]
		revision = matches[3]
		if len(matches) >= 5 && matches[4] != "" {
			filePath = matches[4][1:] // Remove initial slash
		}
	} else if matches[5] != "" { // For /raw/ URLs
		groupOrUser = matches[5]
		repoName = matches[6]
		revision = matches[7]
		if len(matches) >= 9 && matches[8] != "" {
			filePath = matches[8][1:] // Remove initial slash
		}
	}

	return &gitLabInfo{
		Scheme:      parsedURL.Scheme,
		Host:        parsedURL.Host,
		GroupOrUser: groupOrUser,
		Repository:  repoName,
		Revision:    revision,
		FilePath:    filePath,
	}, nil
}

// GetTaskURI if we are getting a URL from the same URL where the provider is,
// it means we can try to get the file with the provider token.
func (v *Provider) GetTaskURI(_ context.Context, event *info.Event, uri string) (bool, string, error) {
	if ret := provider.CompareHostOfURLS(uri, event.URL); !ret {
		return false, "", nil
	}
	extracted, err := extractGitLabInfo(uri)
	if err != nil {
		return false, "", err
	}

	// Use the existing client if available, otherwise create a temporary one.
	// We avoid storing it to prevent side effects on the provider's state.
	client := v.gitlabClient
	if client == nil {
		baseURL := fmt.Sprintf("%s://%s", extracted.Scheme, extracted.Host)
		var clientErr error
		client, clientErr = gl.NewClient(event.Provider.Token, v.clientOptions(baseURL)...)
		if clientErr != nil {
			return false, "", fmt.Errorf("failed to create gitlab client: %w", clientErr)
		}
	}

	// Construct the project slug for the remote repository
	projectSlug := extracted.GroupOrUser + "/" + extracted.Repository

	// Get the project ID for the remote repository
	project, _, err := client.Projects.GetProject(projectSlug, &gl.GetProjectOptions{})
	if err != nil {
		if errors.Is(err, gl.ErrNotFound) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("failed to get project ID for %s: %w", projectSlug, err)
	}

	// Fetch the file from the remote repository
	opt := &gl.GetRawFileOptions{
		Ref: gl.Ptr(extracted.Revision),
	}
	file, _, err := client.RepositoryFiles.GetRawFile(project.ID, extracted.FilePath, opt)
	if err != nil {
		if errors.Is(err, gl.ErrNotFound) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("failed to get file %s from remote repository: %w", extracted.FilePath, err)
	}
	return true, string(file), nil
}
