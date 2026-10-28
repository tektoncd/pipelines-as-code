package bootstrap

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"

	"github.com/tektoncd/pipelines-as-code/pkg/params"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	routePacLabel = "pipelines-as-code/route=controller"
)

// DetectOpenShiftRoute detect the openshift route where the pac controller is running.
func DetectOpenShiftRoute(ctx context.Context, run *params.Run, targetNamespace string) (string, error) {
	gvr := schema.GroupVersionResource{
		Group: openShiftRouteGroup, Version: openShiftRouteVersion, Resource: openShiftRouteResource,
	}
	routes, err := run.Clients.Dynamic.Resource(gvr).Namespace(targetNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: routePacLabel,
	})
	if err != nil {
		return "", err
	}
	if len(routes.Items) != 1 {
		return "", err
	}
	route := routes.Items[0]

	spec, ok := route.Object["spec"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("couldn't find spec in the PAC Controller route")
	}

	host, ok := spec["host"].(string)
	if !ok {
		// this condition is satisfied if there's no metadata at all in the provided CR
		return "", fmt.Errorf("couldn't find spec.host in the PAC controller route")
	}

	return fmt.Sprintf("https://%s", host), nil
}

func detectSelfSignedCertificate(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Sprintf("invalid url?? %s", url)
	}

	client := http.Client{}
	resp, err := client.Do(req)
	if err != nil && isTLSError(err) {
		return "⚠️ your controller route is using a self signed certificate\n⚠️ make sure you allow to connect to self signed url in your github app setting."
	} else if err != nil {
		return fmt.Sprintf("⚠️ could not connect to the route %s, make sure the pipelines-as-code controller is running", url)
	}
	if err := resp.Body.Close(); err != nil {
		return fmt.Sprintf("⚠️ could not close the response body: %v", err)
	}
	return ""
}

func isTLSError(err error) bool {
	return errors.As(err, &x509.UnknownAuthorityError{}) || errors.As(err, &x509.CertificateInvalidError{})
}
