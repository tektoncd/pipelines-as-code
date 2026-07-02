package deleterepo

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/completion"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/spf13/cobra"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var namespaceFlag = "namespace"

const longHelp = `
Delete a Pipelines as Code Repository or multiple of them

eg:
	tkn pac delete repository <repository-name> <repository-name2>
	`

func repositoryCommand(run *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	var repository string
	var cascade bool
	cmd := &cobra.Command{
		Args:    cobra.MinimumNArgs(0),
		Use:     "repository",
		Short:   "Delete a Pipelines-as-Code repository or multiple repositories",
		Long:    longHelp,
		Aliases: []string{"repo"},
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completion.BaseCompletion("repositories", args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			opts := cli.NewCliOptions()
			opts.Namespace, err = cmd.Flags().GetString(namespaceFlag)
			if err != nil {
				return err
			}
			ctx := context.Background()
			err = run.Clients.NewClients(ctx, &run.Info)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return fmt.Errorf("repository name is required")
			}
			if opts.Namespace == "" {
				opts.Namespace = run.Info.Kube.Namespace
			}
			return repodelete(ctx, run, args, opts, ioStreams, cascade)
		},
		Annotations: map[string]string{
			"commandType": "main",
		},
	}

	cmd.Flags().StringP(
		namespaceFlag, "n", "", "If present, the namespace scope for this CLI request",
	)
	_ = cmd.RegisterFlagCompletionFunc(
		namespaceFlag,
		func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completion.BaseCompletion(namespaceFlag, args)
		},
	)

	cmd.Flags().BoolVarP(
		&cascade, "cascade", "c", false, "Delete the repository and its secrets attached to it",
	)
	cmd.Flags().StringVar(&repository, "repository", "", "The name of the repository to delete")

	_ = cmd.RegisterFlagCompletionFunc(
		namespaceFlag,
		func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completion.BaseCompletion(namespaceFlag, args)
		},
	)
	return cmd
}

func repodelete(ctx context.Context, run *params.Run, names []string, opts *cli.PacCliOpts, ioStreams *cli.IOStreams, cascade bool) error {
	for _, name := range names {
		if cascade {
			// get repo spec
			repo, err := run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(opts.Namespace).Get(ctx, name, v1.GetOptions{})
			if err != nil {
				return err
			}
			if repo.Spec.GitProvider != nil {
				if repo.Spec.GitProvider.Secret != nil {
					err = run.Clients.Kube.CoreV1().Secrets(opts.Namespace).Delete(ctx, repo.Spec.GitProvider.Secret.Name, v1.DeleteOptions{})
					if err != nil {
						fmt.Fprintf(ioStreams.ErrOut, "skipping deleting api secret %s\n", repo.Spec.GitProvider.Secret.Name)
					} else {
						fmt.Fprintf(ioStreams.Out, "secret %s has been deleted\n", repo.Spec.GitProvider.Secret.Name)
					}
				}
				if repo.Spec.GitProvider.WebhookSecret != nil {
					err = run.Clients.Kube.CoreV1().Secrets(opts.Namespace).Delete(ctx, repo.Spec.GitProvider.WebhookSecret.Name, v1.DeleteOptions{})
					if err != nil {
						fmt.Fprintf(ioStreams.ErrOut, "skipping deleting webhook secret %s\n", repo.Spec.GitProvider.WebhookSecret.Name)
					} else {
						fmt.Fprintf(ioStreams.Out, "secret %s has been deleted\n", repo.Spec.GitProvider.WebhookSecret.Name)
					}
				}
			}
		}

		err := run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(opts.Namespace).Delete(ctx, name, v1.DeleteOptions{})
		if err != nil {
			return err
		}
		fmt.Fprintf(ioStreams.Out, "repository %s has been deleted\n", name)
	}
	return nil
}
