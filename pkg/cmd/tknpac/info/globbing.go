package info

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/gobwas/glob"
	"github.com/spf13/cobra"
	"github.com/tektoncd/pipelines-as-code/pkg/cli"
	"github.com/tektoncd/pipelines-as-code/pkg/cli/prompt"
)

var help = `
tkn pac info globbing [expression]

tkn pac info globbing allows you to test the globbing expressions as used in paac.

By default the command will test globbing expressions against your current
directory to test the pipelinesascode.tekton.dev/on-path-change annotation.
 
The input is a path on your local filesystem (typically a git repository).

For example this command:

tkn pac info globbing 'docs/***/*.md'

will match all markdown files in the docs directory and its subdirectories if
present in the current directory.

You can specify a different directory than the current one by using the -d/--dir
flag.

If you want to test against a string instead of a filesystem you can use the -s flag.
For example this will test if the globbing expression refs/heads/* matches refs/heads/main:

tkn pac info globbing -s "refs/heads/main" "refs/heads/*"
`

func globbingCommand(ioStreams *cli.IOStreams) *cobra.Command {
	var dir, str, expression string
	cmd := &cobra.Command{
		Use:   "globbing",
		Short: "Test globbing expression.",
		Long:  help,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				q := "Please enter an expression"
				if err := prompt.SurveyAskOne(&survey.Input{Message: q}, &expression, survey.WithValidator(survey.Required)); err != nil {
					return err
				}
			} else {
				expression = args[0]
			}
			if cmd.Flags().Changed("str") {
				g := glob.MustCompile(expression)
				if g.Match(str) {
					fmt.Fprintf(ioStreams.Out, "expression %s has matched the string %s\n", expression, str)
				} else {
					return fmt.Errorf("expression %s has not matched the string %s", expression, str)
				}
				return nil
			}
			if !cmd.Flags().Changed("dir") {
				cwd, err := os.Getwd()
				if err != nil {
					return err
				}
				dir = cwd
			} else {
				if _, err := os.Stat(dir); os.IsNotExist(err) {
					return fmt.Errorf("directory %s does not exist", dir)
				}
			}

			matched := false
			g := glob.MustCompile(expression)
			err := filepath.WalkDir(dir, func(path string, _ fs.DirEntry, _ error) error {
				// remove the dir prefix with a regexp
				p := strings.TrimPrefix(path, dir+"/")
				if g.Match(p) {
					matched = true
					fmt.Fprintf(ioStreams.Out, "%s\n", p)
				}
				return nil
			})
			if err == nil && !matched {
				return fmt.Errorf("no files matched the expression %s", expression)
			}
			return err
		},
		Annotations: map[string]string{
			"commandType": "main",
		},
	}
	// add params for enteprise github
	cmd.PersistentFlags().StringVarP(&dir, "dir", "d", "", "dir to start from")
	cmd.PersistentFlags().StringVarP(&str, "str", "s", "", "string to use for the pattern")
	return cmd
}
