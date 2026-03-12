package reconciler

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
)

func (r *Reconciler) emitMetrics(ctx context.Context, pr *tektonv1.PipelineRun) error {
	if err := r.countPipelineRun(ctx, pr); err != nil {
		return err
	}

	if err := r.calculatePRDuration(ctx, pr); err != nil {
		return err
	}

	return nil
}

func (r *Reconciler) countPipelineRun(ctx context.Context, pr *tektonv1.PipelineRun) error {
	gitProvider := pr.GetAnnotations()[keys.GitProvider]
	eventType := pr.GetAnnotations()[keys.EventType]
	repository := pr.GetAnnotations()[keys.Repository]

	switch gitProvider {
	case "github", "github-enterprise":
		if _, ok := pr.GetAnnotations()[keys.InstallationID]; ok {
			gitProvider += "-app"
		} else {
			gitProvider += "-webhook"
		}
	case "gitlab", "gitea", "forgejo", "bitbucket-cloud", "bitbucket-datacenter":
		gitProvider += "-webhook"
	default:
		return fmt.Errorf("no supported Git provider")
	}

	return r.metrics.Count(ctx, gitProvider, eventType, pr.GetNamespace(), repository)
}

func (r *Reconciler) calculatePRDuration(ctx context.Context, pr *tektonv1.PipelineRun) error {
	repository := pr.GetAnnotations()[keys.Repository]
	duration := time.Duration(0)
	if pr.Status.StartTime != nil {
		duration = time.Since(pr.Status.StartTime.Time)
		if pr.Status.CompletionTime != nil {
			duration = pr.Status.CompletionTime.Sub(pr.Status.StartTime.Time)
		}
	}

	cond := pr.Status.GetCondition(apis.ConditionSucceeded)
	status := "success"
	if cond.Status == corev1.ConditionFalse {
		status = "failed"
		if cond.Reason == tektonv1.PipelineRunReasonCancelled.String() {
			status = "cancelled"
		}
	}
	reason := cond.Reason

	return r.metrics.CountPRDuration(ctx, pr.GetNamespace(), repository, status, reason, duration)
}
