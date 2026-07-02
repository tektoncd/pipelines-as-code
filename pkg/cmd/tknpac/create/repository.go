package create

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	pacInfo "github.com/openshift-pipelines/pipelines-as-code/pkg/cli/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/webhook"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/bootstrap"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cmd/tknpac/generate"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/git"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/spf13/cobra"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	noColorFlag = "no-color"
)

type RepoOptions struct {
	Event        *info.Event
	Repository   *apipac.Repository
	Run          *params.Run
	GitInfo      *git.Info
	pacNamespace string
	Provider     string

	IoStreams *cli.IOStreams
	cliOpts   *cli.PacCliOpts
}

func repositoryCommand(run *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	createOpts := &RepoOptions{
		Event:      info.NewEvent(),
		Repository: &apipac.Repository{},
		Run:        run,
	}
	cmd := &cobra.Command{
		Use:     "repository",
		Aliases: []string{"repo"},
		Short:   "Create a repository",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx := context.Background()
			createOpts.IoStreams = ioStreams
			createOpts.cliOpts = cli.NewCliOptions()
			createOpts.IoStreams.SetColorEnabled(!createOpts.cliOpts.NoColoring)

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			createOpts.GitInfo = git.GetGitInfo(cwd)
			if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
				return err
			}

			if err := getRepoURL(createOpts); err != nil {
				return err
			}

			repoName, repoNamespace, err := createOpts.Create(ctx)
			if err != nil {
				return err
			}

			var providerName string
			installed, installationNS, err := bootstrap.DetectPacInstallation(ctx, createOpts.pacNamespace, run)
			if !installed {
				return fmt.Errorf("pipelines-as-code is not installed in the cluster")
			}
			if err != nil {
				return err
			}

			if pacInfo.IsGithubAppInstalled(ctx, run, installationNS) {
				if strings.Contains(createOpts.Event.URL, "github") {
					return createOpts.generateTemplate(nil)
				}
			}

			if providerName, err = webhook.GetProviderName(createOpts.Event.URL); err != nil {
				return err
			}

			createOpts.Provider = providerName
			config := &webhook.Options{
				Run:                      run,
				PACNamespace:             createOpts.pacNamespace,
				RepositoryURL:            createOpts.Event.URL,
				IOStreams:                ioStreams,
				RepositoryName:           repoName,
				RepositoryNamespace:      repoNamespace,
				RepositoryCreateORUpdate: true,
			}

			if err := config.Install(ctx, createOpts.Provider); err != nil {
				return err
			}
			return createOpts.generateTemplate(nil)
		},
		Annotations: map[string]string{
			"commandType": "main",
		},
	}

	cmd.PersistentFlags().BoolP(noColorFlag, "C", !ioStreams.ColorEnabled(), "Disable coloring")
	cmd.PersistentFlags().StringVar(&createOpts.Repository.Name, "name", "", "Repository name")
	cmd.PersistentFlags().StringVar(&createOpts.Event.URL, "url", "", "Repository URL")
	cmd.PersistentFlags().StringVarP(&createOpts.Repository.Namespace, "namespace", "n", "",
		"The target namespace where the runs will be created")
	cmd.PersistentFlags().StringVarP(&createOpts.pacNamespace, "pac-namespace",
		"", "", "The namespace where pac is installed")
	return cmd
}

func (r *RepoOptions) generateTemplate(gopt *generate.Opts) error {
	if gopt == nil {
		gopt = generate.MakeOpts()
	}
	gopt.GitInfo = r.GitInfo
	gopt.IOStreams = r.IoStreams
	gopt.CLIOpts = r.cliOpts

	// defaulting the values for repo create command
	gopt.Event.EventType = "pull_request, push"
	gopt.Event.BaseBranch = "main"
	gopt.Event.URL = r.Event.URL

	return generate.Generate(gopt, false)
}

func (r *RepoOptions) Create(ctx context.Context) (string, string, error) {
	if err := getOrCreateNamespace(ctx, r); err != nil {
		return "", "", err
	}

	repoName, repoNamespace, err := createRepoCRD(ctx, r)
	if err != nil {
		return "", "", err
	}
	return repoName, repoNamespace, err
}

