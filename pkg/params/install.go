package params

import (
	"context"
	"fmt"

	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const pipelineAsCodeControllerName = "pipelines-as-code-controller"

func GetInstallLocation(ctx context.Context, run *Run) (string, string, error) {
	for _, ns := range info.InstallNamespaces {
		version := "unknown"
		deployment, err := run.Clients.Kube.AppsV1().Deployments(ns).Get(ctx, pipelineAsCodeControllerName, metav1.GetOptions{})
		if err != nil {
			continue
		}
		if val, ok := deployment.GetLabels()["app.kubernetes.io/version"]; ok {
			version = val
		}
		return ns, version, nil
	}
	return "", "", fmt.Errorf("cannot find your pipelines-as-code installation, check that it is installed and you have access")
}
