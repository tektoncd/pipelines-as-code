package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/adapter"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	evadapter "knative.dev/eventing/pkg/adapter/v2"
	"knative.dev/pkg/client/injection/kube/client"
	"knative.dev/pkg/injection/sharedmain"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/signals"
	"knative.dev/pkg/system"
)

const (
	PACControllerLogKey = "pipelinesascode"
)

func main() {
	ctx := signals.NewContext()
	run := params.New()

	err := run.Clients.NewClients(ctx, &run.Info)
	if err != nil {
		log.Fatal("failed to init clients : ", err)
	}

	kinteract, err := kubeinteraction.NewKubernetesInteraction(run)
	if err != nil {
		log.Fatal("failed to init kinit client : ", err)
	}

	loggerConfiguratorOpt := evadapter.WithLoggerConfiguratorConfigMapName(logging.ConfigMapName())
	loggerConfigurator := evadapter.NewLoggerConfiguratorFromConfigMap(PACControllerLogKey, loggerConfiguratorOpt)
	copts := []evadapter.ConfiguratorOption{
		evadapter.WithLoggerConfigurator(loggerConfigurator),
		evadapter.WithObservabilityConfigurator(evadapter.NewObservabilityConfiguratorFromConfigMap()),
		evadapter.WithCloudEventsStatusReporterConfigurator(evadapter.NewCloudEventsReporterConfiguratorFromConfigMap()),
	}
	// put logger configurator to ctx
	ctx = evadapter.WithConfiguratorOptions(ctx, copts)

	ctx = info.StoreNS(ctx, system.Namespace())
	ctx = info.StoreCurrentControllerName(ctx, run.Info.Controller.Name)

	if val, ok := os.LookupEnv("PAC_DISABLE_HEALTH_PROBE"); ok {
		if strings.ToLower(val) == "true" {
			ctx = sharedmain.WithHealthProbesDisabled(ctx)
		}
	}

	ctx = context.WithValue(ctx, client.Key{}, run.Clients.Kube)
	ctx = evadapter.WithNamespace(ctx, system.Namespace())
	ctx = evadapter.WithConfigWatcherEnabled(ctx)
	evadapter.MainWithContext(ctx, PACControllerLogKey, adapter.NewEnvConfig, adapter.New(run, kinteract))
}
