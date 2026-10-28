package repository

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tektoncd/pipelines-as-code/pkg/params/clients"
)

func UpdateRepo(ctx context.Context, repoName, targetNs string, clients clients.Clients) error {
	repo, err := clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(targetNs).Get(ctx, repoName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	repo.Spec.Settings = nil
	if _, err := clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(targetNs).Update(ctx, repo, metav1.UpdateOptions{}); err != nil {
		return err
	}
	return nil
}
