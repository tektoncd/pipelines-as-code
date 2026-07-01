package resolve

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/git"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/resolve"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/templates"
	"github.com/spf13/cobra"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"go.uber.org/zap"
	"sigs.k8s.io/yaml"
)

var (
	filenames      []string
	parameters     []string
	skipInlining   []string
	noGenerateName bool
	remoteTask     bool
	noSecret       bool
	providerToken  string
	output         string
	asv1beta1      bool
)

var longhelp = fmt.Sprintf(`

resolve - resolve a PipelineRun and all its referenced Pipeline/Tasks embedded.

Resolve the .tekton/pull-request as a single pipelinerun, fetching the remote
tasks according to the annotations in the pipelineRun, apply the parameters
substitutions with -p flags. Output on the standard output or to a file with the
-o flag with the complete PipelineRun resolved.

A simple example that would parse .tekton/pull-request.yaml with all
remote tasks embedded, applying parameter substitutions:

%s pac resolve \
		-f .tekton/pull-request.yaml -o output-file.yaml \
		-p revision=main -p repo_url=https://repo_url/

You can specify multiple template files to combine:

%s pac resolve -f .tekton/pull-request.yaml -f task/referenced.yaml

or a directory where it will get all files ending with .yaml:

%s pac resolve -f .tekton/

If it detects a {{ git_auth_secret }} in the template, it will ask if you want
to provide a token. You can set the environment variable PAC_PROVIDER_TOKEN to
avoid being prompted.

*It does not support tasks from local directories referenced in annotations at the
 moment*.`, settings.TknBinaryName, settings.TknBinaryName, settings.TknBinaryName)

func Command(run *params.Run, streams *cli.IOStreams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resolve",
		Long:  longhelp,
		Short: "Resolve PipelineRun the same way it runs on CI",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx := context.Background()

			errc := run.Clients.NewClients(ctx, &run.Info)

			// only report error here on CLI
			zaplog, err := zap.NewProduction(
				zap.IncreaseLevel(zap.FatalLevel),
			)
			if err != nil {
				return err
			}
			run.Clients.Log = zaplog.Sugar()

			if errc != nil {
				// this check allows resolve to be run without
				// a kubeconfig so users can verify the tkn version
				noConfigErr := strings.Contains(errc.Error(), "Couldn't get kubeConfiguration namespace")
				if !noConfigErr {
					return errc
				}
			} else {
				// it's OK  if pac is not installed, ignore the error
				_ = run.UpdatePacConfig(ctx)
			}

			if len(filenames) == 0 {
				return fmt.Errorf("you need to at least specify a file with -f")
			}

			if err := settings.SyncConfig(run.Clients.Log, &run.Info.Pac.Settings, map[string]string{}, settings.DefaultValidators(), &run.Clients.HTTP); err != nil {
				return err
			}

			mapped := splitArgsInMap(parameters)

			// ignore error
			gitinfo := git.GetGitInfo(".")
			if _, ok := mapped["repo_url"]; !ok && gitinfo.URL != "" {
				mapped["repo_url"] = gitinfo.URL
			}

			if _, ok := mapped["revision"]; !ok && gitinfo.SHA != "" {
				mapped["revision"] = gitinfo.SHA
			}

			if _, ok := mapped["repo_owner"]; !ok && gitinfo.URL != "" {
				repoOwner, err := formatting.GetRepoOwnerFromURL(gitinfo.URL)
				if err != nil {
					return err
				}
				mapped["repo_owner"] = strings.Split(repoOwner, "/")[0]
				mapped["repo_name"] = strings.Split(repoOwner, "/")[1]
			}

			s, err := resolveFilenames(ctx, run, filenames, mapped, asv1beta1)
			if err != nil {
				return err
			}

			if output != "" {
				fmt.Fprintf(streams.Out, "PipelineRun has been written to %s\n", output)
				return os.WriteFile(output, []byte(s), 0o600)
			}

			fmt.Fprintln(streams.Out, s)
			return nil
		},
		Annotations: map[string]string{
			"commandType": "main",
		},
	}
	cmd.Flags().StringSliceVarP(&parameters, "params", "p", filenames,
		"Params to resolve (ie: revision, repo_url)")

	cmd.Flags().StringVarP(&output, "output", "o", "",
		"output to this file instead of stdout")

	cmd.Flags().StringSliceVarP(&filenames, "filename", "f", filenames,
		"Filename, directory, or URL to files to use to create the resource")

	cmd.Flags().StringSliceVarP(&skipInlining, "skip", "s", filenames,
		"skip inlining this task and use them as is (must be present in namespace to be able to use them). multiple values are supported")

	cmd.Flags().BoolVar(&noSecret, "no-secret", false, "don't ask if you would like to generate a secret when git_auth_secret is found in the template")

	cmd.Flags().BoolVar(&noGenerateName, "no-generate-name", false,
		"don't automatically generate a GenerateName for pipelinerun uniqueness")

	cmd.Flags().BoolVar(&remoteTask, "remoteTask", true,
		"set this to false to avoid fetching and embed remote tasks")

	cmd.Flags().BoolVarP(&asv1beta1, "v1beta1", "B", false, "output as tekton v1beta1")

	cmd.Flags().StringVarP(&providerToken, "providerToken", "t", "", "use this token to generate the git-auth secret,\n you can set the environment PAC_PROVIDER_TOKEN to have this set automatically")
	return cmd
}

