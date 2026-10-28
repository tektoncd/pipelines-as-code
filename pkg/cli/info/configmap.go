package info

import (
	"context"

	"github.com/tektoncd/pipelines-as-code/pkg/params"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const infoConfigMap = "pipelines-as-code-info"

type Options struct {
	TargetNamespace string
	ControllerURL   string
	Provider        string
}

func IsGithubAppInstalled(ctx context.Context, run *params.Run, targetNamespace string) bool {
	if _, err := run.Clients.Kube.CoreV1().Secrets(targetNamespace).Get(ctx, info.DefaultPipelinesAscodeSecretName, metav1.GetOptions{}); err != nil {
		return false
	}
	return true
}

func GetPACInfo(ctx context.Context, run *params.Run, targetNamespace string) (*Options, error) {
	cm, err := run.Clients.Kube.CoreV1().ConfigMaps(targetNamespace).Get(ctx, infoConfigMap, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return &Options{
		ControllerURL: cm.Data["controller-url"],
		Provider:      cm.Data["provider"],
	}, nil
}

func UpdateInfoConfigMap(ctx context.Context, run *params.Run, opts *Options) error {
	cm, err := run.Clients.Kube.CoreV1().ConfigMaps(opts.TargetNamespace).Get(ctx, infoConfigMap, metav1.GetOptions{})
	if err != nil {
		return err
	}

	cm.Data["controller-url"] = opts.ControllerURL
	cm.Data["provider"] = opts.Provider

	// the user will have read access to configmap
	// but it might be the case, user is not admin and don't have access to update
	// so don't error out, continue with printing a warning
	_, err = run.Clients.Kube.CoreV1().ConfigMaps(opts.TargetNamespace).Update(ctx, cm, metav1.UpdateOptions{})
	if err != nil {
		run.Clients.Log.Warnf("failed to update pipelines-as-code-info configmap: %v", err)
		return nil
	}
	return nil
}
