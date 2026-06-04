package webhook

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/AlecAivazis/survey/v2"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/random"
)

type forgejoConfig struct {
	Client              *forgejo.Client
	IOStream            *cli.IOStreams
	controllerURL       string
	repoOwner           string
	repoName            string
	webhookSecret       string
	personalAccessToken string
	APIURL              string
}

func (fg *forgejoConfig) Run(_ context.Context, opts *Options) (*response, error) {
	err := fg.askForgejoWebhookConfig(opts.RepositoryURL, opts.ControllerURL, opts.ProviderAPIURL, opts.PersonalAccessToken)
	if err != nil {
		return nil, err
	}

	return &response{
		ControllerURL:       fg.controllerURL,
		PersonalAccessToken: fg.personalAccessToken,
		WebhookSecret:       fg.webhookSecret,
		APIURL:              fg.APIURL,
	}, fg.create()
}

func (fg *forgejoConfig) askForgejoWebhookConfig(repoURL, controllerURL, apiURL, personalAccessToken string) error {
	if repoURL == "" {
		msg := "Please enter the git repository url you want to be configured: "
		if err := prompt.SurveyAskOne(&survey.Input{Message: msg}, &repoURL,
			survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(fg.IOStream.Out, "✓ Setting up Forgejo Webhook for Repository %s\n", repoURL)
	}

	repoURLForOwner := strings.TrimSuffix(repoURL, "/")
	repoURLForOwner = strings.TrimSuffix(repoURLForOwner, ".git")
	defaultRepo, err := formatting.GetRepoOwnerFromURL(repoURLForOwner)
	if err != nil {
		return err
	}

	repoArr := strings.Split(defaultRepo, "/")
	if len(repoArr) != 2 {
		return fmt.Errorf("invalid repository, needs to be of format 'org-name/repo-name'")
	}
	fg.repoOwner = repoArr[0]
	fg.repoName = repoArr[1]

	fg.controllerURL = controllerURL
	if fg.controllerURL != "" {
		var answer bool
		fmt.Fprintf(fg.IOStream.Out, "👀 I have detected a controller url: %s\n", fg.controllerURL)
		err := prompt.SurveyAskOne(&survey.Confirm{
			Message: "Do you want me to use it?",
			Default: true,
		}, &answer)
		if err != nil {
			return err
		}
		if !answer {
			fg.controllerURL = ""
		}
	}

	if fg.controllerURL == "" {
		if err := prompt.SurveyAskOne(&survey.Input{
			Message: "Please enter your controller public route URL: ",
		}, &fg.controllerURL, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	}

	data := random.AlphaString(12)
	msg := fmt.Sprintf("Please enter the secret to configure the webhook for payload validation (default: %s): ", data)
	if err := prompt.SurveyAskOne(&survey.Input{Message: msg, Default: data}, &fg.webhookSecret); err != nil {
		return err
	}

	if personalAccessToken == "" {
		fmt.Fprintln(fg.IOStream.Out, "ℹ ️You now need to create a Forgejo personal access token with repository access All, write:repository, and write:issue.")
		if err := prompt.SurveyAskOne(&survey.Password{
			Message: "Please enter the Forgejo access token: ",
		}, &fg.personalAccessToken, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		fg.personalAccessToken = personalAccessToken
	}

	if apiURL == "" {
		defaultURL, err := forgejoInstanceURL(repoURL)
		if err != nil {
			return err
		}
		if err := prompt.SurveyAskOne(&survey.Input{
			Message: "Please enter your Forgejo URL: ",
			Default: defaultURL,
		}, &fg.APIURL, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		fg.APIURL = apiURL
	}

	return nil
}

func (fg *forgejoConfig) create() error {
	fgClient, err := fg.newClient()
	if err != nil {
		return err
	}

	hook := forgejo.CreateHookOption{
		Type: forgejo.HookTypeForgejo,
		Config: map[string]string{
			"content_type": "json",
			"url":          fg.controllerURL,
			"secret":       fg.webhookSecret,
		},
		Events: []string{
			"push",
			"pull_request",
			"pull_request_sync",
			"pull_request_label",
			"issue_comment",
		},
		Active: true,
	}

	_, _, err = fgClient.CreateRepoHook(fg.repoOwner, fg.repoName, hook)
	if err != nil {
		return fmt.Errorf("failed to create Forgejo webhook: %w", err)
	}

	fmt.Fprintf(fg.IOStream.Out, "✓ Webhook has been created on repository %v/%v\n", fg.repoOwner, fg.repoName)
	return nil
}

func (fg *forgejoConfig) newClient() (*forgejo.Client, error) {
	if fg.Client != nil {
		return fg.Client, nil
	}
	return forgejo.NewClient(fg.APIURL, forgejo.SetToken(fg.personalAccessToken))
}

func forgejoInstanceURL(repoURL string) (string, error) {
	parsedURL, err := url.Parse(repoURL)
	if err != nil {
		return "", err
	}
	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		return "", fmt.Errorf("invalid forgejo repository URL: %s", repoURL)
	}
	return fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host), nil
}
