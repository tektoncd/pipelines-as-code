package secret

import (
	"context"

	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Create(ctx context.Context, runcnx *params.Run, secretData map[string]string, targetNamespace, secretName string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:   secretName,
			Labels: map[string]string{"app.kubernetes.io/managed-by": pipelinesascode.GroupName},
		},
	}
	secret.StringData = secretData
	_, err := runcnx.Clients.Kube.CoreV1().Secrets(targetNamespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}
