package bootstrap

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/prompt"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/random"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

//go:embed templates/gosmee.yaml
var gosmeeYaml string

const (
	pacGHRepoOwner             = "openshift-pipelines"
	pacGHRepoName              = "pipelines-as-code"
	rawGHURL                   = "https://raw.githubusercontent.com"
	openshiftReleaseYaml       = "release.yaml"
	k8ReleaseYaml              = "release.k8s.yaml"
	tektonDashboardServiceName = "tekton-dashboard"

	gosmeeInstallHelpText = `Pipelines as Code does not install a Ingress object to make the controller accessing from the internet
we can install a webhook forwarder called gosmee (https://github.com/chmouel/gosmee) using a %s URL
this will let your git platform provider (ie: Github) to reach the controller without having to be having public access`
	minNumOfCharForRandomForwarderID = 12
)

func getLatestRelease(ctx context.Context, k8release string) (string, string, error) {
	// Always go to public
	gh := github.NewClient(nil)
	release, _, err := gh.Repositories.GetLatestRelease(ctx, pacGHRepoOwner, pacGHRepoName)
	if err != nil {
		return "", "", err
	}
	return release.GetTagName(),
		fmt.Sprintf("%s/%s/%s/release-%s/release%s.yaml",
			rawGHURL, pacGHRepoOwner, pacGHRepoName, release.GetTagName(), k8release),
		nil
}

// kubectlApply get kubectl binary and apply a yaml file.
func kubectlApply(ctx context.Context, uri string) error {
	path, err := exec.LookPath("kubectl")
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, "apply", "-f", uri)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w\n%s", err, out)
	}
	return nil
}

// getdashboardURL try to detect dashboardURL in all ingresses, if we can't
// detect it then just ask the user for the URL.
func getDashboardURL(ctx context.Context, opts *bootstrapOpts, run *params.Run) error {
	ingresses, err := run.Clients.Kube.NetworkingV1().Ingresses("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, ingress := range ingresses.Items {
			for _, rule := range ingress.Spec.Rules {
				if rule.HTTP != nil {
					for _, path := range rule.HTTP.Paths {
						if path.Backend.Service != nil {
							if path.Backend.Service.Name == tektonDashboardServiceName {
								protocol := "http"
								if ingress.Spec.TLS != nil {
									protocol = "https"
								}
								useDetectedDashboard, err := askYN(true,
									fmt.Sprintf("👀 We have detected a tekton dashboard install on %s://%s", protocol, rule.Host),
									"Do you want me to use it?", opts.ioStreams.Out)
								if err != nil {
									return err
								}
								if useDetectedDashboard {
									opts.dashboardURL = fmt.Sprintf("%s://%s", protocol, rule.Host)
									return nil
								}
							}
						}
					}
				}
			}
		}
	}

	var answer string
	qs := &survey.Input{
		Message: "Enter your Tekton Dashboard URL: ",
	}
	if err := prompt.SurveyAskOne(qs, &answer); err != nil {
		return err
	}
	if answer == "" {
		return nil
	}
	if _, err := url.ParseRequestURI(answer); err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	opts.dashboardURL = answer
	return nil
}

// installGosmeeForwarder Install a gosmee forwarded to hook.pipelinesascode.com.
func installGosmeeForwarder(ctx context.Context, opts *bootstrapOpts) error {
	gosmeInstall, err := askYN(true, fmt.Sprintf(gosmeeInstallHelpText, opts.forwarderURL), "Do you want me to install the gosmee forwarder?", opts.ioStreams.Out)
	if err != nil {
		return err
	}
	if !gosmeInstall {
		return fmt.Errorf("please install a ingress object pointing to the controller service as documented here: https://is.gd/FzI0eb and pass the full ingress url as argument to the --route-url flag")
	}

	// maybe we can use https://webhook.chmouel.com too
	opts.RouteName = fmt.Sprintf("%s/%s", opts.forwarderURL, random.AlphaString(minNumOfCharForRandomForwarderID))
	tmpl := strings.ReplaceAll(gosmeeYaml, "FORWARD_URL", opts.RouteName)
	f, err := os.CreateTemp("", "pac-gosmee")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(tmpl); err != nil {
		return err
	}
	if err := kubectlApply(ctx, f.Name()); err != nil {
		return err
	}
	fmt.Fprintf(opts.ioStreams.Out, "💡 Your gosmee forward URL has been generated: %s\n", opts.RouteName)
	return nil
}

func installPac(ctx context.Context, run *params.Run, opts *bootstrapOpts) error {
	var latestVersion, latestReleaseYaml, nightlyReleaseYaml string
	k8Ext := ""

	isOpenShift, _ := checkOpenshiftRoute(run)
	if isOpenShift {
		nightlyReleaseYaml = openshiftReleaseYaml
	} else {
		nightlyReleaseYaml = k8ReleaseYaml
		k8Ext = ".k8s"
	}

	if opts.installNightly {
		latestReleaseYaml = fmt.Sprintf("%s/%s/%s/nightly/%s",
			rawGHURL, pacGHRepoOwner, pacGHRepoName, nightlyReleaseYaml)
		latestVersion = "nightly"
	} else {
		var err error
		latestVersion, latestReleaseYaml, err = getLatestRelease(ctx, k8Ext)
		if err != nil {
			return err
		}
	}

	if !opts.forceInstall {
		doinstall, err := askYN(true,
			fmt.Sprintf("🕵️ Pipelines as Code doesn't seems to be installed in %s namespace", opts.targetNamespace),
			fmt.Sprintf("Do you want me to install Pipelines as Code %s?", latestVersion), opts.ioStreams.Out)
		if err != nil {
			return err
		}
		if !doinstall {
			return fmt.Errorf("i will let you install Pipelines as Code")
		}
	}

	if err := kubectlApply(ctx, latestReleaseYaml); err != nil {
		return err
	}

	fmt.Fprintf(opts.ioStreams.Out, "✓ Pipelines-as-Code %s has been installed\n", latestVersion)

	if opts.forceInstallGosmee {
		if err := installGosmeeForwarder(ctx, opts); err != nil {
			return err
		}
	}

	if !isOpenShift && opts.dashboardURL == "" {
		if err := getDashboardURL(ctx, opts, run); err != nil {
			return err
		}
		// only updating for now here since we don't have any other stuff to update yet in there
		// maybe we will have to move this to a different function in the future
		return updatePACConfigMap(ctx, run, opts)
	}
	return nil
}

func updatePACConfigMap(ctx context.Context, run *params.Run, opts *bootstrapOpts) error {
	cm, err := run.Clients.Kube.CoreV1().ConfigMaps(opts.targetNamespace).Get(ctx, "pipelines-as-code", metav1.GetOptions{})
	if err != nil {
		return err
	}

	if opts.dashboardURL != "" {
		cm.Data["tekton-dashboard-url"] = opts.dashboardURL
	}

	if _, err = run.Clients.Kube.CoreV1().ConfigMaps(opts.targetNamespace).Update(ctx, cm, metav1.UpdateOptions{}); err != nil {
		return err
	}

	return nil
}
