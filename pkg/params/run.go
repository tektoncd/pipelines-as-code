package params

import (
	"context"
	"fmt"
	"os"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Run struct {
	Clients clients.Clients
	Info    info.Info
}

func (r *Run) UpdatePacConfig(ctx context.Context) error {
	ns := info.GetNS(ctx)
	if ns == "" {
		return fmt.Errorf("failed to find namespace")
	}

	// TODO: move this to kubeinteractions class so we can add unittests.
	cfg, err := r.Clients.Kube.CoreV1().ConfigMaps(ns).Get(ctx, r.Info.Controller.Configmap, metav1.GetOptions{})
	if err != nil {
		return err
	}

	updatedPacInfo, err := r.Info.UpdatePacOpts(r.Clients.Log, cfg.Data, &r.Clients.HTTP)
	if err != nil {
		return err
	}

	if updatedPacInfo.TektonDashboardURL != "" && updatedPacInfo.TektonDashboardURL != r.Clients.ConsoleUI().URL() {
		r.Clients.Log.Infof("updating console url to: %s", updatedPacInfo.TektonDashboardURL)
		r.Clients.SetConsoleUI(&consoleui.TektonDashboard{BaseURL: updatedPacInfo.TektonDashboardURL})
	}
	if os.Getenv("PAC_TEKTON_DASHBOARD_URL") != "" {
		r.Clients.Log.Infof("using tekton dashboard url on: %s", os.Getenv("PAC_TEKTON_DASHBOARD_URL"))
		r.Clients.SetConsoleUI(&consoleui.TektonDashboard{BaseURL: os.Getenv("PAC_TEKTON_DASHBOARD_URL")})
	}
	if updatedPacInfo.CustomConsoleURL != "" {
		r.Clients.Log.Infof("updating console url to: %s", updatedPacInfo.CustomConsoleURL)
		pacInfo := r.Info.GetPacOpts()
		r.Clients.SetConsoleUI(consoleui.NewCustomConsole(&pacInfo))
	}

	// This is the case when reverted settings for CustomConsole and TektonDashboard then URL should point to OpenshiftConsole for Openshift platform
	if updatedPacInfo.CustomConsoleURL == "" &&
		(updatedPacInfo.TektonDashboardURL == "" && os.Getenv("PAC_TEKTON_DASHBOARD_URL") == "") {
		r.Clients.SetConsoleUI(consoleui.New(ctx, r.Clients.Dynamic, &r.Info))
	}

	return nil
}

func New() *Run {
	return &Run{
		Info: info.NewInfo(),
	}
}
