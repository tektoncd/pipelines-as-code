package logs

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/browser"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/completion"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	"github.com/spf13/cobra"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const longhelp = `

logs - show the PipelineRun logs attached to a Repository

tkn pac logs will get the logs of a PipelineRun belonging to a Repository.

the PipelineRun needs to exist on the kubernetes cluster to be able to display the logs.`

const (
	namespaceFlag          = "namespace"
	limitFlag              = "limit"
	tknPathFlag            = "tkn-path"
	defaultLimit           = -1
	openWebBrowserFlag     = "web"
	useLastPipelineRunFlag = "last"
)

type logOption struct {
	cs         *params.Run
	cw         clockwork.Clock
	opts       *cli.PacCliOpts
	ioStreams  *cli.IOStreams
	repoName   string
	tknPath    string
	limit      int
	webBrowser bool
	useLastPR  bool
	execFunc   func(string, []string, []string) error
}

func Command(run *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs",
		Long:  longhelp,
		Short: "Display the PipelineRun logs from a Repository",
		Annotations: map[string]string{
			"commandType": "main",
		},
		ValidArgsFunction: completion.ParentCompletion,
		RunE: func(cmd *cobra.Command, args []string) error {
			var err error
			var repoName string
			opts := cli.NewCliOptions()

			opts.Namespace, err = cmd.Flags().GetString(namespaceFlag)
			if err != nil {
				return err
			}

			if len(args) > 0 {
				repoName = args[0]
			}

			ctx := context.Background()
			err = run.Clients.NewClients(ctx, &run.Info)
			if err != nil {
				return err
			}

			limit, err := cmd.Flags().GetInt(limitFlag)
			if err != nil {
				return err
			}

			webBrowser, err := cmd.Flags().GetBool(openWebBrowserFlag)
			if err != nil {
				return err
			}

			useLastPR, err := cmd.Flags().GetBool(useLastPipelineRunFlag)
			if err != nil {
				return err
			}

			tknPath, err := cmd.Flags().GetString(tknPathFlag)
			if err != nil {
				return err
			}
			if tknPath == "" {
				if tknPath, err = getTknPath(); err != nil {
					return err
				}

				if tknPath == "" {
					return fmt.Errorf("cannot find tkn binary in Path")
				}
			}

			lopts := &logOption{
				cs:         run,
				cw:         clockwork.NewRealClock(),
				opts:       opts,
				ioStreams:  ioStreams,
				repoName:   repoName,
				limit:      limit,
				webBrowser: webBrowser,
				tknPath:    tknPath,
				useLastPR:  useLastPR,
				execFunc:   defaultExecFunc,
			}
			return log(ctx, lopts)
		},
	}

	cmd.Flags().StringP(
		tknPathFlag, "", "", fmt.Sprintf("Path to the %s binary (default to search for it in you $PATH)", settings.TknBinaryName))

	cmd.Flags().StringP(
		namespaceFlag, "n", "", "If present, the namespace scope for this CLI request")
	_ = cmd.RegisterFlagCompletionFunc(namespaceFlag,
		func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			return completion.BaseCompletion(namespaceFlag, args)
		},
	)

	cmd.Flags().BoolP(
		openWebBrowserFlag, "w", false, "Open Web browser to detected console instead of using tkn")

	cmd.Flags().BoolP(
		useLastPipelineRunFlag, "L", false, "show logs of the last PipelineRun")

	cmd.Flags().IntP(
		limitFlag, "", defaultLimit, "Limit the number of PipelineRun to show (-1 is unlimited)")

	return cmd
}

func getTknPath() (string, error) {
	fname, err := exec.LookPath(settings.TknBinaryName)
	if err != nil {
		return "", err
	}

	return filepath.Abs(fname)
}

// getPipelineRunsToRepo returns all PipelineRuns running in a namespace.
func getPipelineRunsToRepo(ctx context.Context, lopt *logOption, repoName string) ([]string, error) {
	opts := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s",
			keys.Repository, formatting.CleanValueKubernetes(repoName)),
	}
	runs, err := lopt.cs.Clients.Tekton.TektonV1().PipelineRuns(lopt.opts.Namespace).List(ctx, opts)
	if err != nil {
		return []string{}, err
	}
	runslen := len(runs.Items)
	if runslen > 1 {
		sort.PipelineRunSortByStartTime(runs.Items)
	}

	if lopt.limit > runslen {
		lopt.limit = runslen
	}

	ret := []string{}
	for i, run := range runs.Items {
		label := "running since"
		date := run.Status.StartTime
		if run.Status.CompletionTime != nil {
			label = "completed"
			date = run.Status.CompletionTime
		}
		if lopt.limit > -1 && i > lopt.limit {
			continue
		}
		ret = append(ret, fmt.Sprintf("%s %s %s", run.Name, label, formatting.Age(date, lopt.cw)))
	}
	return ret, nil
}

func log(ctx context.Context, lo *logOption) error {
	var repository *v1alpha1.Repository
	var err error

	if lo.opts.Namespace != "" {
		lo.cs.Info.Kube.Namespace = lo.opts.Namespace
	}

	if lo.repoName != "" {
		repository, err = lo.cs.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(lo.cs.Info.Kube.Namespace).Get(ctx,
			lo.repoName, metav1.GetOptions{})
		if err != nil {
			return err
		}
	} else {
		repository, err = prompt.SelectRepo(ctx, lo.cs, lo.cs.Info.Kube.Namespace)
		if err != nil {
			return err
		}
	}

	allprs, err := getPipelineRunsToRepo(ctx, lo, repository.GetName())
	if err != nil {
		return err
	}
	if len(allprs) == 0 {
		return fmt.Errorf("cannot detect pipelineruns belonging to repository: %s", repository.GetName())
	}
	var replyString string
	if lo.useLastPR || len(allprs) == 1 {
		replyString = allprs[0]
	} else {
		if err := prompt.SurveyAskOne(&survey.Select{
			Message: "Select a PipelineRun",
			Options: allprs,
		}, &replyString); err != nil {
			return err
		}
	}
	replyName := strings.Fields(replyString)[0]

	if lo.webBrowser {
		return showLogsWithWebConsole(ctx, lo, replyName)
	}
	return showlogswithtkn(lo.execFunc, lo.tknPath, replyName, lo.cs.Info.Kube.Namespace)
}

func showLogsWithWebConsole(ctx context.Context, lo *logOption, pr string) error {
	if os.Getenv("PAC_TEKTON_DASHBOARD_URL") != "" {
		lo.cs.Clients.SetConsoleUI(&consoleui.TektonDashboard{BaseURL: os.Getenv("PAC_TEKTON_DASHBOARD_URL")})
	}

	prObj := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pr,
			Namespace: lo.cs.Info.Kube.Namespace,
		},
	}
	return browser.OpenWebBrowser(ctx, lo.cs.Clients.ConsoleUI().DetailURL(prObj))
}

func showlogswithtkn(execFn func(string, []string, []string) error, tknPath, pr, ns string) error {
	if err := execFn(tknPath, []string{tknPath, "pr", "logs", "-f", "-n", ns, pr}, os.Environ()); err != nil {
		return fmt.Errorf("failed to exec tkn: %w", err)
	}
	return nil
}
