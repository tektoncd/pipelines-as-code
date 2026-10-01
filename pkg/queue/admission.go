package queue

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/types"
)

type AdmissionMode int

const (
	AdmissionInitial AdmissionMode = iota
	AdmissionResume
	AdmissionPromotion
)

type StartOutcome int

const (
	StartRetry StartOutcome = iota
	StartSucceeded
	StartGone
)

// StartCandidate is a worker's private copy. Attempted retries retain their
// reservation even when a read still says Pending: a lost write can commit later.
type StartCandidate struct {
	Key       string
	UID       types.UID
	Attempted bool
	Outcome   StartOutcome
	reserved  bool
}

type admissionState struct {
	owner         string
	promotion     bool
	busy          bool
	done          bool
	releasedOwner bool
	candidates    []StartCandidate
}

// Admission leases one owner's unfinished work to one worker. FinishAdmission
// must be called even on cancellation; it atomically records every batch result.
type Admission struct {
	Candidates []StartCandidate
	repoKey    string
	state      *admissionState
}

func admissionOwner(pr *tektonv1.PipelineRun) string {
	return PrKey(pr) + "/" + string(pr.UID)
}

// Finishing reports whether a PipelineRun has finished or been asked to stop.
// Both graceful modes need their own check: they leave the run executing its
// finally tasks, so IsDone and IsCancelled stay false throughout.
func Finishing(pr *tektonv1.PipelineRun) bool {
	return pr.DeletionTimestamp != nil ||
		pr.IsDone() || pr.IsCancelled() ||
		pr.IsGracefullyCancelled() || pr.IsGracefullyStopped()
}

func (qm *Manager) acquireUnowned(repoKey string, sema Semaphore) string {
	if qm.claims[repoKey][sema.nextPending()] != nil {
		return ""
	}
	return sema.acquireLatest()
}

func (qm *Manager) forgetCandidate(repoKey, key string) {
	if state := qm.claims[repoKey][key]; state != nil {
		state.candidates = slices.DeleteFunc(state.candidates, func(c StartCandidate) bool { return c.Key == key })
		delete(qm.claims[repoKey], key)
		if !state.promotion && !state.busy && !state.releasedOwner && len(state.candidates) == 0 {
			delete(qm.admissions[repoKey], state.owner)
		}
	}
}

func (qm *Manager) ForgetAdmission(repoKey string, owner *tektonv1.PipelineRun) {
	qm.lock.Lock()
	defer qm.lock.Unlock()
	key := admissionOwner(owner)
	// Unfinished work must be resumed, not forgotten when its owner completes.
	if state := qm.admissions[repoKey][key]; state != nil && !state.busy && len(state.candidates) == 0 {
		delete(qm.admissions[repoKey], key)
	}
}

