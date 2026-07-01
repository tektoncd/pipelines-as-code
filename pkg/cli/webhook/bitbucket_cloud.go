package webhook

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/ktrysmt/go-bitbucket"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud"
)

type bitbucketCloudConfig struct {
	Client        *bitbucket.Client
	IOStream      *cli.IOStreams
	controllerURL string
	repoOwner     string
	repoName      string
	apiToken      string
	accountEmail  string
	APIURL        string
}

func (bb *bitbucketCloudConfig) Run(_ context.Context, opts *Options) (*response, error) {
	err := bb.askBBWebhookConfig(opts.RepositoryURL, opts.ControllerURL, opts.ProviderAPIURL, opts.PersonalAccessToken)
	if err != nil {
		return nil, err
	}

	return &response{
		ControllerURL:       bb.controllerURL,
		PersonalAccessToken: bb.apiToken,
		WebhookSecret:       "",
		APIURL:              bb.APIURL,
		UserName:            bb.accountEmail,
	}, bb.create()
}

func (bb *bitbucketCloudConfig) askBBWebhookConfig(repositoryURL, controllerURL, apiURL, personalAccessToken string) error {
	if repositoryURL == "" {
		msg := "Please enter the git repository url you want to be configured: "
		if err := prompt.SurveyAskOne(&survey.Input{Message: msg}, &repositoryURL,
			survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(bb.IOStream.Out, "✓ Setting up Bitbucket Webhook for Repository %s\n", repositoryURL)
	}

	defaultRepo, err := formatting.GetRepoOwnerFromURL(repositoryURL)
	if err != nil {
		return err
	}

	repoArr := strings.Split(defaultRepo, "/")
	if len(repoArr) != 2 {
		return fmt.Errorf("invalid repository, needs to be of format 'org-name/repo-name'")
	}
	bb.repoOwner = repoArr[0]
	bb.repoName = repoArr[1]

	if err := prompt.SurveyAskOne(&survey.Input{
		Message: "Please enter your Bitbucket Cloud Atlassian account email: ",
	}, &bb.accountEmail, survey.WithValidator(survey.Required)); err != nil {
		return err
	}

	if personalAccessToken == "" {
		fmt.Fprintln(bb.IOStream.Out, "ℹ ️You now need to create a Bitbucket Cloud API token with scopes, please checkout the docs at https://support.atlassian.com/bitbucket-cloud/docs/create-an-api-token/ for the required permissions")
		if err := prompt.SurveyAskOne(&survey.Password{
			Message: "Please enter the Bitbucket Cloud API token: ",
		}, &bb.apiToken, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		bb.apiToken = personalAccessToken
	}

	bb.controllerURL = controllerURL

	// confirm whether to use the detected url
	if bb.controllerURL != "" {
		var answer bool
		fmt.Fprintf(bb.IOStream.Out, "👀 I have detected a controller url: %s\n", bb.controllerURL)
		err := prompt.SurveyAskOne(&survey.Confirm{
			Message: "Do you want me to use it?",
			Default: true,
		}, &answer)
		if err != nil {
			return err
		}
		if !answer {
			bb.controllerURL = ""
		}
	}

	if bb.controllerURL == "" {
		if err := prompt.SurveyAskOne(&survey.Input{
			Message: "Please enter your controller public route URL: ",
		}, &bb.controllerURL, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	}

	if apiURL == "" && !strings.HasPrefix(repositoryURL, "https://bitbucket.org") {
		if err := prompt.SurveyAskOne(&survey.Input{
			Message: "Please enter your Bitbucket enterprise API URL:: ",
		}, &bb.APIURL, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		bb.APIURL = apiURL
	}

	return nil
}

func (bb *bitbucketCloudConfig) create() error {
	if bb.Client == nil {
		if bb.accountEmail == "" {
			return fmt.Errorf("bitbucket cloud Atlassian account email is required")
		}
		if bb.apiToken == "" {
			return fmt.Errorf("bitbucket cloud API token is required")
		}
		var err error
		bb.Client, err = bitbucket.NewBasicAuth(bb.accountEmail, bb.apiToken)
		if err != nil {
			return err
		}
	}
	if bb.APIURL != "" {
		parsedURL, err := url.Parse(bb.APIURL)
		if err != nil {
			return err
		}
		bb.Client.SetApiBaseURL(*parsedURL)
	}

	opts := &bitbucket.WebhooksOptions{
		Owner:    bb.repoOwner,
		RepoSlug: bb.repoName,
		Url:      bb.controllerURL,
		Active:   true,
		Events:   bitbucketcloud.PullRequestAllEvents,
	}
	_, err := bb.Client.Repositories.Webhooks.Create(opts)
	if err != nil {
		return err
	}

	fmt.Fprintf(bb.IOStream.Out, "✓ Webhook has been created on repository %v/%v\n", bb.repoOwner, bb.repoName)
	return nil
}
