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

// sortPipelineRunsByCompletion sorts the pipeline runs by completion time, then by creation time.
// Note that it converts time to milliseconds to avoid precision issues.
func sortPipelineRunsByCompletionMillis(prs []v1.PipelineRun) {
	sort.Slice(prs, func(i, j int) bool {
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

// sortPipelineRunsByCreationMillis sorts the pipeline runs by creation time.
// Note that it converts time to milliseconds to avoid precision issues.
func sortPipelineRunsByCreationMillis(prs []v1.PipelineRun) {
	sort.Slice(prs, func(i, j int) bool {
		ci := time.UnixMilli(prs[i].CreationTimestamp.UnixMilli())
		cj := time.UnixMilli(prs[j].CreationTimestamp.UnixMilli())
		return ci.Before(cj)
	})
}

type Opts struct {
	RepoName        string
	Namespace       string
	MinNumberStatus int
	PollTimeout     time.Duration
	AdminNS         string
	TargetSHA       []string
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

		clients.Log.Infof("waiting for pipelinerun to be created: selector sha=%v, MinNumberStatus=%d pr.Items=%d", opts.TargetSHA, opts.MinNumberStatus, len(prs.Items))
		if len(prs.Items) == opts.MinNumberStatus {
			matched = prs.Items
			sortPipelineRunsByCreationMillis(matched)
			return true, nil
		}
		return false, nil
	})
}

// UntilPipelineRunsFinished waits until at least MinNumberStatus PipelineRuns
// have reached a terminal state (Succeeded, Failed, or Cancelled).
// Results are sorted by completion time descending (newest first, oldest last).
func UntilPipelineRunsFinished(ctx context.Context, clients clients.Clients, opts Opts) ([]v1.PipelineRun, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.PollTimeout)
	defer cancel()
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
			if cond := pr.Status.GetCondition(apis.ConditionSucceeded); cond != nil && cond.Status != corev1.ConditionUnknown {
				finished = append(finished, pr)
			}
		}

		clients.Log.Infof("still waiting for %d pipelinerun(s) to finish in %s namespace (finished=%d, total=%d)",
			opts.MinNumberStatus, opts.Namespace, len(finished), len(prs.Items))
		if len(finished) >= opts.MinNumberStatus {
			sortPipelineRunsByCompletionMillis(finished)
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

		clients.Log.Infof("still waiting for %d pipelinerun(s) to have reason %s in %s namespace", opts.MinNumberStatus, desiredReason.String(), opts.Namespace)
		if len(prsWithReason) >= opts.MinNumberStatus {
			matched = prsWithReason
			sortPipelineRunsByCreationMillis(matched)
			return true, nil
		}
		return false, nil
	})
}