func (qm *Manager) AcquireAdmission(repo *v1alpha1.Repository, owner *tektonv1.PipelineRun, list []string, mode AdmissionMode) (*Admission, error) {
	qm.lock.Lock()
	defer qm.lock.Unlock()
	repoKey, ownerKey := RepoKey(repo), admissionOwner(owner)
	state := qm.admissions[repoKey][ownerKey]
	if mode == AdmissionResume && (state == nil || state.promotion) {
		return nil, nil
	}
	if state != nil && state.busy {
		return nil, fmt.Errorf("queue admission for %s is already being processed", ownerKey)
	}
	sema, err := qm.getSemaphore(repo)
	if err != nil {
		return nil, err
	}
	if state == nil {
		if mode == AdmissionPromotion {
			// An absent owner cannot hand off twice after a successful finalizer
			// or terminal write. Unfinished handoffs have their own record below.
			if !slices.Contains(sema.getCurrentRunning(), PrKey(owner)) && !slices.Contains(sema.getCurrentPending(), PrKey(owner)) {
				return nil, nil
			}
			qm.removeFromQueue(repoKey, PrKey(owner))
		}
		state = &admissionState{owner: ownerKey, promotion: mode == AdmissionPromotion}
		if qm.admissions[repoKey] == nil {
			qm.admissions[repoKey] = make(map[string]*admissionState)
			qm.claims[repoKey] = make(map[string]*admissionState)
		}
		qm.admissions[repoKey][ownerKey] = state
	}
	claims := qm.claims[repoKey]
	if claims == nil {
		return nil, fmt.Errorf("missing admission claims for repository %s", repoKey)
	}
	if state.promotion != (mode == AdmissionPromotion) {
		return nil, fmt.Errorf("unfinished admission for %s must be resumed before promotion", ownerKey)
	}
	if mode == AdmissionResume && Finishing(owner) &&
		(slices.Contains(sema.getCurrentRunning(), PrKey(owner)) || slices.Contains(sema.getCurrentPending(), PrKey(owner))) {
		// A completed batch owner must not hold the capacity its retries need,
		// particularly after the concurrency limit has been reduced.
		state.releasedOwner = true
		qm.removeFromQueue(repoKey, PrKey(owner))
		if len(state.candidates) == 0 {
			state.done = true
		}
	}
	for _, key := range list {
		sema.addToQueue(key, time.Now())
	}
	for i := range state.candidates {
		candidate := &state.candidates[i]
		if !candidate.reserved && sema.nextPending() == candidate.Key && sema.acquireLatest() != "" {
			candidate.reserved = true
		}
	}
	if len(state.candidates) == 0 && !state.done {
		limit := sema.getLimit()
		if state.promotion {
			limit = 1
		}
		for limit == 0 || len(state.candidates) < limit {
			if claims[sema.nextPending()] != nil {
				if len(state.candidates) == 0 {
					return nil, fmt.Errorf("next queue candidate is owned by another admission")
				}
				break
			}
			next := sema.acquireLatest()
			if next == "" {
				break
			}
			state.candidates = append(state.candidates, StartCandidate{Key: next, reserved: true})
			claims[next] = state
		}
		if len(state.candidates) == 0 {
			state.done = true
		}
	}
	candidates := make([]StartCandidate, 0, len(state.candidates))
	for _, candidate := range state.candidates {
		if candidate.reserved {
			candidates = append(candidates, candidate)
		}
	}
	if len(state.candidates) != 0 && len(candidates) == 0 {
		return nil, fmt.Errorf("waiting to resume queue candidates for %s", ownerKey)
	}
	state.busy = true
	return &Admission{Candidates: candidates, repoKey: repoKey, state: state}, nil
}

func (qm *Manager) FinishAdmission(work *Admission) error {
	qm.lock.Lock()
	defer qm.lock.Unlock()
	state := work.state
	if qm.admissions[work.repoKey][state.owner] != state {
		return nil // Repository removal or reconstruction invalidated this lease.
	}
	defer func() { state.busy = false }()
	sema := qm.queueMap[work.repoKey]
	if sema == nil {
		return fmt.Errorf("missing queue while finishing admission in repository %s", work.repoKey)
	}
	var errs []error
	for _, candidate := range work.Candidates {
		if qm.claims[work.repoKey][candidate.Key] != state {
			continue // Completion/deletion already removed this candidate.
		}
		switch candidate.Outcome {
		case StartSucceeded:
			qm.forgetCandidate(work.repoKey, candidate.Key)
			state.done = true
		case StartGone:
			qm.forgetCandidate(work.repoKey, candidate.Key)
			qm.removeFromQueue(work.repoKey, candidate.Key)
		default:
			if !candidate.Attempted {
				if !sema.requeue(candidate.Key) {
					errs = append(errs, fmt.Errorf("cannot restore queue candidate %s", candidate.Key))
					continue
				}
				candidate.reserved = false
			}
			if i := slices.IndexFunc(state.candidates, func(c StartCandidate) bool { return c.Key == candidate.Key }); i >= 0 {
				state.candidates[i] = candidate
			}
		}
	}
	if !state.promotion && len(state.candidates) == 0 {
		if state.releasedOwner {
			state.promotion = true
			state.done = false
		} else if state.done {
			delete(qm.admissions[work.repoKey], state.owner)
		}
	}
	if len(state.candidates) != 0 {
		errs = append(errs, fmt.Errorf("unfinished queue admission for %s", state.owner))
	}
	return errors.Join(errs...)
}
