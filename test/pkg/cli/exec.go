package cli

import (
	"bytes"

	"github.com/spf13/cobra"
	"github.com/tektoncd/pipelines-as-code/pkg/cli"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	cli2 "github.com/tektoncd/pipelines-as-code/pkg/test/cli"
)

// ExecCommand setup a cobra command for running with params run and
// custom iostream.
func ExecCommand(runcnx *params.Run, cmd func(*params.Run, *cli.IOStreams) *cobra.Command, args ...string) (string, error) {
	bufout := new(bytes.Buffer)
	ecmd := cmd(runcnx, &cli.IOStreams{
		Out: bufout,
	})
	_, err := cli2.ExecuteCommand(ecmd, args...)
	return bufout.String(), err
}

// ExecCommandNoRun is a wrapper around ExecuteCommand that does not run the
// command with the params.Run clients.
func ExecCommandNoRun(cmd func(*cli.IOStreams) *cobra.Command, args ...string) (string, error) {
	bufout := new(bytes.Buffer)
	ecmd := cmd(&cli.IOStreams{
		Out: bufout,
	})
	_, err := cli2.ExecuteCommand(ecmd, args...)
	return bufout.String(), err
}
