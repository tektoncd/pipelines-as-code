package webhook

import (
	"context"
	"fmt"

	"github.com/AlecAivazis/survey/v2"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/completion"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func webhookUpdateToken(run *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "update-token",
		Aliases: []string{""},
		Short:   "Update webhook provider token",
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				err      error
				repoName string
			)
			opts := cli.NewCliOptions()

			opts.Namespace, err = cmd.Flags().GetString(namespaceFlag)
			if err != nil {
				return err
			}

			if len(args) > 0 {
				repoName = args[0]
			}

			ctx := cmd.Context()
			if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
				return err
			}
			return update(ctx, opts, run, ioStreams, repoName)
		},
		Annotations: map[string]string{
			"commandType": "main",
		},
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completion.BaseCompletion("repositories", args)
		},
	}

	cmd.Flags().StringP(
		namespaceFlag, "n", "", "If present, the namespace scope for this CLI request")

	_ = cmd.RegisterFlagCompletionFunc(namespaceFlag,
		func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completion.BaseCompletion(namespaceFlag, args)
		},
	)
	return cmd
}

func update(ctx context.Context, opts *cli.PacCliOpts, run *params.Run, ioStreams *cli.IOStreams, repoName string) error {
	var (
		err                 error
		repo                *v1alpha1.Repository
		personalAccessToken string
	)
	if opts.Namespace != "" {
		run.Info.Kube.Namespace = opts.Namespace
	}
	if repoName != "" {
		repo, err = run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(run.Info.Kube.Namespace).Get(ctx,
			repoName, metav1.GetOptions{})
		if err != nil {
			return err
		}
	} else {
		repo, err = prompt.SelectRepo(ctx, run, run.Info.Kube.Namespace)
		if err != nil {
			return err
		}
	}

	// Should not proceed when GithubApp is configured or GitProvider is nil
	if repo.Spec.GitProvider == nil {
		fmt.Fprintf(ioStreams.Out, "%s Webhook is not configured for the repository %s ",
			ioStreams.ColorScheme().InfoIcon(),
			repoName)
		return nil
	}

	if repo.Spec.GitProvider.Secret == nil {
		fmt.Fprintf(ioStreams.Out, "%s Can not update provider token when git_provider secret is empty",
			ioStreams.ColorScheme().WarningIcon())
		return nil
	}

	secretName := repo.Spec.GitProvider.Secret.Name
	secretData, err := run.Clients.Kube.CoreV1().Secrets(repo.Namespace).Get(ctx, repo.Spec.GitProvider.Secret.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if err := prompt.SurveyAskOne(&survey.Password{
		Message: "Please enter your personal access token: ",
	}, &personalAccessToken, survey.WithValidator(survey.Required)); err != nil {
		return err
	}

	gitProviderSecretKey := repo.Spec.GitProvider.Secret.Key
	if gitProviderSecretKey == "" {
		gitProviderSecretKey = secrets.DefaultGitProviderSecretKey
	}

	secretData.Data[gitProviderSecretKey] = []byte(personalAccessToken)
	_, err = run.Clients.Kube.CoreV1().Secrets(repo.Namespace).Update(ctx, secretData, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	fmt.Fprintf(ioStreams.Out, "🔑 Secret %s has been updated with new personal access token in the %s namespace.\n", secretName, repo.Namespace)

	return nil
}
