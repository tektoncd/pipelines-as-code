package describe

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"text/tabwriter"
	"text/template"

	"github.com/jonboulle/clockwork"
	"github.com/juju/ansiterm"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/completion"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	namespaceFlag     = "namespace"
	targetPRFlag      = "target-pipelinerun"
	useRealTimeFlag   = "use-realtime"
	showEventflag     = "show-events"
	creationTimestamp = "{.metadata.creationTimestamp}"
	maxEventLimit     = 50
)

//go:embed templates/describe.tmpl
var describeTemplate string

func formatError(cs *cli.ColorScheme, log string) string {
	n := status.ErorrRE.ReplaceAllString(log, cs.RedBold("$0"))
	// add two space to every characters at beginning of line in string
	n = regexp.MustCompile(`(?m)^`).ReplaceAllString(n, "  ")
	return n
}

func formatRunStatus(rs status.RunStatus, cs *cli.ColorScheme, c clockwork.Clock) string {
	return fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s",
		cs.ColorStatus(rs.Reason),
		rs.EventType,
		formatting.SanitizeBranch(rs.TargetBranch),
		cs.HyperLink(formatting.ShortSHA(rs.SHA), rs.SHAURL),
		formatting.Age(rs.StartTime, c),
		formatting.PRDuration(rs.StartTime, rs.CompletionTime),
		cs.HyperLink(rs.PipelineRunName, rs.LogURL))
}

type describeOpts struct {
	cli.PacCliOpts
	TargetPipelineRun string
	ShowEvents        bool
}

func newDescribeOptions(_ *cobra.Command) *describeOpts {
	return &describeOpts{
		PacCliOpts: *cli.NewCliOptions(),
	}
}

func Root(run *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	var useRealTime bool
	cmd := &cobra.Command{
		Use:     "describe",
		Aliases: []string{"desc"},
		Short:   "Describe a repository",
		Annotations: map[string]string{
			"commandType": "main",
		},
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completion.BaseCompletion("repositories", args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			var repoName string
			opts := newDescribeOptions(cmd)

			opts.UseRealTime, err = cmd.Flags().GetBool(useRealTimeFlag)
			if err != nil {
				return err
			}

			opts.Namespace, err = cmd.Flags().GetString(namespaceFlag)
			if err != nil {
				return err
			}

			opts.ShowEvents, err = cmd.Flags().GetBool(showEventflag)
			if err != nil {
				return err
			}

			opts.TargetPipelineRun, err = cmd.Flags().GetString(targetPRFlag)
			if err != nil {
				return err
			}

			if len(args) > 0 {
				repoName = args[0]
			}

			ctx := context.Background()
			clock := clockwork.NewRealClock()
			if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
				return err
			}

			// The only way to know the tekton dashboard url is if the user specify it because we are not supposed to have access to the configmap.
			// so let the user specify a env variable to implicitly set tekton dashboard
			if os.Getenv("TEKTON_DASHBOARD_URL") != "" {
				run.Clients.SetConsoleUI(&consoleui.TektonDashboard{BaseURL: os.Getenv("TEKTON_DASHBOARD_URL")})
			}

			return describe(ctx, run, clock, opts, ioStreams, repoName)
		},
	}

	cmd.Flags().StringP(
		targetPRFlag, "t", "", "Show this PipelineRun information",
	)
	_ = cmd.RegisterFlagCompletionFunc(
		targetPRFlag,
		func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completion.BaseCompletion("pipelinerun", args)
		},
	)

	cmd.Flags().StringP(
		namespaceFlag, "n", "", "If present, the namespace scope for this CLI request",
	)
	_ = cmd.RegisterFlagCompletionFunc(
		namespaceFlag,
		func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completion.BaseCompletion(namespaceFlag, args)
		},
	)

	cmd.Flags().BoolP(
		showEventflag, "", false, "show kubernetes events associated with this repository, useful if you have an error that cannot be reported on the git provider interface",
	)
	cmd.PersistentFlags().BoolVarP(&useRealTime, useRealTimeFlag, "", false,
		"display the time as RFC3339 instead of a relative time")
	return cmd
}

