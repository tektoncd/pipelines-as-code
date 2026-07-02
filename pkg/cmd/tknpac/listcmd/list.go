package listcmd

import (
	"context"
	_ "embed"
	"fmt"
	"text/tabwriter"
	"text/template"

	"github.com/jonboulle/clockwork"
	"github.com/juju/ansiterm"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/status"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/completion"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed template/list.tmpl
var lsTmpl string

var (
	allNamespacesFlag = "all-namespaces"
	namespaceFlag     = "namespace"
	useRealTimeFlag   = "use-realtime"
	noHeadersFlag     = "no-headers"
)

func Root(run *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	var noheaders, useRealTime, allNamespaces bool
	var selectors string

	cmd := &cobra.Command{
		Use:          "list",
		Aliases:      []string{"ls"},
		Short:        "List Pipelines-as-Code repositories",
		Long:         `List Pipelines-as-Code repositories`,
		SilenceUsage: true,
		Annotations: map[string]string{
			"commandType": "main",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			opts := cli.NewCliOptions()
			opts.AllNameSpaces, err = cmd.Flags().GetBool(allNamespacesFlag)
			if err != nil {
				return err
			}

			opts.UseRealTime, err = cmd.Flags().GetBool(useRealTimeFlag)
			if err != nil {
				return err
			}

			opts.NoHeaders, err = cmd.Flags().GetBool(noHeadersFlag)
			if err != nil {
				return err
			}

			opts.Namespace, err = cmd.Flags().GetString(namespaceFlag)
			if err != nil {
				return err
			}
			ctx := context.Background()
			err = run.Clients.NewClients(ctx, &run.Info)
			if err != nil {
				return err
			}
			cw := clockwork.NewRealClock()
			return list(ctx, run, opts, ioStreams, cw, selectors)
		},
	}

	cmd.PersistentFlags().BoolVarP(&allNamespaces, allNamespacesFlag, "A", false,
		"list the repositories across all namespaces.")

	cmd.PersistentFlags().BoolVarP(&useRealTime, useRealTimeFlag, "", false,
		"display the time as RFC3339 instead of a relative time")

	cmd.Flags().StringP(
		namespaceFlag, "n", "", "If present, the namespace scope for this CLI request",
	)

	_ = cmd.RegisterFlagCompletionFunc(
		namespaceFlag,
		func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completion.BaseCompletion(namespaceFlag, args)
		},
	)

	cmd.Flags().BoolVar(
		&noheaders, noHeadersFlag, false, "don't print headers.",
	)

	cmd.Flags().StringVarP(&selectors, "selectors", "l",
		"", "Selector (label query) to filter on, "+
			"supports '=', "+
			"'==',"+
			" and '!='.(e.g. -l key1=value1,key2=value2)")
	return cmd
}

func formatStatus(status *v1alpha1.RepositoryRunStatus, cs *cli.ColorScheme, c clockwork.Clock, ns string, opts *cli.PacCliOpts) string {
	// TODO: we could make a hyperlink to the console namespace list of repo if
	// we wanted to go the extra step
	if status == nil {
		s := fmt.Sprintf("%s\t%s\t%s\t", cs.Dimmed("---"), cs.Dimmed("---"), cs.Dimmed("---"))
		if opts.AllNameSpaces {
			s += fmt.Sprintf("%s\t", ns)
		}
		return fmt.Sprintf("%s%s", s, cs.Dimmed("NoRun"))
	}
	starttime := formatting.Age(status.StartTime, c)
	if opts.UseRealTime {
		starttime = status.StartTime.Format("2006-01-02T15:04:05Z07:00") // RFC3339
	}
	s := fmt.Sprintf("%s\t%s\t%s",
		cs.HyperLink(formatting.ShortSHA(*status.SHA), *status.SHAURL),
		starttime,
		formatting.PRDuration(*status))
	if opts.AllNameSpaces {
		s = fmt.Sprintf("%s\t%s", s, ns)
	}

	reason := "UNKNOWN"
	if len(status.Conditions) > 0 {
		reason = status.Conditions[0].Reason
	}
	return fmt.Sprintf("%s\t%s", s, cs.HyperLink(cs.ColorStatus(reason), *status.LogURL))
}

func list(ctx context.Context, cs *params.Run, opts *cli.PacCliOpts, ioStreams *cli.IOStreams, clock clockwork.Clock, selectors string) error {
	if opts.Namespace != "" {
		cs.Info.Kube.Namespace = opts.Namespace
	}
	if opts.AllNameSpaces {
		cs.Info.Kube.Namespace = ""
	}

	lopt := metav1.ListOptions{LabelSelector: selectors}

	repositories, err := cs.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(cs.Info.Kube.Namespace).List(
		ctx, lopt,
	)
	if err != nil {
		return err
	}

	type repoStatusInfo struct {
		Status               *v1alpha1.RepositoryRunStatus
		Name, Namespace, URL string
	}
	repoStatuses := []repoStatusInfo{}
	for _, repo := range repositories.Items {
		rs := repoStatusInfo{
			Name:      repo.GetName(),
			URL:       repo.Spec.URL,
			Namespace: repo.GetNamespace(),
		}
		statuses := status.MixLivePRandRepoStatus(ctx, cs, repo)
		if len(statuses) > 0 {
			rs.Status = &statuses[0]
		}
		repoStatuses = append(repoStatuses, rs)
	}

	if len(repoStatuses) == 0 {
		return fmt.Errorf("no repo found")
	}

	w := ansiterm.NewTabWriter(ioStreams.Out, 0, 5, 3, ' ', tabwriter.TabIndent)
	colorScheme := ioStreams.ColorScheme()
	data := struct {
		Statuses    []repoStatusInfo
		ColorScheme *cli.ColorScheme
		Clock       clockwork.Clock
		Opts        *cli.PacCliOpts
	}{
		Statuses:    repoStatuses,
		ColorScheme: colorScheme,
		Clock:       clock,
		Opts:        opts,
	}
	funcMap := template.FuncMap{
		"formatStatus": formatStatus,
	}

	t := template.Must(template.New("LS Template").Funcs(funcMap).Parse(lsTmpl))
	if err := t.Execute(w, data); err != nil {
		return err
	}
	return w.Flush()
}