// getOrCreateNamespace ask and create namespace or use the default one.
func getOrCreateNamespace(ctx context.Context, opts *RepoOptions) error {
	if opts.Repository.Namespace != "" {
		return nil
	}

	// by default, use the current namespace unless it's default or
	// pipelines-as-code and then propose some meaningful namespace based on the
	// git url.
	autoNS := opts.Run.Info.Kube.Namespace

	if (autoNS == "default" || autoNS == "pipelines-as-code") &&
		opts.GitInfo.URL != "" {
		autoNS = filepath.Base(opts.GitInfo.URL) + "-pipelines"
	}

	var chosenNS string
	msg := fmt.Sprintf("Please enter the namespace where the pipeline should run (default: %s):", autoNS)
	if err := prompt.SurveyAskOne(&survey.Input{Message: msg}, &chosenNS); err != nil {
		return err
	}

	// set the namespace as the default one
	if chosenNS == "" {
		chosenNS = autoNS
	}
	// check if the namespace exists if it does just exit
	_, err := opts.Run.Clients.Kube.CoreV1().Namespaces().Get(ctx, chosenNS, metav1.GetOptions{})
	if err == nil {
		opts.Repository.Namespace = chosenNS
		return nil
	}

	fmt.Fprintf(
		opts.IoStreams.Out, "%s Namespace %s is not found\n",
		opts.IoStreams.ColorScheme().WarningIcon(),
		chosenNS,
	)
	msg = fmt.Sprintf("Would you like me to create the namespace %s?", chosenNS)
	var createNamespace bool
	if err := prompt.SurveyAskOne(&survey.Confirm{Message: msg, Default: true}, &createNamespace); err != nil {
		return err
	}
	if !createNamespace {
		return fmt.Errorf("you need to create the target namespace first")
	}

	_, err = opts.Run.Clients.Kube.CoreV1().Namespaces().Create(ctx,
		&v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: chosenNS,
			},
		},
		metav1.CreateOptions{})
	opts.Repository.Namespace = chosenNS
	return err
}

// getRepoURL get the repository URL from the user using the git url as default.
func getRepoURL(opts *RepoOptions) error {
	if opts.Event.URL != "" {
		return nil
	}

	q := "Enter the Git repository URL "
	var err error
	if opts.GitInfo.URL != "" {
		opts.GitInfo.URL, err = cleanupGitURL(opts.GitInfo.URL)
		if err != nil {
			return err
		}
		q += fmt.Sprintf("(default: %s)", opts.GitInfo.URL)
	}
	q += ": "
	if err := prompt.SurveyAskOne(&survey.Input{Message: q}, &opts.Event.URL); err != nil {
		return err
	}
	if opts.Event.URL != "" {
		return nil
	}
	if opts.Event.URL == "" && opts.GitInfo.URL != "" {
		opts.Event.URL = opts.GitInfo.URL
		return nil
	}

	return fmt.Errorf("no url has been provided")
}

func cleanupGitURL(rawURL string) (string, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s://%s%s", parsedURL.Scheme, parsedURL.Host, parsedURL.Path), nil
}

func createRepoCRD(ctx context.Context, opts *RepoOptions) (string, string, error) {
	repoOwner, err := formatting.GetRepoOwnerFromURL(opts.Event.URL)
	if err != nil {
		return "", "", fmt.Errorf("invalid git URL: %s, it should be of format: https://gitprovider/project/repository", opts.Event.URL)
	}
	repositoryName := formatting.CleanKubernetesName(repoOwner)
	opts.Repository, err = opts.Run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(opts.Repository.Namespace).Create(
		ctx,
		&apipac.Repository{
			ObjectMeta: metav1.ObjectMeta{
				Name: repositoryName,
			},
			Spec: apipac.RepositorySpec{
				URL: opts.Event.URL,
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		return "", "", err
	}
	cs := opts.IoStreams.ColorScheme()
	fmt.Fprintf(
		opts.IoStreams.Out, "%s Repository %s has been created in %s namespace\n",
		cs.SuccessIconWithColor(cs.Green),
		repositoryName,
		opts.Repository.Namespace,
	)
	return opts.Repository.Name, opts.Repository.Namespace, nil
}
