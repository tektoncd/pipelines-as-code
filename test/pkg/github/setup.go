package github

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/setup"
)

func Setup(ctx context.Context, onGHE, viaDirectWebhook bool) (context.Context, *params.Run, options.E2E, *github.Provider, error) {
	config, err := setupEnvVars(onGHE, viaDirectWebhook)
	if err != nil {
		return ctx, nil, options.E2E{}, github.New(), err
	}

	split := strings.Split(config.repoOwner, "/")
	repo := ""

	if len(split) > 1 {
		repo = split[1]
	}

	run := params.New()
	if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
		return ctx, nil, options.E2E{}, github.New(), err
	}
	run.Info.Controller = info.GetControllerInfoFromEnvOrDefault()
	ctxWithInfo, err := cctx.GetControllerCtxInfo(ctx, run)
	if err != nil {
		return ctx, nil, options.E2E{}, github.New(), err
	}
	ctx = ctxWithInfo
	e2eoptions := options.E2E{
		Organization:  split[0],
		Repo:          repo,
		DirectWebhook: viaDirectWebhook,
		ControllerURL: config.controllerURL,
	}
	gprovider := github.New()
	gprovider.Run = run
	event := info.NewEvent()

	if err := setupGithubAppToken(ctx, config, viaDirectWebhook, run, gprovider); err != nil {
		return ctx, nil, options.E2E{}, github.New(), err
	}

	event.Provider = &info.Provider{
		Token: config.token,
		URL:   config.url,
	}
	gprovider.Token = &config.token
	// TODO: before PR
	if err := gprovider.SetClient(ctx, run, event, nil, nil); err != nil {
		return ctx, nil, options.E2E{}, github.New(), err
	}

	return ctx, run, e2eoptions, gprovider, nil
}

type envConfig struct {
	token         string
	url           string
	repoOwner     string
	controllerURL string
}

// setupEnvVars validates and retrieves environment variables based on the test scenario.
// Combination: (Public/GHE) x (App/Webhook).
func setupEnvVars(onGHE, viaDirectWebhook bool) (*envConfig, error) {
	var requiredEnvs []string
	config := &envConfig{}

	switch {
	case onGHE && viaDirectWebhook:
		requiredEnvs = append(requiredEnvs,
			"TEST_EL_WEBHOOK_SECRET",
			"TEST_GITHUB_SECOND_API_URL",
			"TEST_GITHUB_SECOND_EL_URL",
			"TEST_GITHUB_SECOND_WEBHOOK_TOKEN",
			"TEST_GITHUB_SECOND_WEBHOOK_SECRET",
		)
		if err := setup.RequireEnvs(requiredEnvs...); err != nil {
			return nil, err
		}
		config.url = os.Getenv("TEST_GITHUB_SECOND_API_URL")
		config.controllerURL = os.Getenv("TEST_GITHUB_SECOND_EL_URL")
		config.token = os.Getenv("TEST_GITHUB_SECOND_WEBHOOK_TOKEN")
		webhookOrg := os.Getenv("TEST_GITHUB_SECOND_WEBHOOK_ORG")
		if webhookOrg == "" {
			webhookOrg = os.Getenv("TEST_GITHUB_SECOND_REPO_OWNER_GITHUBAPP")
		}
		config.repoOwner = webhookOrg
	case onGHE && !viaDirectWebhook:
		requiredEnvs = append(requiredEnvs,
			"TEST_GITHUB_SECOND_API_URL",
			"TEST_GITHUB_SECOND_EL_URL",
			"TEST_GITHUB_SECOND_TOKEN",
			"TEST_GITHUB_SECOND_REPO_OWNER_GITHUBAPP",
			"TEST_GITHUB_SECOND_REPO_INSTALLATION_ID",
			"TEST_GITHUB_SECOND_WEBHOOK_SECRET",
		)
		if err := setup.RequireEnvs(requiredEnvs...); err != nil {
			return nil, err
		}
		config.url = os.Getenv("TEST_GITHUB_SECOND_API_URL")
		config.controllerURL = os.Getenv("TEST_GITHUB_SECOND_EL_URL")
		config.token = os.Getenv("TEST_GITHUB_SECOND_TOKEN")
		config.repoOwner = os.Getenv("TEST_GITHUB_SECOND_REPO_OWNER_GITHUBAPP")

	case !onGHE && viaDirectWebhook:
		requiredEnvs = append(requiredEnvs,
			"TEST_EL_URL",
			"TEST_EL_WEBHOOK_SECRET",
			"TEST_GITHUB_API_URL",
			"TEST_GITHUB_TOKEN",
			"TEST_GITHUB_REPO_OWNER_WEBHOOK",
		)
		if err := setup.RequireEnvs(requiredEnvs...); err != nil {
			return nil, err
		}
		config.url = os.Getenv("TEST_GITHUB_API_URL")
		config.controllerURL = os.Getenv("TEST_EL_URL")
		config.token = os.Getenv("TEST_GITHUB_TOKEN")
		config.repoOwner = os.Getenv("TEST_GITHUB_REPO_OWNER_WEBHOOK")

	case !onGHE && !viaDirectWebhook:
		requiredEnvs = append(requiredEnvs,
			"TEST_EL_URL",
			"TEST_GITHUB_API_URL",
			"TEST_GITHUB_REPO_OWNER_GITHUBAPP",
		)
		if err := setup.RequireEnvs(requiredEnvs...); err != nil {
			return nil, err
		}
		config.url = os.Getenv("TEST_GITHUB_API_URL")
		config.controllerURL = os.Getenv("TEST_EL_URL")
		config.repoOwner = os.Getenv("TEST_GITHUB_REPO_OWNER_GITHUBAPP")
		// token left empty - will be obtained from GitHub App
	}

	return config, nil
}

func setupGithubAppToken(ctx context.Context, config *envConfig, viaDirectWebhook bool, run *params.Run, gprovider *github.Provider) error {
	if config.token == "" && !viaDirectWebhook {
		var err error

		envGithubRepoInstallationID, err := setup.GetRequiredEnv("TEST_GITHUB_REPO_INSTALLATION_ID")
		if err != nil {
			return err
		}
		// convert to int64 githubRepoInstallationID
		githubRepoInstallationID, err := strconv.ParseInt(envGithubRepoInstallationID, 10, 64)
		if err != nil {
			return fmt.Errorf("TEST_GITHUB_REPO_INSTALLATION_ID env variable must be an integer but got '%s'", envGithubRepoInstallationID)
		}
		ns := info.GetNS(ctx)
		config.token, err = gprovider.GetAppToken(ctx, run.Clients.Kube, config.url, githubRepoInstallationID, ns)
		if err != nil {
			return err
		}
	}
	return nil
}