func filterOnlyToPipelineRun(opts *describeOpts, statuses []status.RunStatus) []status.RunStatus {
	var ret []status.RunStatus
	for _, rs := range statuses {
		if rs.PipelineRunName == opts.TargetPipelineRun {
			ret = append(ret, rs)
		}
	}
	return ret
}

func describe(ctx context.Context, cs *params.Run, clock clockwork.Clock, opts *describeOpts, ioStreams *cli.IOStreams, repoName string) error {
	var repository *v1alpha1.Repository
	var err error

	if opts.Namespace != "" {
		cs.Info.Kube.Namespace = opts.Namespace
	}

	if repoName != "" {
		repository, err = cs.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(cs.Info.Kube.Namespace).Get(ctx,
			repoName, metav1.GetOptions{})
		if err != nil {
			return err
		}
	} else {
		repository, err = prompt.SelectRepo(ctx, cs, cs.Info.Kube.Namespace)
		if err != nil {
			return err
		}
	}
	eventList := []corev1.Event{}
	if opts.ShowEvents {
		kinteract, err := kubeinteraction.NewKubernetesInteraction(cs)
		if err != nil {
			return err
		}
		events, err := kinteract.GetEvents(ctx, repository.GetNamespace(), "Repository", repository.GetName())
		if err != nil {
			fmt.Fprintf(ioStreams.ErrOut, "warning: could not fetch repository events: %v\n", err)
			events = &corev1.EventList{}
		}

		// events to runtime obj
		runTimeObj := []runtime.Object{}
		for i := range events.Items {
			runTimeObj = append(runTimeObj, &events.Items[i])
		}

		// we do twice the prun list, but since it's behind a flag and not the default behavior, it's ok (I guess)
		label := keys.Repository + "=" + repository.Name
		prs, err := cs.Clients.Tekton.TektonV1().PipelineRuns(repository.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: label,
		})
		if err != nil {
			return err
		}
		for _, pr := range prs.Items {
			prevents, err := kinteract.GetEvents(ctx, repository.GetNamespace(), "PipelineRun", pr.GetName())
			if err != nil {
				continue
			}
			for i := range prevents.Items {
				runTimeObj = append(runTimeObj, &prevents.Items[i])
			}
		}
		sort.ByField(creationTimestamp, runTimeObj)

		// append event in reverse order as they are sorted
		for i := len(runTimeObj) - 1; i >= 0; i-- {
			event, _ := runTimeObj[i].(*corev1.Event)
			eventList = append(eventList, *event)
		}
		// if there are more events than the max limit, take only the latest
		// equal to max limit
		if len(eventList) > maxEventLimit {
			eventList = eventList[:maxEventLimit-1]
		}
	}

	colorScheme := ioStreams.ColorScheme()
	funcMap := template.FuncMap{
		"formatError":     formatError,
		"formatStatus":    formatRunStatus,
		"formatEventType": formatting.CamelCasit,
		"formatDuration":  formatting.PRDuration,
		"formatTime":      formatting.Age,
		"sanitizeBranch":  formatting.SanitizeBranch,
		"shortSHA":        formatting.ShortSHA,
	}

	statuses := status.GetRunStatus(ctx, cs, *repository)

	if opts.TargetPipelineRun != "" {
		statuses = filterOnlyToPipelineRun(opts, statuses)
		if len(statuses) == 0 {
			return fmt.Errorf("cannot find target pipelinerun %s", opts.TargetPipelineRun)
		}
	}

	data := struct {
		Repository  *v1alpha1.Repository
		Statuses    []status.RunStatus
		ColorScheme *cli.ColorScheme
		Clock       clockwork.Clock
		Opts        *describeOpts
		EventList   []corev1.Event
	}{
		Repository:  repository,
		Statuses:    statuses,
		ColorScheme: colorScheme,
		Clock:       clock,
		EventList:   eventList,
		Opts:        opts,
	}
	w := ansiterm.NewTabWriter(ioStreams.Out, 0, 5, 3, ' ', tabwriter.TabIndent)
	t := template.Must(template.New("Describe Repository").Funcs(funcMap).Parse(describeTemplate))

	if err := t.Execute(w, data); err != nil {
		return err
	}

	return w.Flush()
}
