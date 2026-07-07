package wait

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

// SortPipelineRunsByCompletion sorts the pipeline runs by completion time, then by creation time.
// Note that it converts time to milliseconds to avoid precision issues.
func SortPipelineRunsByCompletionMillis(prs []v1.PipelineRun) {
	sort.Slice(prs, func(i, j int) bool {
		if prs[i].Status.CompletionTime == nil {
			return false
		}
		if prs[j].Status.CompletionTime == nil {
			return true
		}

		ci := time.UnixMilli(prs[i].Status.CompletionTime.UnixMilli())
		cj := time.UnixMilli(prs[j].Status.CompletionTime.UnixMilli())
		if ci.IsZero() && cj.IsZero() {
			ci = time.UnixMilli(prs[i].Status.StartTime.UnixMilli())
			cj = time.UnixMilli(prs[j].Status.StartTime.UnixMilli())
			return ci.Before(cj)
		}
		if ci.IsZero() {
			return false
		}
		if cj.IsZero() {
			return true
		}
		return ci.Before(cj)
	})
}

// SortPipelineRunsByCreationMillis sorts the pipeline runs by creation time.
// Note that it converts time to milliseconds to avoid precision issues.
func SortPipelineRunsByCreationMillis(prs []v1.PipelineRun) {
	sort.Slice(prs, func(i, j int) bool {
		ci := time.UnixMilli(prs[i].CreationTimestamp.UnixMilli())
		cj := time.UnixMilli(prs[j].CreationTimestamp.UnixMilli())
		return ci.Before(cj)
	})
}

type Opts struct {
	Namespace       string
	MinNumberStatus int
	PollTimeout     time.Duration
	AdminNS         string
	TargetSHA       []string
}

func minNumberStatus(opts Opts) int {
	if opts.MinNumberStatus < 1 {
		return 1
	}
	return opts.MinNumberStatus
}

func shaLabelSelector(shas []string) string {
	switch len(shas) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf("%s=%s", keys.SHA, shas[0])
	default:
		return fmt.Sprintf("%s in (%s)", keys.SHA, strings.Join(shas, ","))
	}
}

func UntilMinPRAppeared(ctx context.Context, clients clients.Clients, opts Opts, minNumber int) error {
	ctx, cancel := context.WithTimeout(ctx, opts.PollTimeout)
	defer cancel()
	return kubeinteraction.PollImmediateWithContext(ctx, opts.PollTimeout, func() (bool, error) {
		listOpts := metav1.ListOptions{}
		if sel := shaLabelSelector(opts.TargetSHA); sel != "" {
			listOpts.LabelSelector = sel
		}
		prs, err := clients.Tekton.TektonV1().PipelineRuns(opts.Namespace).List(ctx, listOpts)
		if err != nil {
			return false, err
		}
		if len(prs.Items) >= minNumber {
			return true, nil
		}
		return false, nil
	})
}

func UntilPipelineRunCreated(ctx context.Context, clients clients.Clients, opts Opts) ([]v1.PipelineRun, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.PollTimeout)
	defer cancel()
	minStatus := minNumberStatus(opts)
	var matched []v1.PipelineRun
	return matched, kubeinteraction.PollImmediateWithContext(ctx, opts.PollTimeout, func() (bool, error) {
		listOpts := metav1.ListOptions{}
		if sel := shaLabelSelector(opts.TargetSHA); sel != "" {
			listOpts.LabelSelector = sel
		}
		prs, err := clients.Tekton.TektonV1().PipelineRuns(opts.Namespace).List(ctx, listOpts)
		if err != nil {
			return true, err
		}

		clients.Log.Infof("waiting for pipelinerun to be created: selector sha=%v, MinNumberStatus=%d pr.Items=%d", opts.TargetSHA, minStatus, len(prs.Items))
		if len(prs.Items) >= minStatus {
			matched = prs.Items
			SortPipelineRunsByCreationMillis(matched)
			return true, nil
		}
		return false, nil
	})
}

// UntilPipelineRunsFinished waits until at least MinNumberStatus PipelineRuns
// have reached a terminal state (Succeeded, Failed, or Cancelled) AND have
// been fully reported by the PaC watcher (state annotation set to completed
// or failed). The watcher patches the state annotation as its last action,
// after all status reporting and annotation patching (log-url, check-run-id,
// ...), so waiting on it guarantees those annotations are present — the same
// ordering the old Repository.Status-based wait provided.
// Results are sorted by completion time ascending (oldest first, newest last).
func UntilPipelineRunsFinished(ctx context.Context, clients clients.Clients, opts Opts) ([]v1.PipelineRun, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.PollTimeout)
	defer cancel()
	minStatus := minNumberStatus(opts)
	var matched []v1.PipelineRun
	return matched, kubeinteraction.PollImmediateWithContext(ctx, opts.PollTimeout, func() (bool, error) {
		listOpts := metav1.ListOptions{}
		if sel := shaLabelSelector(opts.TargetSHA); sel != "" {
			listOpts.LabelSelector = sel
		}
		prs, err := clients.Tekton.TektonV1().PipelineRuns(opts.Namespace).List(ctx, listOpts)
		if err != nil {
			return true, err
		}

		var finished []v1.PipelineRun
		for _, pr := range prs.Items {
			cond := pr.Status.GetCondition(apis.ConditionSucceeded)
			state := pr.GetAnnotations()[keys.State]
			if cond != nil && cond.Status != corev1.ConditionUnknown &&
				(state == kubeinteraction.StateCompleted || state == kubeinteraction.StateFailed) {
				finished = append(finished, pr)
			}
		}

		clients.Log.Infof("still waiting for %d pipelinerun(s) to finish in %s namespace (finished=%d, total=%d)",
			minStatus, opts.Namespace, len(finished), len(prs.Items))
		if len(finished) >= minStatus {
			SortPipelineRunsByCompletionMillis(finished)
			matched = finished
			return true, nil
		}
		return false, nil
	})
}

// UntilPipelineRunHasReason Checks for certain reason of PipelineRuns.
func UntilPipelineRunHasReason(ctx context.Context, clients clients.Clients, desiredReason v1.PipelineRunReason, opts Opts) ([]v1.PipelineRun, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.PollTimeout)
	defer cancel()
	minStatus := minNumberStatus(opts)
	var matched []v1.PipelineRun
	return matched, kubeinteraction.PollImmediateWithContext(ctx, opts.PollTimeout, func() (bool, error) {
		listOpts := metav1.ListOptions{}
		if sel := shaLabelSelector(opts.TargetSHA); sel != "" {
			listOpts.LabelSelector = sel
		}
		prs, err := clients.Tekton.TektonV1().PipelineRuns(opts.Namespace).List(ctx, listOpts)
		if err != nil {
			return true, err
		}

		var prsWithReason []v1.PipelineRun
		for _, pr := range prs.Items {
			if len(pr.Status.Conditions) > 0 && pr.Status.Conditions[0].Reason == desiredReason.String() {
				prsWithReason = append(prsWithReason, pr)
			}
		}

		clients.Log.Infof("still waiting for %d pipelinerun(s) to have reason %s in %s namespace", minStatus, desiredReason.String(), opts.Namespace)
		if len(prsWithReason) >= minStatus {
			matched = prsWithReason
			SortPipelineRunsByCreationMillis(matched)
			return true, nil
		}
		return false, nil
	})
}
