package queue

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/generated/clientset/versioned"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	versioned2 "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"
	"go.uber.org/zap"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"knative.dev/pkg/logging"
)

const (
	creationTimestamp = "{.metadata.creationTimestamp}"
)

type Manager struct {
	queueMap map[string]Semaphore
	lock     *sync.Mutex
	logger   *zap.SugaredLogger
}

func NewManager(logger *zap.SugaredLogger) *Manager {
	return &Manager{
		queueMap: make(map[string]Semaphore),
		lock:     &sync.Mutex{},
		logger:   logger,
	}
}

// getSemaphore returns existing semaphore created for repository or create
// a new one with limit provided in repository
// Semaphore: nothing but a waiting and a running queue for a repository
// with limit deciding how many should be running at a time.
func (qm *Manager) getSemaphore(repo *v1alpha1.Repository) (Semaphore, error) {
	repoKey := RepoKey(repo)

	if sema, found := qm.queueMap[repoKey]; found {
		if err := qm.checkAndUpdateSemaphoreSize(repo, sema); err != nil {
			return nil, err
		}
		return sema, nil
	}

	// create a new semaphore; can't assume callers have checked that ConcurrencyLimit is set
	limit := 0
	if repo.Spec.ConcurrencyLimit != nil {
		limit = *repo.Spec.ConcurrencyLimit
	}
	qm.queueMap[repoKey] = newSemaphore(repoKey, limit)

	return qm.queueMap[repoKey], nil
}

func (qm *Manager) checkAndUpdateSemaphoreSize(repo *v1alpha1.Repository, semaphore Semaphore) error {
	// a repository whose concurrency limit has been removed is treated as unlimited,
	// which the semaphore models as a limit of zero.
	limit := 0
	if repo.Spec.ConcurrencyLimit != nil {
		limit = *repo.Spec.ConcurrencyLimit
	}
	if limit != semaphore.getLimit() {
		if semaphore.resize(limit) {
			return nil
		}
		return fmt.Errorf("failed to resize semaphore")
	}
	return nil
}

// AddListToRunningQueue adds the pipelineRun to the waiting queue of the repository
// and if it is at the top and ready to run which means currently running pipelineRun < limit
// then move it to running queue
// This adds the pipelineRuns in the same order as in the list.
func (qm *Manager) AddListToRunningQueue(repo *v1alpha1.Repository, list []string) ([]string, error) {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	sema, err := qm.getSemaphore(repo)
	if err != nil {
		return []string{}, err
	}

	for _, pr := range list {
		if sema.addToQueue(pr, time.Now()) {
			qm.logger.Infof("added pipelineRun (%s) to running queue for repository (%s)", pr, RepoKey(repo))
		}
	}

	// it is possible something besides PAC set the PipelineRun to Pending; if concurrency limit has not
	// been set, return all the pending PipelineRuns; also, if the limit is zero, that also means do not throttle,
	// so we return all the PipelinesRuns, the for loop below skips that case as well
	if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
		return sema.getCurrentPending(), nil
	}

	acquiredList := []string{}
	for i := 0; i < *repo.Spec.ConcurrencyLimit; i++ {
		acquired := sema.acquireLatest()
		if acquired != "" {
			qm.logger.Infof("moved (%s) to running for repository (%s)", acquired, RepoKey(repo))
			acquiredList = append(acquiredList, acquired)
		}
	}

	return acquiredList, nil
}

func (qm *Manager) AddToPendingQueue(repo *v1alpha1.Repository, list []string) error {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	sema, err := qm.getSemaphore(repo)
	if err != nil {
		return err
	}

	for _, pr := range list {
		if sema.addToPendingQueue(pr, time.Now()) {
			qm.logger.Infof("added pipelineRun (%s) to pending queue for repository (%s)", pr, RepoKey(repo))
		}
	}
	return nil
}

func (qm *Manager) RemoveFromQueue(repoKey, prKey string) bool {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	return qm.removeFromQueue(repoKey, prKey)
}

// removeFromQueue must be called with qm.lock held.
func (qm *Manager) removeFromQueue(repoKey, prKey string) bool {
	sema, found := qm.queueMap[repoKey]
	if !found {
		return false
	}

	sema.release(prKey)
	sema.removeFromQueue(prKey)
	qm.logger.Infof("removed (%s) for repository (%s)", prKey, repoKey)
	return true
}

func (qm *Manager) RemoveAndTakeItemFromQueue(repo *v1alpha1.Repository, run *tektonv1.PipelineRun) string {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	repoKey := RepoKey(repo)
	prKey := PrKey(run)
	if !qm.removeFromQueue(repoKey, prKey) {
		return ""
	}
	sema, found := qm.queueMap[repoKey]
	if !found {
		return ""
	}

	if next := sema.acquireLatest(); next != "" {
		qm.logger.Infof("moved (%s) to running for repository (%s)", next, repoKey)
		return next
	}
	return ""
}

