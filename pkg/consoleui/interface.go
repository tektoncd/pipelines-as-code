package consoleui

import (
	"context"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/client-go/dynamic"
)

// NotConfiguredURLSuffix is the hostname suffix used by every placeholder
// URL returned by this package when a console/dashboard has not been
// configured (e.g. https://dashboard.is.not.configured). Providers can use
// it to detect and skip sending these placeholders as real URLs.
const NotConfiguredURLSuffix = "is.not.configured"

const consoleIsnotConfiguredURL = "https://dashboard." + NotConfiguredURLSuffix

type Interface interface {
	DetailURL(pr *tektonv1.PipelineRun) string
	TaskLogURL(pr *tektonv1.PipelineRun, taskRunStatusstatus *tektonv1.PipelineRunTaskRunStatus) string
	NamespaceURL(pr *tektonv1.PipelineRun) string
	UI(ctx context.Context, kdyn dynamic.Interface) error
	URL() string
	GetName() string
	SetParams(mt map[string]string)
}

type FallBackConsole struct{}

func (f FallBackConsole) GetName() string {
	return "Not configured"
}

func (f FallBackConsole) DetailURL(_ *tektonv1.PipelineRun) string {
	return consoleIsnotConfiguredURL
}

func (f FallBackConsole) TaskLogURL(_ *tektonv1.PipelineRun, _ *tektonv1.PipelineRunTaskRunStatus) string {
	return consoleIsnotConfiguredURL
}

func (f FallBackConsole) NamespaceURL(_ *tektonv1.PipelineRun) string {
	return consoleIsnotConfiguredURL
}

func (f FallBackConsole) UI(_ context.Context, _ dynamic.Interface) error {
	return nil
}

func (f FallBackConsole) URL() string {
	return consoleIsnotConfiguredURL
}

func (f FallBackConsole) SetParams(_ map[string]string) {
}

func New(ctx context.Context, kdyn dynamic.Interface, _ *info.Info) Interface {
	oc := &OpenshiftConsole{}
	if err := oc.UI(ctx, kdyn); err == nil {
		return oc
	}

	// TODO: Try to detect TektonDashboard somehow by ingress?
	return FallBackConsole{}
}
