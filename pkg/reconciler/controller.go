package reconciler

import (
	"context"
	"path"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/injection/informers/pipelinesascode/v1alpha1/repository"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/informer/transform"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/tracing"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonPipelineRunInformerv1 "github.com/tektoncd/pipeline/pkg/client/injection/informers/pipeline/v1/pipelinerun"
	tektonPipelineRunReconcilerv1 "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1/pipelinerun"
	tektonv1lister "github.com/tektoncd/pipeline/pkg/client/listers/pipeline/v1"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"knative.dev/pkg/configmap"
	"knative.dev/pkg/controller"
	"knative.dev/pkg/kmeta"
	"knative.dev/pkg/logging"
	"knative.dev/pkg/system"
)

func NewController() func(context.Context, configmap.Watcher) *controller.Impl {
	return func(ctx context.Context, _ configmap.Watcher) *controller.Impl {
		ctx = info.StoreNS(ctx, system.Namespace())
		log := logging.FromContext(ctx)

		tp := tracing.New(log)
		// linter false positive: fresh Background is required because outer ctx is cancelled past <-ctx.Done().
		go func() { //nolint:gosec
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := tp.Shutdown(shutdownCtx); err != nil {
				log.Errorw("failed to shut down tracer provider", "error", err)
			}
		}()

		run := params.New()
		err := run.Clients.NewClients(ctx, &run.Info)
		if err != nil {
			log.Fatal("failed to init clients : ", err)
		}

		kinteract, err := kubeinteraction.NewKubernetesInteraction(run)
		if err != nil {
			log.Fatal("failed to init kinit client : ", err)
		}

		// Start pac config syncer
		go params.StartConfigSync(ctx, run)

		repoInformer := repository.Get(ctx)
		if err := repoInformer.Informer().SetTransform(transform.RepositoryForCache); err != nil {
			log.Fatal("failed to set transform on repository informer: ", err)
		}

		pipelineRunInformer := tektonPipelineRunInformerv1.Get(ctx)
		if err := pipelineRunInformer.Informer().SetTransform(transform.PipelineRunForCache); err != nil {
			log.Fatal("failed to set transform on pipelinerun informer: ", err)
		}

		metrics, err := prmetrics.NewRecorder()
		if err != nil {
			log.Fatalf("Failed to create pipeline as code metrics recorder %v", err)
		}

		qm := queuepkg.NewManager(run.Clients.Log)
		queuepkg.RegisterForDebug(qm)

		r := &Reconciler{
			run:               run,
			kinteract:         kinteract,
			pipelineRunLister: pipelineRunInformer.Lister(),
			repoLister:        repoInformer.Lister(),
			qm:                qm,
			metrics:           metrics,
			eventEmitter:      events.NewEventEmitter(run.Clients.Kube, run.Clients.Log),
		}
		impl := tektonPipelineRunReconcilerv1.NewImpl(ctx, r, ctrlOpts())

		if err := r.qm.InitQueues(ctx, run.Clients.Tekton, run.Clients.PipelineAsCode); err != nil {
			log.Fatal("failed to init queues", err)
		}

		if _, err := pipelineRunInformer.Informer().AddEventHandler(controller.HandleAll(checkStateAndEnqueue(impl))); err != nil {
			logging.FromContext(ctx).Panicf("Couldn't register PipelineRun informer event handler: %v", err)
		}

		if _, err := repoInformer.Informer().AddEventHandler(controller.HandleAll(enqueueQueuedPipelineRuns(impl, pipelineRunInformer.Lister(), log))); err != nil {
			logging.FromContext(ctx).Panicf("Couldn't register Repository informer event handler: %v", err)
		}

		return impl
	}
}

// enqueue only the pipelineruns which are in `started` state
// pipelinerun will have a label `pipelinesascode.tekton.dev/state` to describe the state.
func checkStateAndEnqueue(impl *controller.Impl) func(obj any) {
	return func(obj any) {
		object, err := kmeta.DeletionHandlingAccessor(obj)
		if err == nil {
			_, exist := object.GetAnnotations()[keys.State]
			if exist {
				impl.EnqueueKey(types.NamespacedName{Namespace: object.GetNamespace(), Name: object.GetName()})
			}
		}
	}
}

// enqueueQueuedPipelineRuns re-enqueues every queued PipelineRun of a Repository
// whenever that Repository changes. Without this, a concurrency limit that is
// raised, lowered, zeroed or removed only takes effect the next time some
// unrelated PipelineRun event happens to wake the repository up.
//
// Note this only covers the Repository the PipelineRuns belong to. Changing the
// concurrency limit on a *global* Repository does not wake the repositories that
// merge from it, since that would require fanning out across every namespace.
func enqueueQueuedPipelineRuns(impl *controller.Impl, lister tektonv1lister.PipelineRunLister, log *zap.SugaredLogger) func(obj any) {
	return func(obj any) {
		object, err := kmeta.DeletionHandlingAccessor(obj)
		if err != nil {
			return
		}
		selector := labels.SelectorFromSet(labels.Set{
			keys.Repository: formatting.CleanValueKubernetes(object.GetName()),
			keys.State:      kubeinteraction.StateQueued,
		})
		prs, err := lister.PipelineRuns(object.GetNamespace()).List(selector)
		if err != nil {
			log.Errorf("failed to list queued pipelineRuns for repository %s/%s: %v", object.GetNamespace(), object.GetName(), err)
			return
		}
		for _, pr := range prs {
			impl.EnqueueKey(types.NamespacedName{Namespace: pr.GetNamespace(), Name: pr.GetName()})
		}
	}
}

func ctrlOpts() func(impl *controller.Impl) controller.Options {
	return func(_ *controller.Impl) controller.Options {
		return controller.Options{
			FinalizerName: path.Join(pipelinesascode.GroupName, pipelinesascode.FinalizerName),
			// The watcher observes PipelineRun status but never owns it. Skipping
			// generated status sync avoids doing UpdateStatus() calls when
			// informer transforms trim cached status fields.
			SkipStatusUpdates: true,
			PromoteFilterFunc: func(obj any) bool {
				_, exist := obj.(*tektonv1.PipelineRun).GetAnnotations()[keys.State]
				return exist
			},
		}
	}
}