// FilterPipelineRunByInProgress filters the given list of PipelineRun names to only include those
// that are in a "queued" state and have a pending status. It retrieves the PipelineRun objects
// from the Tekton API and checks their annotations and status to determine if they should be included.
//
// Returns A list of PipelineRun names that are in a "queued" state and have a pending status.
func FilterPipelineRunByState(ctx context.Context, tekton versioned2.Interface, orderList []string, wantedStatus, wantedState string) []string {
	orderedList := []string{}
	for _, prName := range orderList {
		// The list comes straight from a user-editable annotation, so a
		// malformed entry must be skipped, not indexed: a panic here takes
		// down the whole watcher, and from InitQueues it does so on every
		// restart.
		namespace, name, ok := SplitPrKey(prName)
		if !ok {
			logging.FromContext(ctx).Warnf("ignoring malformed execution-order entry %q", prName)
			continue
		}
		pr, err := tekton.TektonV1().PipelineRuns(namespace).Get(ctx, name, v1.GetOptions{})
		if err != nil {
			continue
		}

		state, exist := pr.GetAnnotations()[keys.State]
		if !exist {
			continue
		}

		if state == wantedState {
			if wantedStatus != "" && pr.Spec.Status != tektonv1.PipelineRunSpecStatus(wantedStatus) {
				continue
			}
			orderedList = append(orderedList, namespace+"/"+name)
		}
	}
	return orderedList
}

// InitQueues rebuild all the queues for all repository if concurrency is defined before
// reconciler started reconciling them.
func (qm *Manager) InitQueues(ctx context.Context, tekton versioned2.Interface, pac versioned.Interface) error {
	// fetch all repos
	repos, err := pac.PipelinesascodeV1alpha1().Repositories("").List(ctx, v1.ListOptions{})
	if err != nil {
		return err
	}

	// pipelineRuns from the namespace where repository is present
	// those are required for creating queues
	for _, repo := range repos.Items {
		if repo.Spec.ConcurrencyLimit == nil || *repo.Spec.ConcurrencyLimit == 0 {
			continue
		}

		// add all pipelineRuns in started state to pending queue
		prs, err := tekton.TektonV1().PipelineRuns(repo.Namespace).
			List(ctx, v1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s", keys.State, kubeinteraction.StateStarted),
			})
		if err != nil {
			return err
		}

		// sort the pipelinerun by creation time before adding to queue
		sortedPRs := sortPipelineRunsByCreationTimestamp(prs.Items)

		for _, pr := range sortedPRs {
			order, exist := pr.GetAnnotations()[keys.ExecutionOrder]
			if !exist {
				// if the pipelineRun doesn't have an execution order annotation
				// then skip it, but keep initializing the remaining pipelineRuns
				// and repositories.
				continue
			}
			orderedList := FilterPipelineRunByState(ctx, tekton, strings.Split(order, ","), "", kubeinteraction.StateStarted)

			_, err = qm.AddListToRunningQueue(&repo, orderedList)
			if err != nil {
				qm.logger.Error("failed to init queue for repo: ", repo.GetName())
			}
		}

		// now fetch all queued pipelineRun
		prs, err = tekton.TektonV1().PipelineRuns(repo.Namespace).
			List(ctx, v1.ListOptions{
				LabelSelector: fmt.Sprintf("%s=%s", keys.State, kubeinteraction.StateQueued),
			})
		if err != nil {
			return err
		}

		// sort the pipelinerun by creation time before adding to queue
		sortedPRs = sortPipelineRunsByCreationTimestamp(prs.Items)

		for _, pr := range sortedPRs {
			order, exist := pr.GetAnnotations()[keys.ExecutionOrder]
			if !exist {
				// if the pipelineRun doesn't have an execution order annotation
				// then skip it, but keep initializing the remaining pipelineRuns
				// and repositories.
				continue
			}
			orderedList := FilterPipelineRunByState(ctx, tekton, strings.Split(order, ","), tektonv1.PipelineRunSpecStatusPending, kubeinteraction.StateQueued)
			if err := qm.AddToPendingQueue(&repo, orderedList); err != nil {
				qm.logger.Error("failed to init queue for repo: ", repo.GetName())
			}
		}
	}

	return nil
}

func (qm *Manager) RemoveRepository(repo *v1alpha1.Repository) {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	repoKey := RepoKey(repo)
	delete(qm.queueMap, repoKey)
}

func (qm *Manager) QueuedPipelineRuns(repo *v1alpha1.Repository) []string {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	repoKey := RepoKey(repo)
	if sema, ok := qm.queueMap[repoKey]; ok {
		return sema.getCurrentPending()
	}
	return []string{}
}

func (qm *Manager) RunningPipelineRuns(repo *v1alpha1.Repository) []string {
	qm.lock.Lock()
	defer qm.lock.Unlock()

	repoKey := RepoKey(repo)
	if sema, ok := qm.queueMap[repoKey]; ok {
		return sema.getCurrentRunning()
	}
	return []string{}
}

func sortPipelineRunsByCreationTimestamp(prs []tektonv1.PipelineRun) []*tektonv1.PipelineRun {
	runTimeObj := make([]runtime.Object, len(prs))
	for i := range prs {
		runTimeObj[i] = &prs[i]
	}
	sort.ByField(creationTimestamp, runTimeObj)
	sortedPRs := make([]*tektonv1.PipelineRun, len(runTimeObj))
	for i, run := range runTimeObj {
		pr, _ := run.(*tektonv1.PipelineRun)
		sortedPRs[i] = pr
	}
	return sortedPRs
}
