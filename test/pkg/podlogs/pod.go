package podlogs

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1i "k8s.io/client-go/kubernetes/typed/core/v1"
)

type ControllerLogSource struct {
	LabelSelector string
	ContainerName string
}

func GetControllerLog(ctx context.Context, kclient corev1i.CoreV1Interface, labelselector, containerName string, sinceSeconds *int64) (string, error) {
	ns := info.GetNS(ctx)
	_, err := kclient.Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	numLines := int64(10)
	return GetPodLog(ctx, kclient, ns, labelselector, containerName, &numLines, sinceSeconds)
}

func GetControllerLogByName(
	ctx context.Context,
	kclient corev1i.CoreV1Interface,
	ns, controllerName string,
	lines *int64,
	sinceSeconds *int64,
) (string, ControllerLogSource, error) {
	selectors := controllerLabelSelectors(controllerName)
	containers := controllerContainerNames(controllerName)
	attempts := make([]string, 0, len(selectors)*len(containers))

	for _, selector := range selectors {
		for _, container := range containers {
			output, err := GetPodLog(ctx, kclient, ns, selector, container, lines, sinceSeconds)
			if err == nil {
				return output, ControllerLogSource{
					LabelSelector: selector,
					ContainerName: container,
				}, nil
			}

			attempts = append(attempts, fmt.Sprintf("%s/%s: %v", selector, container, err))
		}
	}

	return "", ControllerLogSource{}, fmt.Errorf(
		"could not get controller logs for controller %q in namespace %q, attempts: %s",
		controllerName, ns, strings.Join(attempts, "; "),
	)
}

func GetPodLog(ctx context.Context, kclient corev1i.CoreV1Interface, ns, labelselector, containerName string, lines, sinceSeconds *int64) (string, error) {
	nsO, err := kclient.Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	items, err := kclient.Pods(nsO.GetName()).List(ctx, metav1.ListOptions{
		LabelSelector: labelselector,
	})
	if err != nil {
		return "", err
	}
	if len(items.Items) == 0 {
		return "", fmt.Errorf("could not match any pod to label selector: %s", labelselector)
	}

	pod, err := findPodWithContainer(items.Items, containerName)
	if err != nil {
		return "", fmt.Errorf("could not find logs for label selector %q: %w", labelselector, err)
	}

	pdOpts := &v1.PodLogOptions{
		Container: containerName,
	}

	if lines != nil {
		pdOpts.TailLines = lines
	}

	if sinceSeconds != nil {
		pdOpts.SinceSeconds = sinceSeconds
	}

	ios, err := kclient.Pods(nsO.GetName()).GetLogs(pod.GetName(), pdOpts).Stream(ctx)
	if err != nil {
		return "", err
	}
	defer ios.Close()
	log, err := io.ReadAll(ios)
	return string(log), err
}

func controllerLabelSelectors(controllerName string) []string {
	selectors := []string{
		fmt.Sprintf("app.kubernetes.io/name=%s", controllerName),
	}

	if controllerName == "controller" {
		selectors = append(selectors, "app.kubernetes.io/name=pipelines-as-code-controller")
	}

	selectors = append(selectors, "app.kubernetes.io/component=controller,app.kubernetes.io/part-of=pipelines-as-code")
	return selectors
}

func controllerContainerNames(controllerName string) []string {
	if controllerName != "controller" {
		return []string{controllerName}
	}

	return []string{"pac-controller", "controller", "pipelines-as-code-controller"}
}

func findPodWithContainer(pods []v1.Pod, containerName string) (*v1.Pod, error) {
	for i := range pods {
		for _, container := range pods[i].Spec.Containers {
			if container.Name == containerName {
				return &pods[i], nil
			}
		}
	}

	return nil, fmt.Errorf(
		"container %q is not present in any pod: %s",
		containerName, describePodsWithContainers(pods),
	)
}

func describePodsWithContainers(pods []v1.Pod) string {
	descriptions := make([]string, 0, len(pods))
	for _, pod := range pods {
		containerNames := make([]string, 0, len(pod.Spec.Containers))
		for _, container := range pod.Spec.Containers {
			containerNames = append(containerNames, container.Name)
		}
		descriptions = append(descriptions, fmt.Sprintf("%s[%s]", pod.Name, strings.Join(containerNames, ",")))
	}
	return strings.Join(descriptions, ";")
}
