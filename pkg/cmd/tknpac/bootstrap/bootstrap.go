package bootstrap

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	kapierror "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	pacNS                  = "pipelines-as-code"
	openShiftRouteGroup    = "route.openshift.io"
	openShiftRouteVersion  = "v1"
	openShiftRouteResource = "routes"
	secretName             = "pipelines-as-code-secret"
	defaultProviderType    = "github-app"
	defaultWebForwarderURL = "https://hook.pipelinesascode.com"
)

var providerTargets = []string{"github-app", "github-enterprise-app"}

var defaultNamespaces = []string{"openshift-pipelines", pacNS}

type bootstrapOpts struct {
	forceInstallGosmee bool
	providerType       string
	installNightly     bool
	skipInstall        bool
	skipGithubAPP      bool
	forceInstall       bool
	webserverPort      int
	cliOpts            *cli.PacCliOpts
	ioStreams          *cli.IOStreams
	targetNamespace    string
	autoDetectedRoute  bool
	forwarderURL       string
	dashboardURL       string

	RouteName              string
	GithubAPIURL           string
	GithubApplicationName  string
	GithubApplicationURL   string
	GithubOrganizationName string
	forceGitHubApp         bool
}

const infoConfigMap = "pipelines-as-code-info"

const indexTmpl = `
<html>
<body>
  <form method="post" action="%s/settings/apps/new">
  <input type="submit" value="Create your GitHub APP"></input>
  <input type="hidden" name="manifest" id="manifest"/>
  </form>
  <script>
   input = document.getElementById("manifest")
   input.value = JSON.stringify(%s)
  </script>
</body>
</html>
`

var successTmpl = fmt.Sprintf(`
<html><body>You have <span style=\"color: green\">successfully</span> created a new GitHub application. Return to the %s pac CLI to finish the installation.</body></html>
`, settings.TknBinaryName)

func install(ctx context.Context, run *params.Run, opts *bootstrapOpts) error {
	if !opts.forceInstall {
		fmt.Fprintln(opts.ioStreams.Out, "=> Checking if Pipelines-as-Code is installed.")
	}
	tektonInstalled, err := checkPipelinesInstalled(run)
	if err != nil {
		return err
	}
	if !tektonInstalled {
		return fmt.Errorf("no Tekton installation has been found on this kubernetes cluster, install Tekton first before launching this command")
	}

	// if we gt a ns back it means it has been detected in here so keep it as is.
	// or else just set the default to pacNS
	installed, ns, err := DetectPacInstallation(ctx, opts.targetNamespace, run)

	// installed but there is error for missing resources
	if installed && err != nil && !opts.forceInstall {
		return err
	}
	if ns != "" {
		opts.targetNamespace = ns
	} else if opts.targetNamespace == "" {
		opts.targetNamespace = pacNS
	}

	if !opts.forceInstall && err == nil && installed {
		fmt.Fprintln(opts.ioStreams.Out, "✓ Pipelines as Code is already installed.")
	} else if err := installPac(ctx, run, opts); err != nil {
		return err
	}
	return nil
}

func createSecret(ctx context.Context, run *params.Run, opts *bootstrapOpts) error {
	var err error

	if opts.RouteName == "" && !opts.forceInstallGosmee {
		opts.RouteName, _ = DetectOpenShiftRoute(ctx, run, opts.targetNamespace)
		if opts.RouteName != "" {
			opts.autoDetectedRoute = true
		}
	}
	if err := askQuestions(opts); err != nil {
		return err
	}

	if opts.forceGitHubApp {
		if err := deleteSecret(ctx, run, opts); err != nil {
			return err
		}
	}

	jeez, err := generateManifest(opts)
	if err != nil {
		return err
	}

	return startWebServer(ctx, opts, run, string(jeez))
}

