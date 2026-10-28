package kubestuff

import (
	"context"
	"testing"
	"time"

	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ScaleDeployment(ctx context.Context, t *testing.T, runcnx *params.Run, replicas int32, deploymentName, namespace string) {
	scale, err := runcnx.Clients.Kube.AppsV1().Deployments(namespace).GetScale(ctx, deploymentName, metav1.GetOptions{})
	assert.NilError(t, err)
	scale.Spec.Replicas = replicas
	time.Sleep(5 * time.Second)
	_, err = runcnx.Clients.Kube.AppsV1().Deployments(namespace).UpdateScale(ctx, deploymentName, scale, metav1.UpdateOptions{})
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Deployment %s in namespace %s scaled to %d replicas", deploymentName, namespace, replicas)
}
