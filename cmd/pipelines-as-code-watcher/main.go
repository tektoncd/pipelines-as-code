package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/reconciler"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/tracing"
	"go.uber.org/zap"
	"k8s.io/client-go/rest"
	"knative.dev/pkg/injection"
	"knative.dev/pkg/injection/sharedmain"
	"knative.dev/pkg/signals"
)

const globalProbesPort = "8080"

func main() {
	probesPort := globalProbesPort
	envProbePort := os.Getenv("PAC_WATCHER_PORT")
	if envProbePort != "" {
		probesPort = envProbePort
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})

	c := make(chan struct{})
	go func() {
		log.Println("started goroutine for watcher")
		c <- struct{}{}
		// start the web server on port and accept requests
		log.Printf("Readiness and health check server listening on port %s", probesPort)
		// timeout values same as default one from triggers eventlistener
		// https://github.com/tektoncd/triggers/blame/b5b0ee1249402187d1ceff68e0b9d4e49f2ee957/pkg/sink/initialization.go#L48-L52
		srv := &http.Server{
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 40 * time.Second,
			Addr:         ":" + probesPort,
			Handler:      mux,
		}
		log.Fatal(srv.ListenAndServe())
	}()
	<-c

	// This parses flags.
	cfg := injection.ParseAndGetRESTConfigOrDie()

	if cfg.QPS == 0 {
		cfg.QPS = 2 * rest.DefaultQPS
	}
	if cfg.Burst == 0 {
		cfg.Burst = rest.DefaultBurst
	}

	// multiply by no of controllers being created
	cfg.QPS = 5 * cfg.QPS
	cfg.Burst = 5 * cfg.Burst
	ctx := signals.NewContext()

	// Initialize tracing
	zapLogger, err := zap.NewProduction()
	if err != nil {
		log.Fatal("failed to create logger for tracing: ", err)
	}
	tracerProvider, err := tracing.New(ctx, zapLogger.Sugar())
	if err != nil {
		log.Fatal("failed to init tracing: ", err)
	}
	defer func() {
		if err := tracerProvider.Shutdown(ctx); err != nil {
			zapLogger.Sugar().Warnf("failed to shutdown tracer provider: %v", err)
		}
	}()

	if val, ok := os.LookupEnv("PAC_DISABLE_HA"); ok {
		if strings.ToLower(val) == "true" {
			ctx = sharedmain.WithHADisabled(ctx)
		}
	}

	if val, ok := os.LookupEnv("PAC_DISABLE_HEALTH_PROBE"); ok {
		if strings.ToLower(val) == "true" {
			ctx = sharedmain.WithHealthProbesDisabled(ctx)
		}
	}

	sharedmain.MainWithConfig(ctx, "pac-watcher", cfg, reconciler.NewController())
}
