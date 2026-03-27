package kubeinteraction

import (
	"context"

	"github.com/google/go-github/v84/github"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (k Interaction) GetEvents(ctx context.Context, ns, objtype, name string) (*corev1.EventList, error) {
	kclient := k.Run.Clients.Kube.CoreV1()
	selector := kclient.Events(ns).GetFieldSelector(github.Ptr(name), github.Ptr(ns), github.Ptr(objtype), nil)
	events, err := kclient.Events(ns).List(ctx, metav1.ListOptions{FieldSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	return events, nil
}
