package cctx

import (
	"context"
	"fmt"

	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
)

func GetControllerCtxInfo(ctx context.Context, run *params.Run) (context.Context, error) {
	ns, _, err := params.GetInstallLocation(ctx, run)
	if err != nil {
		return nil, fmt.Errorf("error looking for your pipelines-as-code-installation: %w", err)
	}
	if ns == "" {
		return nil, fmt.Errorf("cannot find your pipelines-as-code installation, check that it is installed and you have access")
	}
	run.Clients.Log.Infof("Found pipelines-as-code installation in namespace %s", ns)
	ctx = info.StoreNS(ctx, ns)
	run.Info.Controller = info.GetControllerInfoFromEnvOrDefault()
	run.Clients.Log.Infof("Pipelines as Code Controller: %+v", run.Info.Controller)
	ctx = info.StoreCurrentControllerName(ctx, run.Info.Controller.Name)
	return ctx, nil
}
