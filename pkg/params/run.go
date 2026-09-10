package params

import (
	"context"
	"fmt"
	"os"

	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	paclisters "github.com/openshift-pipelines/pipelines-as-code/pkg/generated/listers/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

type Run struct {
	Clients          clients.Clients
	Info             info.Info
	RepositoryLister paclisters.RepositoryLister
}

// GetRepository returns the named Repository from ns, reading from the cached
// lister when one is configured and falling back to a live API call otherwise.
// The lister hands out pointers into the shared informer cache, so the result
// is deep-copied to keep callers from mutating cached objects.
func (r *Run) GetRepository(ctx context.Context, ns, name string) (*apipac.Repository, error) {
	if r.RepositoryLister != nil {
		repo, err := r.RepositoryLister.Repositories(ns).Get(name)
		if err != nil {
			return nil, err
		}
		return repo.DeepCopy(), nil
	}
	return r.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(ns).Get(ctx, name, metav1.GetOptions{})
}

// ListRepositories returns the Repositories in ns (all namespaces when ns is
// empty), reading from the cached lister when one is configured and falling
// back to a live API call otherwise. Results are always deep copies, so callers
// may mutate them without touching the informer cache.
func (r *Run) ListRepositories(ctx context.Context, ns string) ([]apipac.Repository, error) {
	if r.RepositoryLister != nil {
		var listed []*apipac.Repository
		var err error
		if ns == "" {
			listed, err = r.RepositoryLister.List(labels.Everything())
		} else {
			listed, err = r.RepositoryLister.Repositories(ns).List(labels.Everything())
		}
		if err != nil {
			return nil, err
		}
		repos := make([]apipac.Repository, 0, len(listed))
		for _, repo := range listed {
			if dc := repo.DeepCopy(); dc != nil {
				repos = append(repos, *dc)
			}
		}
		return repos, nil
	}

	list, err := r.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	return list.Items, nil
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

	updatedPacInfo, err := r.Info.UpdatePacOpts(r.Clients.Log, cfg.Data)
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
