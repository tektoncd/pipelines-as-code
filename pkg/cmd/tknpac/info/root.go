package info

import (
	"github.com/spf13/cobra"
	"github.com/tektoncd/pipelines-as-code/pkg/cli"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
)

func Root(clients *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "info",
		Aliases:      []string{},
		Short:        "Show installation information",
		Long:         `Show status and information about your Pipelines as Code installation`,
		SilenceUsage: true,
		Annotations: map[string]string{
			"commandType": "main",
		},
	}

	cmd.AddCommand(installCommand(clients, ioStreams))
	cmd.AddCommand(globbingCommand(ioStreams))
	return cmd
}
