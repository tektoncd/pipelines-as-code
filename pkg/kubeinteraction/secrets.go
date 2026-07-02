package kubeinteraction

import (
	"context"
	"fmt"

	ktypes "github.com/openshift-pipelines/pipelines-as-code/pkg/secrets/types"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

func (k Interaction) GetSecret(ctx context.Context, secretopt ktypes.GetSecretOpt) (string, error) {
	secret, err := k.Run.Clients.Kube.CoreV1().Secrets(secretopt.Namespace).Get(
		ctx, secretopt.Name, metav1.GetOptions{},
	)
	if err != nil {
		return "", err
	}
	return string(secret.Data[secretopt.Key]), nil
}

// DeleteSecret deletes the secret created for git-clone basic-auth.
func (k Interaction) DeleteSecret(ctx context.Context, _ *zap.SugaredLogger, targetNamespace, secretName string) error {
	err := k.Run.Clients.Kube.CoreV1().Secrets(targetNamespace).Delete(ctx, secretName, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return err
	}
	return nil
}

// UpdateSecretWithOwnerRef updates the secret with ownerReference.
func (k Interaction) UpdateSecretWithOwnerRef(ctx context.Context, logger *zap.SugaredLogger, targetNamespace, secretName string, pr *pipelinev1.PipelineRun) error {
	controllerOwned := false
	ownerRef := &metav1.OwnerReference{
		APIVersion:         pr.GetGroupVersionKind().GroupVersion().String(),
		Kind:               pr.GetGroupVersionKind().Kind,
		Name:               pr.GetName(),
		UID:                pr.GetUID(),
		BlockOwnerDeletion: &controllerOwned,
		Controller:         &controllerOwned,
	}
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		secret, err := k.Run.Clients.Kube.CoreV1().Secrets(targetNamespace).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		secret.OwnerReferences = []metav1.OwnerReference{*ownerRef}

		_, err = k.Run.Clients.Kube.CoreV1().Secrets(targetNamespace).Update(ctx, secret, metav1.UpdateOptions{})
		if err != nil {
			logger.Infof("failed to update secret, retrying  %v/%v: %v", targetNamespace, secretName, err)
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to update secret with ownerRef %v/%v: %w", targetNamespace, secretName, err)
	}
	return nil
}

func (k Interaction) CreateSecret(ctx context.Context, ns string, secret *corev1.Secret) error {
	_, err := k.Run.Clients.Kube.CoreV1().Secrets(ns).Create(ctx, secret, metav1.CreateOptions{})
	return err
}
