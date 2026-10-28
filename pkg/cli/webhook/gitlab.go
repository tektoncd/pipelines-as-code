package webhook

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/AlecAivazis/survey/v2"
	"github.com/tektoncd/pipelines-as-code/pkg/cli"
	"github.com/tektoncd/pipelines-as-code/pkg/cli/prompt"
	"github.com/tektoncd/pipelines-as-code/pkg/random"
	gitlab "gitlab.com/gitlab-org/api/client-go"
)

type gitLabConfig struct {
	Client              *gitlab.Client
	IOStream            *cli.IOStreams
	controllerURL       string
	projectID           string
	webhookSecret       string
	personalAccessToken string
	APIURL              string
}

func (gl *gitLabConfig) Run(_ context.Context, opts *Options) (*response, error) {
	err := gl.askGLWebhookConfig(opts.RepositoryURL, opts.ControllerURL, opts.ProviderAPIURL, opts.PersonalAccessToken)
	if err != nil {
		return nil, err
	}

	return &response{
		ControllerURL:       gl.controllerURL,
		PersonalAccessToken: gl.personalAccessToken,
		WebhookSecret:       gl.webhookSecret,
		APIURL:              gl.APIURL,
	}, gl.create()
}

// Changes in askGLWebhookConfig function.
func (gl *gitLabConfig) askGLWebhookConfig(repoURL, controllerURL, apiURL, personalAccessToken string) error {
	if repoURL == "" {
		msg := "Enter the GitLab repository URL to configure: "
		if err := prompt.SurveyAskOne(&survey.Input{Message: msg}, &repoURL,
			survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(gl.IOStream.Out, "✓ Setting up GitLab Webhook for Repository %s\n", repoURL)
	}

	msg := "Enter the project ID of your GitLab repository.\nThe project ID is a unique number (e.g. 34405323) shown at the top of your GitLab project page: "
	if err := prompt.SurveyAskOne(&survey.Input{Message: msg}, &gl.projectID,
		survey.WithValidator(survey.Required)); err != nil {
		return err
	}

	gl.controllerURL = controllerURL

	if gl.controllerURL != "" {
		var answer bool
		fmt.Fprintf(gl.IOStream.Out, "👀 Controller URL detected: %s\n", gl.controllerURL)
		err := prompt.SurveyAskOne(&survey.Confirm{
			Message: "Do you want me to use it?",
			Default: true,
		}, &answer)
		if err != nil {
			return err
		}
		if !answer {
			gl.controllerURL = ""
		}
	}

	if gl.controllerURL == "" {
		if err := prompt.SurveyAskOne(&survey.Input{
			Message: "Enter your controller's public route URL: ",
		}, &gl.controllerURL, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	}

	data := random.AlphaString(12)
	msg = fmt.Sprintf("Enter a secret for webhook payload validation (default: %s): ", data)
	var webhookSecret string
	if err := prompt.SurveyAskOne(&survey.Input{Message: msg, Default: data}, &webhookSecret); err != nil {
		return err
	}

	gl.webhookSecret = webhookSecret

	if personalAccessToken == "" {
		fmt.Fprintln(gl.IOStream.Out, "ℹ ️You need to create a GitLab personal access token with 'api' scope")
		fmt.Fprintln(gl.IOStream.Out, "ℹ ️Generate one at https://gitlab.com/-/profile/personal_access_tokens (see documentation: https://is.gd/rOEo9B)")
		if err := prompt.SurveyAskOne(&survey.Password{
			Message: "Enter your GitLab access token: ",
		}, &gl.personalAccessToken, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		gl.personalAccessToken = personalAccessToken
	}

	if apiURL == "" {
		if err := prompt.SurveyAskOne(&survey.Input{
			Message: "Enter your GitLab API URL: ",
		}, &gl.APIURL, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	} else {
		gl.APIURL = apiURL
	}

	return nil
}

// create function.
func (gl *gitLabConfig) create() error {
	glClient, err := gl.newClient()
	if err != nil {
		return err
	}

	hookOpts := &gitlab.AddProjectHookOptions{
		EnableSSLVerification: gitlab.Ptr(true),
		MergeRequestsEvents:   gitlab.Ptr(true),
		NoteEvents:            gitlab.Ptr(true),
		PushEvents:            gitlab.Ptr(true),
		TagPushEvents:         gitlab.Ptr(true),
		Token:                 gitlab.Ptr(gl.webhookSecret),
		URL:                   gitlab.Ptr(gl.controllerURL),
	}

	_, resp, err := glClient.Projects.AddProjectHook(gl.projectID, hookOpts)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusCreated {
		payload, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("failed to read response body: %w", err)
		}
		return fmt.Errorf("failed to create webhook, status code: %v, error : %v",
			resp.StatusCode, payload)
	}

	fmt.Fprintln(gl.IOStream.Out, "✓ Webhook successfully created on your repository")
	return nil
}

func (gl *gitLabConfig) newClient() (*gitlab.Client, error) {
	if gl.Client != nil {
		return gl.Client, nil
	}

	if gl.APIURL == "" {
		return gitlab.NewClient(gl.personalAccessToken)
	}
	return gitlab.NewClient(gl.personalAccessToken, gitlab.WithBaseURL(gl.APIURL))
}
