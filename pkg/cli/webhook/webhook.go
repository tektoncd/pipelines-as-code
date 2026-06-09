package webhook

import (
	"context"
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/bootstrap"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
)

type Interface interface {
	Run(context.Context, *Options) (*response, error)
}

type Options struct {
	Run                      *params.Run
	IOStreams                *cli.IOStreams
	PACNamespace             string
	RepositoryURL            string
	RepositoryName           string
	RepositoryNamespace      string
	ProviderAPIURL           string
	ControllerURL            string
	PersonalAccessToken      string
	RepositoryCreateORUpdate bool
	SecretName               string
	ProviderSecretKey        string
}

type response struct {
	UserName            string
	ControllerURL       string
	WebhookSecret       string
	PersonalAccessToken string
	APIURL              string
}

func (w *Options) Install(ctx context.Context, providerType string) error {
	// figure out pac installation namespace
	installed, installationNS, err := bootstrap.DetectPacInstallation(ctx, w.PACNamespace, w.Run)
	if !installed {
		return fmt.Errorf("pipelines as code not installed")
	}
	if installed && err != nil {
		return err
	}

	// fetch configmap to get controller url
	pacInfo, err := info.GetPACInfo(ctx, w.Run, installationNS)
	if err != nil {
		return err
	}

	// check if info configmap has url then use that otherwise try to detect
	if pacInfo.ControllerURL != "" && w.ControllerURL == "" {
		w.ControllerURL = pacInfo.ControllerURL
	} else {
		w.ControllerURL, _ = bootstrap.DetectOpenShiftRoute(ctx, w.Run, w.PACNamespace)
	}

	if w.RepositoryURL == "" {
		q := "Please enter the Git repository url: "
		if err := prompt.SurveyAskOne(&survey.Input{Message: q}, &w.RepositoryURL,
			survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	}

	var webhookProvider Interface
	switch providerType {
	case "github":
		webhookProvider = &gitHubConfig{IOStream: w.IOStreams}
	case "gitlab":
		webhookProvider = &gitLabConfig{IOStream: w.IOStreams}
	case "bitbucket-cloud":
		webhookProvider = &bitbucketCloudConfig{IOStream: w.IOStreams}
	case "forgejo":
		webhookProvider = &forgejoConfig{IOStream: w.IOStreams}
	default:
		return fmt.Errorf("invalid webhook provider")
	}

	response, err := webhookProvider.Run(ctx, w)
	if err != nil {
		return err
	}

	// RepositoryCreateORUpdate is false for tkn-pac webhook add command
	if !w.RepositoryCreateORUpdate {
		return w.updateWebhookSecret(ctx, response)
	}

	// create webhook secret in namespace where repository CR is created
	if err := w.createWebhookSecret(ctx, response); err != nil {
		return err
	}

	// update repo cr with webhook secret
	return w.updateRepositoryCR(ctx, response)
}

func GetProviderName(url string) (string, error) {
	var (
		err          error
		providerName string
	)
	switch {
	case strings.Contains(url, "github"):
		providerName = "github"
	case strings.Contains(url, "gitlab"):
		providerName = "gitlab"
	case strings.Contains(url, "bitbucket-cloud"):
		providerName = "bitbucket-cloud"
	case strings.Contains(url, "forgejo"):
		providerName = "forgejo"
	default:
		msg := "Please select the type of the git platform to setup webhook:"
		if err = prompt.SurveyAskOne(
			&survey.Select{
				Message: msg,
				Options: []string{"github", "gitlab", "bitbucket-cloud", "forgejo"},
				Default: 0,
			}, &providerName); err != nil {
			return "", err
		}
	}
	return providerName, nil
}
