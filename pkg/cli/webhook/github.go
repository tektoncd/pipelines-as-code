package webhook

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/random"
	"golang.org/x/oauth2"
)

type gitHubConfig struct {
	Client              *github.Client
	IOStream            *cli.IOStreams
	controllerURL       string
	repoOwner           string
	repoName            string
	webhookSecret       string
	personalAccessToken string
	APIURL              string
}

func (gh *gitHubConfig) Run(ctx context.Context, opts *Options) (*response, error) {
	err := gh.askGHWebhookConfig(opts.RepositoryURL, opts.ControllerURL, opts.ProviderAPIURL, opts.PersonalAccessToken)
	if err != nil {
		return nil, err
	}

	return &response{
		ControllerURL:       gh.controllerURL,
		PersonalAccessToken: gh.personalAccessToken,
		WebhookSecret:       gh.webhookSecret,
		APIURL:              gh.APIURL,
	}, gh.create(ctx)
}

func (gh *gitHubConfig) askGHWebhookConfig(repoURL, controllerURL, apiURL, personalAccessToken string) error {
	if repoURL == "" {
		msg := "Please enter the git repository url you want to be configured: "
		if err := prompt.SurveyAskOne(&survey.Input{Message: msg}, &repoURL,
			survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(gh.IOStream.Out, "✓ Setting up GitHub Webhook for Repository %s\n", repoURL)
	}

	defaultRepo, err := formatting.GetRepoOwnerFromURL(repoURL)
	if err != nil {
		return err
	}

	defaultRepo = strings.TrimSuffix(defaultRepo, "/")
	repoArr := strings.Split(defaultRepo, "/")
	if len(repoArr) != 2 {
		return fmt.Errorf("invalid repository, needs to be of format 'org-name/repo-name'")
	}

	gh.repoOwner = repoArr[0]
	gh.repoName = repoArr[1]

	// set controller url
	gh.controllerURL = controllerURL

	// confirm whether to use the detected url
	if gh.controllerURL != "" {
		var answer bool
		fmt.Fprintf(gh.IOStream.Out, "👀 I have detected a controller url: %s\n", gh.controllerURL)
		err := prompt.SurveyAskOne(&survey.Confirm{
			Message: "Do you want me to use it?",
			Default: true,
		}, &answer)
		if err != nil {
			return err
		}
		if !answer {
			gh.controllerURL = ""
		}
	}

	if gh.controllerURL == "" {
		if err := prompt.SurveyAskOne(&survey.Input{
			Message: "Please enter your controller public route URL: ",
		}, &gh.controllerURL, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	}

	data := random.AlphaString(12)
	msg := fmt.Sprintf("Please enter the secret to configure the webhook for payload validation (default: %s): ", data)
	var webhookSecret string
	if err := prompt.SurveyAskOne(&survey.Input{Message: msg, Default: data}, &webhookSecret); err != nil {
		return err
	}

	gh.webhookSecret = webhookSecret

	if personalAccessToken == "" {
		fmt.Fprintln(gh.IOStream.Out, "ℹ ️You now need to create a GitHub personal access token, please checkout the docs at https://is.gd/KJ1dDH for the required scopes")
		if err := prompt.SurveyAskOne(&survey.Password{
			Message: "Please enter the GitHub access token: ",
		}, &gh.personalAccessToken, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		gh.personalAccessToken = personalAccessToken
	}

	if apiURL == "" && !strings.HasPrefix(repoURL, "https://github.com") {
		if err := prompt.SurveyAskOne(&survey.Input{
			Message: "Please enter your GitHub enterprise API URL: ",
		}, &gh.APIURL, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		gh.APIURL = apiURL
	}

	return nil
}

func (gh *gitHubConfig) create(ctx context.Context) error {
	hook := &github.Hook{
		Name:   github.Ptr("web"),
		Active: github.Ptr(true),
		Events: []string{
			"issue_comment",
			triggertype.PullRequest.String(),
			"push",
		},
		Config: &github.HookConfig{
			URL:         github.Ptr(gh.controllerURL),
			ContentType: github.Ptr("json"),
			InsecureSSL: github.Ptr("0"),
			Secret:      github.Ptr(gh.webhookSecret),
		},
	}

	ghClient, err := gh.newGHClientByToken(ctx)
	if err != nil {
		return err
	}

	_, res, err := ghClient.Repositories.CreateHook(ctx, gh.repoOwner, gh.repoName, hook)
	if err != nil {
		return err
	}

	if res.StatusCode != http.StatusCreated {
		payload, err := io.ReadAll(res.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}

		return fmt.Errorf("failed to create webhook on repository %v/%v, status code: %v, error : %v",
			gh.repoOwner, gh.repoName, res.StatusCode, payload)
	}

	fmt.Fprintf(gh.IOStream.Out, "✓ Webhook has been created on repository %v/%v\n", gh.repoOwner, gh.repoName)
	return nil
}

func (gh *gitHubConfig) newGHClientByToken(ctx context.Context) (*github.Client, error) {
	if gh.Client != nil {
		return gh.Client, nil
	}
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: gh.personalAccessToken},
	)

	if gh.APIURL == "" || gh.APIURL == keys.PublicGithubAPIURL {
		return github.NewClient(oauth2.NewClient(ctx, ts)), nil
	}

	gprovider, err := github.NewClient(oauth2.NewClient(ctx, ts)).WithEnterpriseURLs(gh.APIURL, gh.APIURL)
	if err != nil {
		return nil, err
	}
	return gprovider, nil
}
