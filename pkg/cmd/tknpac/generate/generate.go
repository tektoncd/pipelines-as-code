package generate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/git"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/spf13/cobra"
)

var eventTypes = map[string]string{triggertype.PullRequest.String(): "Pull Request", "push": "Push to a Branch or a Tag"}

const (
	gitCloneClusterTaskName = "git-clone"
	defaultEventType        = "Pull Request"
	mainBranch              = "main"
)

type Opts struct {
	Event   *info.Event
	GitInfo *git.Info

	IOStreams *cli.IOStreams
	CLIOpts   *cli.PacCliOpts

	pipelineRunName         string
	FileName                string
	overwrite               bool
	language                string
	generateWithClusterTask bool
}

func MakeOpts() *Opts {
	return &Opts{
		Event:   info.NewEvent(),
		GitInfo: &git.Info{},

		IOStreams: &cli.IOStreams{},
		CLIOpts:   &cli.PacCliOpts{},
	}
}

func Command(_ *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	gopt := MakeOpts()
	gopt.IOStreams = ioStreams
	cmd := &cobra.Command{
		Use:     "generate",
		Aliases: []string{"gen"},
		Short:   "Generate a PipelineRun",
		RunE: func(_ *cobra.Command, _ []string) error {
			gopt.CLIOpts = cli.NewCliOptions()
			gopt.IOStreams.SetColorEnabled(!gopt.CLIOpts.NoColoring)

			if gopt.generateWithClusterTask {
				return fmt.Errorf("ClusterTasks are deprecated and not available anymore")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			gopt.GitInfo = git.GetGitInfo(cwd)
			return Generate(gopt, true)
		},
		Annotations: map[string]string{
			"commandType": "main",
		},
	}
	cmd.PersistentFlags().StringVar(&gopt.Event.BaseBranch, "branch", "",
		"Target branch for the PipelineRun to handle (e.g., main, nightly)")
	cmd.PersistentFlags().StringVar(&gopt.Event.EventType, "event-type", "",
		"Event type of the repository event to handle (e.g., pull_request, push)")
	cmd.PersistentFlags().StringVar(&gopt.pipelineRunName, "pipeline-name", "",
		"Pipeline name")
	cmd.PersistentFlags().StringVarP(&gopt.FileName, "file-name", "f", "",
		"File name location")
	cmd.PersistentFlags().BoolVar(&gopt.overwrite, "overwrite", false,
		"Whether to overwrite the file if it exists")
	cmd.PersistentFlags().StringVarP(&gopt.language, "language", "l", "",
		"Generate template for this programming language")
	cmd.PersistentFlags().BoolVarP(&gopt.generateWithClusterTask, "use-clustertasks", "", false,
		"Deprecated, not available anymore")
	_ = cmd.PersistentFlags().MarkDeprecated("use-clustertasks", "This flag will be removed in a future release")
	return cmd
}

func Generate(o *Opts, recreateTemplate bool) error {
	if err := o.targetEvent(); err != nil {
		return err
	}

	if err := o.branchOrTag(); err != nil {
		return err
	}

	return o.samplePipeline(recreateTemplate)
}

func (o *Opts) targetEvent() error {
	var choice string
	if o.Event.EventType != "" {
		return nil
	}
	msg := "Enter the Git event type for triggering the pipeline: "

	eventLabels := make([]string, 0, len(eventTypes))
	for _, label := range eventTypes {
		eventLabels = append(eventLabels, label)
	}
	if err := prompt.SurveyAskOne(
		&survey.Select{
			Message: msg,
			Options: eventLabels,
			Default: 0,
		}, &choice,
	); err != nil {
		return err
	}

	if choice == "" {
		choice = defaultEventType
	}

	for k, v := range eventTypes {
		if v == choice {
			o.Event.EventType = k
			return nil
		}
	}

	return fmt.Errorf("invalid event type: %s", choice)
}

func (o *Opts) branchOrTag() error {
	var msg string
	choice := new(string)
	if o.Event.BaseBranch != "" {
		return nil
	}

	o.Event.BaseBranch = mainBranch

	if o.Event.EventType == triggertype.PullRequest.String() {
		msg = "Enter the target Git branch for the Pull Request (default: %s): "
	} else if o.Event.EventType == "push" {
		msg = "Enter a target Git branch or tag for the push (default: %s)"
	}

	if err := prompt.SurveyAskOne(
		&survey.Input{
			Message: fmt.Sprintf(msg, mainBranch),
		}, choice,
	); err != nil {
		return err
	}

	if *choice != "" {
		o.Event.BaseBranch = *choice
	}
	return nil
}

func generatefileName(eventType string) string {
	var filename string
	types := strings.Split(eventType, ",")
	if len(types) > 1 {
		filename = "pipelinerun"
	} else {
		filename = strings.ReplaceAll(eventType, "_", "-")
	}
	return fmt.Sprintf("%s.yaml", filename)
}

// samplePipeline will try to create a basic pipeline in tekton
// directory.
func (o *Opts) samplePipeline(recreateTemplate bool) error {
	cs := o.IOStreams.ColorScheme()
	var relpath, fpath, dirPath string

	if o.FileName != "" {
		fpath = o.FileName
		relpath = fpath
		dirPath = filepath.Dir(fpath)
	} else {
		fname := generatefileName(o.Event.EventType)
		fpath = filepath.Join(o.GitInfo.TopLevelPath, ".tekton", fname)
		relpath, _ = filepath.Rel(o.GitInfo.TopLevelPath, fpath)
		dirPath = filepath.Join(o.GitInfo.TopLevelPath, ".tekton")
	}

	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		if err := os.MkdirAll(dirPath, 0o750); err != nil {
			return err
		}
		fmt.Fprintf(
			o.IOStreams.Out, "%s Directory %s has been created.\n",
			cs.InfoIcon(),
			cs.Bold(dirPath),
		)
	}

	if _, err := os.Stat(fpath); !os.IsNotExist(err) && !o.overwrite {
		if recreateTemplate {
			var overwrite bool
			msg := fmt.Sprintf("A file named %s already exists. Would you like to override it?", relpath)
			if err := prompt.SurveyAskOne(&survey.Confirm{Message: msg, Default: false}, &overwrite); err != nil {
				return err
			}
			if !overwrite {
				fmt.Fprintf(o.IOStreams.ErrOut, "%s File not overwritten, exiting...\n", cs.WarningIcon())
				fmt.Fprintf(o.IOStreams.ErrOut, "%s Use the -f flag to specify a different file name.\n", cs.InfoIcon())
			}
		} else {
			fmt.Fprintf(o.IOStreams.Out, "%s File %s already exists, skipping template generation. Use \"%s pac generate\" to generate a sample template.\n", cs.InfoIcon(), relpath, settings.TknBinaryName)
		}
		return nil
	}
	tmpl, err := o.genTmpl()
	if err != nil {
		return err
	}

	err = os.WriteFile(fpath, tmpl.Bytes(), 0o600)
	if err != nil {
		return fmt.Errorf("cannot write template to %s: %w", fpath, err)
	}

	fmt.Fprintf(
		o.IOStreams.Out, "%s A basic template has been created in %s, feel free to customize it.\n",
		cs.SuccessIcon(),
		cs.Bold(fpath),
	)
	fmt.Fprintf(o.IOStreams.Out, "%s You can test your pipeline by pushing the generated template to your git repository\n", cs.InfoIcon())

	return nil
}
