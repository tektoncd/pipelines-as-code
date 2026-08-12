package consoleui

import (
	"context"
	"fmt"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	openShiftConsoleNS                = "openshift-console"
	openShiftConsoleRouteName         = "console"
	openShiftPipelineNamespaceViewURL = "%s/pipelines/ns/%s/pipeline-runs"
	openShiftPipelineDetailViewURL    = "%s/k8s/ns/%s/tekton.dev~v1~PipelineRun/%s"
	openShiftPipelineTaskLogURL       = "%s/logs/%s"
	openShiftRouteGroup               = "route.openshift.io"
	openShiftRouteVersion             = "v1"
	openShiftRouteResource            = "routes"
	openshiftConsoleName              = "OpenShift Console"
)

type OpenshiftConsole struct {
	host string
}

func (o *OpenshiftConsole) SetParams(_ map[string]string) {
}

func (o *OpenshiftConsole) GetName() string {
	return openshiftConsoleName
}

func (o *OpenshiftConsole) URL() string {
	if o.host == "" {
		return "https://openshift.url." + NotConfiguredURLSuffix
	}
	return "https://" + o.host
}

func (o *OpenshiftConsole) DetailURL(pr *tektonv1.PipelineRun) string {
	return fmt.Sprintf(openShiftPipelineDetailViewURL, o.URL(), pr.GetNamespace(), pr.GetName())
}

func (o *OpenshiftConsole) TaskLogURL(pr *tektonv1.PipelineRun, taskRunStatus *tektonv1.PipelineRunTaskRunStatus) string {
	return fmt.Sprintf(openShiftPipelineTaskLogURL, o.DetailURL(pr), taskRunStatus.PipelineTaskName)
}

func (o *OpenshiftConsole) NamespaceURL(pr *tektonv1.PipelineRun) string {
	return fmt.Sprintf(openShiftPipelineNamespaceViewURL, o.URL(), pr.GetNamespace())
}

// UI use dynamic client to get the route of the openshift
// console where we can point to.
func (o *OpenshiftConsole) UI(ctx context.Context, kdyn dynamic.Interface) error {
	gvr := schema.GroupVersionResource{
		Group: openShiftRouteGroup, Version: openShiftRouteVersion, Resource: openShiftRouteResource,
	}

	route, err := kdyn.Resource(gvr).Namespace(openShiftConsoleNS).Get(ctx, openShiftConsoleRouteName,
		metav1.GetOptions{})
	if err != nil {
		return err
	}

	spec, ok := route.Object["spec"].(map[string]any)
	if !ok {
		// this condition is satisfied if there's no metadata at all in the provided CR
		return fmt.Errorf("couldn't find spec in the OpenShift Console route")
	}

	if o.host, ok = spec["host"].(string); !ok {
		// this condition is satisfied if there's no metadata at all in the provided CR
		return fmt.Errorf("couldn't find spec.host in the OpenShift Console route")
	}

	return nil
}
