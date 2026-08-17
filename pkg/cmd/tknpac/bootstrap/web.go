package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/browser"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cli/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
)

// startWebServer starts a webserver that will redirect the user to the github app creation page.
func startWebServer(ctx context.Context, opts *bootstrapOpts, run *params.Run, jeez string) error {
	m := http.NewServeMux()
	//nolint: gosec
	s := http.Server{
		Addr:              fmt.Sprintf(":%d", opts.webserverPort),
		Handler:           m,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	codeCh := make(chan string)
	m.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code != "" {
			fmt.Fprint(rw, successTmpl)
			codeCh <- code
		} else {
			url := opts.GithubAPIURL
			if opts.GithubOrganizationName != "" {
				url = filepath.Join(opts.GithubAPIURL, "organizations", opts.GithubOrganizationName)
			}
			fmt.Fprintf(rw, indexTmpl, url, jeez)
		}
	})
	go func() {
		url := fmt.Sprintf("http://localhost:%d", opts.webserverPort)
		fmt.Fprintf(opts.ioStreams.Out, "🌍 Starting a web browser on %s, click on the button to create your GitHub APP\n", url)
		//nolint:errcheck
		go browser.OpenWebBrowser(ctx, url)
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	code := <-codeCh

	err := s.Shutdown(ctx)
	if err != nil {
		return err
	}

	gprovider, err := getGHClient(opts)
	if err != nil {
		return err
	}

	manifest, _, err := gprovider.Apps.CompleteAppManifest(ctx, code)
	if err != nil {
		return err
	}

	err = createPacSecret(ctx, run, opts, manifest)
	if err != nil {
		return err
	}

	if err := trustProviderHostname(ctx, run, opts); err != nil {
		return err
	}

	if err := info.UpdateInfoConfigMap(ctx, run, &info.Options{
		TargetNamespace: opts.targetNamespace,
		ControllerURL:   opts.RouteName,
		Provider:        provider.GitHubApp,
	}); err != nil {
		return err
	}

	fmt.Fprintf(opts.ioStreams.Out, "🚀 You can now add your newly created application on your repository by going to this URL:\n\n%s\n\n", *manifest.HTMLURL)
	fmt.Fprintf(opts.ioStreams.Out, "💡 Don't forget to run the \"%s pac create repository\" to create a new Repository CR on your cluster.\n", settings.TknBinaryName)

	detectString := detectSelfSignedCertificate(ctx, opts.RouteName)
	if detectString != "" {
		fmt.Fprintln(opts.ioStreams.Out, detectString)
	}

	return nil
}
