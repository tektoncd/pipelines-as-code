package bootstrap

import (
	"context"
	"fmt"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
