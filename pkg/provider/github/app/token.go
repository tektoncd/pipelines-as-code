package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"knative.dev/pkg/logging"
)

type Install struct {
	request   *http.Request
	run       *params.Run
	repo      *v1alpha1.Repository
	ghClient  *github.Provider
	namespace string
}

func NewInstallation(req *http.Request, run *params.Run, repo *v1alpha1.Repository, gh *github.Provider, namespace string) *Install {
	if req == nil {
		req = &http.Request{}
	}
	return &Install{
		request:   req,
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

	// Generate a JWT token for authentication
	jwtToken, err := ip.GenerateJWT(ctx)
	if err != nil {
		return "", "", 0, err
	}

	// Get owner and repo from the repository URL
	repoURL, err := url.Parse(ip.repo.Spec.URL)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to parse repository URL: %w", err)
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

	if ip.ghClient.APIURL == nil {
		return "", "", 0, fmt.Errorf("github client APIURL is nil")
	}
	apiURL := *ip.ghClient.APIURL
	enterpriseHost := ""
	if repoURL.Host != "" && repoURL.Host != "github.com" {
		enterpriseHost = repoURL.Host
		if apiURL == keys.PublicGithubAPIURL {
			apiURL = fmt.Sprintf("https://%s/api/v3", strings.TrimSuffix(enterpriseHost, "/"))
		}
	}

	client, _, _ := github.MakeClient(ctx, apiURL, jwtToken)
	// Directly get the installation for the repository
	installation, _, err := client.Apps.FindRepositoryInstallation(ctx, owner, repoName)
	if err != nil {
		// Fallback to finding organization installation if repository installation is not found
		installation, _, err = client.Apps.FindOrganizationInstallation(ctx, owner)
		if err != nil {
			// Fallback to finding user installation if organization installation is not found
			installation, _, err = client.Apps.FindUserInstallation(ctx, owner)
		}
	}

	if err != nil {
		return "", "", 0, fmt.Errorf("could not find repository, organization or user installation for %s/%s: %w", owner, repoName, err)
	}

	if installation.ID == nil {
		return "", "", 0, fmt.Errorf("github App installation found but contained no ID. This is likely a bug")
	}

	installationID := *installation.ID
	token, err := ip.ghClient.GetAppToken(ctx, ip.run.Clients.Kube, enterpriseHost, installationID, ip.namespace)
	if err != nil {
		logger.Warnf("Could not get a token for installation ID %d: %v", installationID, err)
		// Return with the installation ID even if token generation fails,
		// as some operations might only need the ID.
		return enterpriseHost, "", installationID, nil
	}

	return enterpriseHost, token, installationID, nil
}

// JWTClaim represents the JWT claims for the GitHub App.
type JWTClaim struct {
	Issuer int64 `json:"iss"`
	jwt.RegisteredClaims
}

// GenerateJWT generates a JWT token for the GitHub App.
// It retrieves the application ID and private key, sets the claims, and signs the token.
func (ip *Install) GenerateJWT(ctx context.Context) (string, error) {
	// TODO: move this out of here
	gh := github.New()
	gh.Run = ip.run
	applicationID, privateKey, err := gh.GetAppIDAndPrivateKey(ctx, ip.namespace, ip.run.Clients.Kube)
	if err != nil {
		return "", err
	}

	// The expirationTime claim identifies the expiration time on or after which the JWT MUST NOT be accepted for processing.
	// Value cannot be longer duration.
	// See https://datatracker.ietf.org/doc/html/rfc7519#section-4.1.4
	expirationTime := time.Now().Add(5 * time.Minute)
	claims := &JWTClaim{
		Issuer: applicationID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	parsedPK, err := jwt.ParseRSAPrivateKeyFromPEM(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	tokenString, err := token.SignedString(parsedPK)
	if err != nil {
		return "", fmt.Errorf("failed to sign private key: %w", err)
	}
	return tokenString, nil
}
