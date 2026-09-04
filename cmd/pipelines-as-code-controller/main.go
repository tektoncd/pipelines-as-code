package main

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/adapter"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/injection/informers/pipelinesascode/v1alpha1/repository"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/informer/transform"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	evadapter "knative.dev/eventing/pkg/adapter/v2"
	"knative.dev/pkg/client/injection/kube/client"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/injection"
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

	// Set up injection context for informers, same way as the watcher does.
	cfg := injection.ParseAndGetRESTConfigOrDie()
	ctx = controller.WithResyncPeriod(ctx, 10*time.Minute)
	ctx, informers := injection.Default.SetupInformers(ctx, cfg)

	// Register the informer and set the cache transform before starting informers.
	// SetTransform must happen before the informer starts. Trim cached objects
	// the same way the watcher does to keep the cache footprint small.
	repoInformer := repository.Get(ctx)
	if err := repoInformer.Informer().SetTransform(transform.RepositoryForCache); err != nil {
		log.Fatal("failed to set transform on repository informer: ", err)
	}
	run.RepositoryLister = repoInformer.Lister()

	kinteract, err := kubeinteraction.NewKubernetesInteraction(run)
	if err != nil {
		log.Fatal("failed to init kinit client : ", err)
	}

	// Start all informers and wait for cache sync before processing webhooks.
	// controller.StartInformers starts each informer in its own goroutine and waits
	// for all caches to sync, ensuring RepositoryLister has an up-to-date view.
	if err := controller.StartInformers(ctx.Done(), informers...); err != nil {
		log.Fatalf("failed to start and sync informers: %v", err)
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
