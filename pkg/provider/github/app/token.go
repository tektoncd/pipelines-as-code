package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	gt "github.com/google/go-github/v61/github"
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

	repoList []string
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

func (ip *Install) GetAndUpdateInstallationID(ctx context.Context) (string, string, int64, error) {
	var (
		enterpriseHost, token string
		installationID        int64
	)
	jwtToken, err := ip.GenerateJWT(ctx)
	if err != nil {
		return "", "", 0, err
	}

	if ip.ghClient.APIURL == nil {
		return "", "", 0, fmt.Errorf("github client APIURL is nil")
	}
	apiURL := *ip.ghClient.APIURL
	repoURL, err := url.Parse(ip.repo.Spec.URL)
	if err != nil {
		return "", "", 0, fmt.Errorf("failed to parse repository URL: %w", err)
	}
	enterpriseHost = ""
	if repoURL.Host != "" && repoURL.Host != "github.com" {
		enterpriseHost = repoURL.Host
		if apiURL == keys.PublicGithubAPIURL {
			apiURL = fmt.Sprintf("https://%s/api/v3", strings.TrimSuffix(enterpriseHost, "/"))
		}
	}

	logger := logging.FromContext(ctx)
	opt := &gt.ListOptions{PerPage: ip.ghClient.PaginedNumber}
	client, _, _ := github.MakeClient(ctx, apiURL, jwtToken)
	installationData := []*gt.Installation{}
	for {
		installationSet, resp, err := client.Apps.ListInstallations(ctx, opt)
		if err != nil {
			return "", "", 0, err
		}
		installationData = append(installationData, installationSet...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	/* each installationID can have list of repository
	ref: https://docs.github.com/en/developers/apps/building-github-apps/authenticating-with-github-apps#authenticating-as-an-installation ,
	     https://docs.github.com/en/rest/apps/installations?apiVersion=2022-11-28#list-repositories-accessible-to-the-app-installation */
	for i := range installationData {
		if installationData[i].ID == nil {
			return "", "", 0, fmt.Errorf("installation ID is nil")
		}
		if *installationData[i].ID != 0 {
			token, err = ip.ghClient.GetAppToken(ctx, ip.run.Clients.Kube, enterpriseHost, *installationData[i].ID, ip.namespace)
			// While looping on the list of installations, there could be cases where we can't
			// obtain a token for installation. In a test I did for GitHub App with ~400
			// installations, there were 3 failing consistently with:
			// "could not refresh installation id XXX's token: received non 2xx response status "403 Forbidden".
			// If there is a matching installation after the failure, we miss it. So instead of
			// failing, we just log the error and continue. Token is "".
			if err != nil {
				logger.Warn(err)
				continue
			}
		}
		exist, err := ip.matchRepos(ctx)
		if err != nil {
			return "", "", 0, err
		}
		if exist {
			installationID = *installationData[i].ID
			break
		}
	}
	return enterpriseHost, token, installationID, nil
}

// matchRepos matching github repositories to its installation IDs.
func (ip *Install) matchRepos(ctx context.Context) (bool, error) {
	installationRepoList, err := github.ListRepos(ctx, ip.ghClient)
	if err != nil {
		return false, err
	}
	ip.repoList = append(ip.repoList, installationRepoList...)
	for i := range installationRepoList {
		// If URL matches with repo spec url then we can break for loop
		if installationRepoList[i] == ip.repo.Spec.URL {
			return true, nil
		}
	}
	return false, nil
}

type JWTClaim struct {
	Issuer int64 `json:"iss"`
	jwt.RegisteredClaims
}

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
