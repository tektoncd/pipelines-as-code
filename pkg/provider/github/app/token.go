package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"knative.dev/pkg/logging"
)

type Install struct {
	run       *params.Run
	repo      *v1alpha1.Repository
	ghClient  *github.Provider
	namespace string
}

func NewInstallation(run *params.Run, repo *v1alpha1.Repository, gh *github.Provider, namespace string) *Install {
	return &Install{
		run:       run,
		repo:      repo,
		ghClient:  gh,
		namespace: namespace,
	}
}

// GetAndUpdateInstallationID retrieves and updates the installation ID for the GitHub App.
// It generates a JWT token, and directly fetches the installation for the
// repository.
func (ip *Install) GetAndUpdateInstallationID(ctx context.Context) (string, string, int64, error) {
	logger := logging.FromContext(ctx)

	// Get owner and repo from the repository URL
	repoURL, err := url.Parse(ip.repo.Spec.URL)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to parse repository URL: %w", err)
	}
	if repoURL.Scheme != "https" || repoURL.User != nil {
		return "", "", 0, fmt.Errorf("GitHub repository URL must use https without userinfo")
	}
	pathParts := strings.Split(strings.Trim(repoURL.Path, "/"), "/")
	if len(pathParts) != 2 {
		return "", "", 0, fmt.Errorf("invalid repository URL path: %s", repoURL.Path)
	}
	owner := pathParts[0]
	repoName := pathParts[1]
	if owner == "" || repoName == "" {
		return "", "", 0, fmt.Errorf("invalid repository URL: owner or repo name is empty")
	}
	endpoint, err := github.TrustedAPIEndpointForRepository(ctx, ip.run, ip.repo.Spec.URL)
	if err != nil {
		return "", "", 0, err
	}
	apiURL, err := endpoint.BaseURLForClient()
	if err != nil {
		return "", "", 0, err
	}

	// Generate a JWT only after the repository has been checked against the
	// controller-owned GitHub endpoint.
	jwtToken, err := ip.ghClient.GenerateJWT(ctx, ip.namespace, ip.run.Clients.Kube)
	if err != nil {
		return "", "", 0, err
	}

	client, _, _, err := ip.ghClient.MakeClient(ctx, apiURL, jwtToken)
	if err != nil {
		return "", "", 0, err
	}
	// Directly get the installation for the repository
	installation, _, err := client.Apps.GetRepositoryInstallation(ctx, owner, repoName)
	if err != nil {
		// Fallback to finding organization installation if repository installation is not found
		installation, _, err = client.Apps.GetOrganizationInstallation(ctx, owner)
		if err != nil {
			// Fallback to finding user installation if organization installation is not found
			installation, _, err = client.Apps.GetUserInstallation(ctx, owner)
		}
	}

	if err != nil {
		return "", "", 0, fmt.Errorf("could not find repository, organization or user installation for %s/%s: %w", owner, repoName, err)
	}

	if installation.ID == nil {
		return "", "", 0, fmt.Errorf("github App installation found but contained no ID. This is likely a bug")
	}

	installationID := *installation.ID
	token, err := ip.ghClient.GetAppToken(ctx, ip.run.Clients.Kube, endpoint.BaseURL, installationID, ip.namespace)
	if err != nil {
		logger.Warnf("Could not get a token for installation ID %d: %v", installationID, err)
		// Return with the installation ID even if token generation fails,
		// as some operations might only need the ID.
		return endpoint.BaseURL, "", installationID, nil
	}

	return endpoint.BaseURL, token, installationID, nil
}
