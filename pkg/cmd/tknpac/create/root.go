package create

import (
	"github.com/spf13/cobra"
	"github.com/tektoncd/pipelines-as-code/pkg/cli"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
)

func Root(clients *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "create",
		Aliases:      []string{},
		Short:        "Create Pipelines-as-Code resources",
		Long:         `Create Pipelines-as-Code resources such as repositories, webhook configurations, and other related components. This command helps you set up and configure Pipelines-as-Code in your cluster.`,
		SilenceUsage: true,
		Annotations: map[string]string{
			"commandType": "main",
		},
	}

	cmd.AddCommand(repositoryCommand(clients, ioStreams))
	return cmd
}
