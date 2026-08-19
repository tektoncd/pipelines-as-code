package bootstrap

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/go-github/v90/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/hostpolicy"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/vcshost"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// deleteSecret delete secret first if it exists.
func deleteSecret(ctx context.Context, run *params.Run, opts *bootstrapOpts) error {
	return run.Clients.Kube.CoreV1().Secrets(opts.targetNamespace).Delete(ctx, secretName, metav1.DeleteOptions{})
}

// create a kubernetes secret from the manifest file values.
func createPacSecret(ctx context.Context, run *params.Run, opts *bootstrapOpts, manifest *github.AppConfig) error {
	_, err := run.Clients.Kube.CoreV1().Secrets(opts.targetNamespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: secretName,
			Labels: map[string]string{
				"app.kubernetes.io/part-of": "pipelines-as-code",
			},
		},
		Data: map[string][]byte{
			"github-application-id": []byte(fmt.Sprintf("%d", manifest.GetID())),
			"github-private-key":    []byte(manifest.GetPEM()),
			"webhook.secret":        []byte(manifest.GetWebhookSecret()),
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	fmt.Fprintf(opts.ioStreams.Out, "🔑 Secret %s has been created in the %s namespace\n", secretName, opts.targetNamespace)
	return nil
}

// trustProviderHostname adds the GitHub instance the App was created on to the
// controller allowlist, so that the controller is allowed to send its
// credentials there. Seeding it here also closes the trust on first use window
// immediately: the hostname comes from the user running the bootstrap, not from
// a webhook payload.
func trustProviderHostname(ctx context.Context, run *params.Run, opts *bootstrapOpts) error {
	configMapName := hostpolicy.ControllerConfigMap(run)

	host, err := vcshost.Parse(opts.GithubAPIURL)
	if err != nil {
		// The App already exists at this point, so failing the bootstrap would
		// leave a half provisioned install. Warn loudly instead: skipping a
		// security setup step must never be silent.
		warnUntrustedHostname(opts, configMapName, opts.GithubAPIURL, err)
		return nil //nolint:nilerr
	}

	host = vcshost.Canonical(host)

	cms := run.Clients.Kube.CoreV1().ConfigMaps(opts.targetNamespace)
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cm, err := cms.Get(ctx, configMapName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		hosts, err := vcshost.ParseAllowlist(cm.Data[settings.TrustedProviderHostnamesKey])
		if err != nil {
			return err
		}
		if slices.Contains(hosts, host) {
			return nil
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[settings.TrustedProviderHostnamesKey] = vcshost.Join(append(hosts, host))
		_, err = cms.Update(ctx, cm, metav1.UpdateOptions{})
		return err
	})
	if err != nil {
		warnUntrustedHostname(opts, configMapName, host, err)
		return nil //nolint:nilerr
	}

	fmt.Fprintf(opts.ioStreams.Out, "🔒 Hostname %s has been added to the %q key of the %s ConfigMap\n",
		host, settings.TrustedProviderHostnamesKey, configMapName)
	return nil
}

// warnUntrustedHostname tells the user, with a ready to run command, that the
// controller will refuse to talk to the instance until they configure it.
func warnUntrustedHostname(opts *bootstrapOpts, configMapName, host string, cause error) {
	fmt.Fprintf(opts.ioStreams.ErrOut,
		"\n⚠️  Could not add %q to the %q key of the %s ConfigMap: %v\n"+
			"   Pipelines-as-Code refuses to send credentials to a host it does not trust.\n"+
			"   Run this before using the App:\n"+
			"   kubectl -n %s patch configmap %s --type merge -p '{\"data\":{\"%s\":\"HOSTNAME\"}}'\n\n",
		host, settings.TrustedProviderHostnamesKey, configMapName, cause,
		opts.targetNamespace, configMapName, settings.TrustedProviderHostnamesKey)
}

func checkPipelinesInstalled(run *params.Run) (bool, error) {
	return checkGroupInstalled(run, "tekton.dev")
}

func checkOpenshiftRoute(run *params.Run) (bool, error) {
	return checkGroupInstalled(run, openShiftRouteGroup)
}

func checkGroupInstalled(run *params.Run, resourceGroup string) (bool, error) {
	sg, err := run.Clients.Kube.Discovery().ServerGroups()
	if err != nil {
		return false, err
	}
	found := false
	for _, t := range sg.Groups {
		if t.Name == resourceGroup {
			found = true
		}
	}
	return found, nil
}