func splitArgsInMap(args []string) map[string]string {
	m := make(map[string]string)
	for _, e := range args {
		parts := strings.Split(e, "=")
		m[parts[0]] = parts[1]
	}
	return m
}

func resolveFilenames(ctx context.Context, cs *params.Run, filenames []string, params map[string]string, asv1beta1 bool) (string, error) {
	var ret string

	ropt := &resolve.Opts{
		GenerateName:  !noGenerateName,
		RemoteTasks:   remoteTask,
		SkipInlining:  skipInlining,
		ProviderToken: providerToken,
	}
	allTheYamls := expandYamlsAsSingleTemplate(filenames)
	if !noSecret {
		outSecret, secretName, err := makeGitAuthSecret(ctx, cs, filenames, ropt.ProviderToken, params)
		if err != nil {
			return "", err
		}
		if secretName != "" {
			params["git_auth_secret"] = secretName
		}
		ret += outSecret
	}

	// TODO: flags
	allTheYamls = templates.ReplacePlaceHoldersVariables(allTheYamls, params, nil, http.Header{}, map[string]any{})
	// We use github here but since we don't do remotetask we would not care
	providerintf := github.New()
	providerintf.SetLogger(cs.Clients.Log)
	event := info.NewEvent()
	types, err := resolve.ReadTektonTypes(ctx, cs.Clients.Log, allTheYamls)
	if err != nil {
		return "", err
	}
	prun, _, err := resolve.Resolve(ctx, cs, cs.Clients.Log, providerintf, types, event, ropt)
	if err != nil {
		return "", err
	}

	// cleanedup regexp do as much as we can but really it's a lost game to try this
	cleanRe := regexp.MustCompile(`\n(\t|\s)*(status|taskRunTemplate|creationTimestamp|spec|taskRunTemplate|metadata|computeResources):\s*(null|{})\n`)

	for _, run := range prun {
		var doc []byte
		if asv1beta1 {
			//nolint: staticcheck
			nrun := &tektonv1beta1.PipelineRun{}
			if err := nrun.ConvertFrom(ctx, run); err != nil {
				return "", err
			}
			nrun.APIVersion = tektonv1beta1.SchemeGroupVersion.String()
			nrun.Kind = "PipelineRun"
			nrun.SetNamespace("")
			if doc, err = yaml.Marshal(nrun); err != nil {
				return "", err
			}
		} else {
			run.APIVersion = tektonv1.SchemeGroupVersion.String()
			run.Kind = "PipelineRun"
			run.SetNamespace("")
			if doc, err = yaml.Marshal(run); err != nil {
				return "", err
			}
		}
		cleaned := cleanRe.ReplaceAllString(string(doc), "\n")
		ret += fmt.Sprintf("---\n%s\n", cleaned)
	}
	return ret, nil
}

func appendYaml(filename string) string {
	b, err := os.ReadFile(filename)
	if err != nil {
		log.Fatal(err)
	}
	s := string(b)
	if strings.HasPrefix(s, "---") {
		return s
	}
	return fmt.Sprintf("---\n%s", s)
}

// listAllYamls takes a list of paths and returns a list of all the yaml files in those paths even if they are in subdirectories.
func listAllYamls(paths []string) []string {
	ret := []string{}

	for _, path := range paths {
		if stat, err := os.Stat(path); err == nil && !stat.IsDir() {
			ret = append(ret, path)
			continue
		}
		err := filepath.Walk(path, func(fname string, _ os.FileInfo, _ error) error {
			if filepath.Ext(fname) == ".yaml" {
				ret = append(ret, fname)
			}
			return nil
		})
		if err != nil {
			log.Fatalf("Error enumerating files in %s: %v", path, err)
		}
	}
	return ret
}

// expandYamlsAsSingleTemplate takes a list of filenames and returns a single yaml.
func expandYamlsAsSingleTemplate(filenames []string) string {
	var yamlDoc string
	for _, paths := range listAllYamls(filenames) {
		yamlDoc += appendYaml(paths)
	}
	return yamlDoc
}
