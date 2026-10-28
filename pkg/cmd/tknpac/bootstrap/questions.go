package bootstrap

import (
	"fmt"
	"io"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/tektoncd/pipelines-as-code/pkg/cli/prompt"
)

const defaultPublicGithub = "https://github.com"

func askYN(deflt bool, title, question string, writer io.Writer) (bool, error) {
	var answer bool
	if title != "" {
		fmt.Fprintf(writer, "%s\n", title)
	}
	err := prompt.SurveyAskOne(&survey.Confirm{
		Message: question,
		Default: deflt,
	}, &answer)
	return answer, err
}

// askQuestions ask questions to the user for the name and url of the app.
func askQuestions(opts *bootstrapOpts) error {
	var qs []*survey.Question

	if opts.GithubAPIURL == "" {
		if opts.providerType == "github-enterprise-app" {
			prompt := "Enter your GitHub enterprise API URL: "
			qs = append(qs, &survey.Question{
				Name: "GithubAPIURL",
				Prompt: &survey.Input{
					Message: prompt,
				},
				Validate: survey.Required,
			})
		} else {
			opts.GithubAPIURL = defaultPublicGithub
		}
	}

	msg := "Enter the name of your GitHub application: "
	if opts.GithubApplicationName == "" {
		qs = append(qs, &survey.Question{
			Name:     "GithubApplicationName",
			Prompt:   &survey.Input{Message: msg},
			Validate: survey.Required,
		})
	}

	err := prompt.SurveyAsk(qs, opts)
	if err != nil {
		return err
	}

	if !strings.HasPrefix(opts.GithubAPIURL, "https") {
		opts.GithubAPIURL = "https://" + strings.Trim(opts.GithubAPIURL, "/")
	}

	if opts.autoDetectedRoute && opts.RouteName != "" {
		answer, err := askYN(true,
			fmt.Sprintf("👀 I have detected an OpenShift Route on: %s", opts.RouteName),
			"Do you want me to use it?", opts.ioStreams.Out)
		if err != nil {
			return err
		}
		if !answer {
			opts.RouteName = ""
		}
	}

	if opts.RouteName == "" {
		if err := prompt.SurveyAskOne(&survey.Input{
			Message: "Enter your public route URL: ",
		}, &opts.RouteName, survey.WithValidator(survey.Required)); err != nil {
			return err
		}
	}

	if opts.GithubApplicationURL == "" {
		opts.GithubApplicationURL = opts.RouteName
	}

	return nil
}
