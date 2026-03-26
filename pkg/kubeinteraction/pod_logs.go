package kubeinteraction

import (
	"context"
	"io"

	"github.com/google/go-github/v84/github"
	corev1 "k8s.io/api/core/v1"
)

// GetPodLogs of a ns on a podname and container, tailLines is the number of
// line to tail -1 mean unlimited.
func (k Interaction) GetPodLogs(ctx context.Context, ns, podName, containerName string, tailLines int64) (string, error) {
	kclient := k.Run.Clients.Kube.CoreV1()
	pdOpts := &corev1.PodLogOptions{
		Container: containerName,
	}
	if tailLines > 0 {
		pdOpts.TailLines = github.Ptr(tailLines)
	}
	ios, err := kclient.Pods(ns).GetLogs(podName, pdOpts).Stream(ctx)
	if err != nil {
		return "", err
	}
	log, err := io.ReadAll(ios)
	return string(log), err
}