func Command(run *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	opts := &bootstrapOpts{
		ioStreams: ioStreams,
	}
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Long:  "Bootstrap Pipelines as Code",
		Short: "Bootstrap Pipelines as Code.",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx := context.Background()
			opts.cliOpts = cli.NewCliOptions()
			opts.ioStreams.SetColorEnabled(!opts.cliOpts.NoColoring)
			if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
				return err
			}

			if !opts.skipInstall {
				if err := install(ctx, run, opts); err != nil {
					return err
				}
			}

			if !opts.forceGitHubApp {
				if info.IsGithubAppInstalled(ctx, run, opts.targetNamespace) {
					fmt.Fprintln(opts.ioStreams.Out, "👌 Skips bootstrapping GitHub App, as one is already configured. Please pass --force-configure to override existing")
					return nil
				}
			}

			if !opts.skipGithubAPP {
				if err := createSecret(ctx, run, opts); err != nil {
					return err
				}
			}
			return nil
		},
		Annotations: map[string]string{
			"commandType": "main",
		},
	}
	cmd.AddCommand(GithubApp(run, ioStreams))

	addCommonFlags(cmd, ioStreams)
	addGithubAppFlag(cmd, opts)

	return cmd
}

func GithubApp(run *params.Run, ioStreams *cli.IOStreams) *cobra.Command {
	opts := &bootstrapOpts{
		ioStreams: ioStreams,
	}

	cmd := &cobra.Command{
		Use:   "github-app",
		Long:  "A command helper to help you create the Pipelines as Code GitHub Application",
		Short: "Create PAC GitHub Application",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx := context.Background()
			opts.cliOpts = cli.NewCliOptions()
			opts.ioStreams.SetColorEnabled(!opts.cliOpts.NoColoring)
			if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
				return err
			}

			var err error
			var installed bool
			installed, opts.targetNamespace, err = DetectPacInstallation(ctx, opts.targetNamespace, run)
			if err != nil {
				return err
			}
			// installed but there is error for missing resources
			if installed && !opts.forceInstall && !opts.skipInstall {
				return fmt.Errorf("pipelines as Code is already installed, please pass --force-install to override existing")
			}

			if !opts.forceGitHubApp {
				if info.IsGithubAppInstalled(ctx, run, opts.targetNamespace) {
					fmt.Fprintln(opts.ioStreams.Out, "👌 Skips bootstrapping GitHub App, as one is already configured. Please pass --force-configure to override existing")
					return nil
				}
			}

			// if the user has specified a github-api-url and it's not a pubcli github url or api url then set it as providerType github-enterprise-app
			// otherwise if no --github-api-url has been provided we ask for it interactively if we want to configure on github-enterprise-app
			if opts.GithubAPIURL != "" {
				if opts.GithubAPIURL == defaultPublicGithub || opts.GithubAPIURL == keys.PublicGithubAPIURL {
					fmt.Fprintf(opts.ioStreams.Out, "👕 Using Public Github on %s\n", keys.PublicGithubAPIURL)
					opts.GithubAPIURL = keys.PublicGithubAPIURL
				} else {
					// check if it finishes with /api/v3 and strip it out if it does
					opts.GithubAPIURL = strings.TrimSuffix(opts.GithubAPIURL, "/api/v3")
					// if we have only a host then add https into it
					if !strings.HasPrefix(opts.GithubAPIURL, "https://") {
						opts.GithubAPIURL = fmt.Sprintf("https://%s", opts.GithubAPIURL)
					}
					fmt.Fprintf(opts.ioStreams.Out, "👔 Using Github Enterprise URL: %s\n", opts.GithubAPIURL)
					opts.providerType = "github-enterprise-app"
				}
			} else {
				if b, _ := askYN(false, "", "Do you need to configure this on GitHub Enterprise?", opts.ioStreams.Out); b {
					opts.providerType = "github-enterprise-app"
				}
			}

			return createSecret(ctx, run, opts)
		},
		Annotations: map[string]string{
			"commandType": "main",
		},
	}
	addCommonFlags(cmd, ioStreams)
	addGithubAppFlag(cmd, opts)
	return cmd
}

func DetectPacInstallation(ctx context.Context, wantedNS string, run *params.Run) (bool, string, error) {
	var installed bool
	_, err := run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories("").List(ctx, metav1.ListOptions{})
	if err != nil && kapierror.IsNotFound(err) {
		return false, "", nil
	}

	installed = true
	if wantedNS != "" {
		_, err := run.Clients.Kube.CoreV1().ConfigMaps(wantedNS).Get(ctx, infoConfigMap, metav1.GetOptions{})
		if err == nil {
			return installed, wantedNS, nil
		}
		return installed, "", fmt.Errorf("could not detect Pipelines as Code configmap in %s namespace : %w, please reinstall", wantedNS, err)
	}

	cm, err := getConfigMap(ctx, run)
	if err == nil {
		return installed, cm.Namespace, nil
	}
	return installed, "", fmt.Errorf("could not detect Pipelines as Code configmap on the cluster, please specify the namespace in which pac is installed: %w", err)
}

func getConfigMap(ctx context.Context, run *params.Run) (*corev1.ConfigMap, error) {
	var (
		err            error
		configMap      *corev1.ConfigMap
		foundConfigmap bool
	)

	for _, n := range defaultNamespaces {
		configMap, err = run.Clients.Kube.CoreV1().ConfigMaps(n).Get(ctx, infoConfigMap, metav1.GetOptions{})
		if err != nil {
			if kapierror.IsNotFound(err) {
				continue
			}
			if strings.Contains(err.Error(), fmt.Sprintf(`cannot get resource "configmaps" in API group "" in the namespace "%s"`, n)) {
				continue
			}
			return nil, err
		}
		if configMap != nil && configMap.GetName() != "" {
			foundConfigmap = true
			break
		}
	}
	if !foundConfigmap {
		return nil, fmt.Errorf("ConfigMap not found in default namespaces (\"openshift-pipelines\", \"pipelines-as-code\")")
	}
	return configMap, nil
}

func addGithubAppFlag(cmd *cobra.Command, opts *bootstrapOpts) {
	cmd.PersistentFlags().StringVar(&opts.GithubOrganizationName, "github-organization-name", "", "Whether you want to target an organization instead of the current user")
	cmd.PersistentFlags().StringVar(&opts.GithubApplicationName, "github-application-name", "", "GitHub Application Name")
	cmd.PersistentFlags().StringVar(&opts.GithubApplicationURL, "github-application-url", "", "GitHub Application URL")
	cmd.PersistentFlags().StringVarP(&opts.GithubAPIURL, "github-hostname", "", "", "Github Enterprise Hostname ")
	cmd.PersistentFlags().StringVarP(&opts.GithubAPIURL, "github-api-url", "", "", "Github Enterprise API Host or URL")
	_ = cmd.Flags().MarkDeprecated("github-api-url", "please use --github-hostname flag instead")
	cmd.PersistentFlags().StringVar(&opts.RouteName, "route-url", "", "The public URL for the pipelines-as-code controller")
	cmd.PersistentFlags().StringVar(&opts.forwarderURL, "web-forwarder-url", defaultWebForwarderURL, "the web forwarder url")
	cmd.PersistentFlags().StringVar(&opts.dashboardURL, "dashboard-url", "", "the full URL to the tekton dashboard ")
	cmd.PersistentFlags().BoolVar(&opts.installNightly, "nightly", false, "Whether to install the nightly Pipelines as Code")
	cmd.PersistentFlags().IntVar(&opts.webserverPort, "webserver-port", 8080, "Webserver port")
	cmd.PersistentFlags().StringVarP(&opts.providerType, "install-type", "t", defaultProviderType,
		fmt.Sprintf("target install type, choices are: %s ", strings.Join(providerTargets, ", ")))
	cmd.PersistentFlags().BoolVar(&opts.forceGitHubApp, "force-configure", false, "Whether we should override existing GitHub App")
	cmd.PersistentFlags().BoolVar(&opts.forceInstall, "force-install", false, "whether we should force pac install even if it's already installed")
	cmd.PersistentFlags().BoolVar(&opts.forceInstallGosmee, "force-gosmee", false, "force install gosmee on OpenShift if your cluster is not reachable from the internet")
	cmd.PersistentFlags().BoolVar(&opts.skipInstall, "skip-install", false, "skip Pipelines as Code installation")
	cmd.PersistentFlags().BoolVar(&opts.skipGithubAPP, "skip-github-app", false, "skip creating github application")
}

func addCommonFlags(cmd *cobra.Command, ioStreams *cli.IOStreams) {
	cmd.PersistentFlags().BoolP("no-color", "C", !ioStreams.ColorEnabled(), "disable coloring")
}
