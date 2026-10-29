package info

import (
	"context"
	"os"
)

var currentControllerName = contextKey("current-controller-name")

const (
	DefaultPipelinesAscodeSecretName = "pipelines-as-code-secret"

	DefaultPipelinesAscodeConfigmapName = "pipelines-as-code"
	DefaultGlobalRepoName               = "pipelines-as-code"
	defaultControllerLabel              = "default"

	// GHE second controller info defaults for E2E tests.
	DefaultGHESecondControllerLabel     = "ghe-controller"
	DefaultGHESecondControllerSecret    = "ghe-secret"
	DefaultGHESecondControllerConfigmap = "ghe-configmap"
)

var InstallNamespaces = []string{"openshift-pipelines", "pipelines-as-code"}

type ControllerInfo struct {
	Name             string `json:"name"`
	Configmap        string `json:"configmap"`
	Secret           string `json:"secret"`
	GlobalRepository string `json:"gRepo"`
}

// GetControllerInfoFromEnvOrDefault retrieves controller info from the env or use the defaults
// TODO: handles doublons when fallbacking in case there is multiple
// controllers but no env variable.
func GetControllerInfoFromEnvOrDefault() *ControllerInfo {
	controllerlabel, ok := os.LookupEnv("PAC_CONTROLLER_LABEL")
	if !ok {
		controllerlabel = defaultControllerLabel
	}
	controllerSecret, ok := os.LookupEnv("PAC_CONTROLLER_SECRET")
	if !ok {
		controllerSecret = DefaultPipelinesAscodeSecretName
	}
	controllerConfigMap, ok := os.LookupEnv("PAC_CONTROLLER_CONFIGMAP")
	if !ok {
		controllerConfigMap = DefaultPipelinesAscodeConfigmapName
	}
	globalRepo, ok := os.LookupEnv("PAC_CONTROLLER_GLOBAL_REPOSITORY")
	if !ok {
		globalRepo = DefaultGlobalRepoName
	}
	return &ControllerInfo{
		Name:             controllerlabel,
		Secret:           controllerSecret,
		Configmap:        controllerConfigMap,
		GlobalRepository: globalRepo,
	}
}

func GetControllerInfoFromEnvOrDefaultGHE() *ControllerInfo {
	controllerlabel, ok := os.LookupEnv("PAC_CONTROLLER_LABEL")
	if !ok {
		controllerlabel = DefaultGHESecondControllerLabel
	}
	controllerSecret, ok := os.LookupEnv("PAC_CONTROLLER_SECRET")
	if !ok {
		controllerSecret = DefaultGHESecondControllerSecret
	}
	controllerConfigMap, ok := os.LookupEnv("PAC_CONTROLLER_CONFIGMAP")
	if !ok {
		controllerConfigMap = DefaultGHESecondControllerConfigmap
	}
	globalRepo, ok := os.LookupEnv("PAC_CONTROLLER_GLOBAL_REPOSITORY")
	if !ok {
		globalRepo = DefaultGlobalRepoName
	}
	return &ControllerInfo{
		Name:             controllerlabel,
		Secret:           controllerSecret,
		Configmap:        controllerConfigMap,
		GlobalRepository: globalRepo,
	}
}

// StoreCurrentControllerName stores current controller name in the context.
func StoreCurrentControllerName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, currentControllerName, name)
}

// GetCurrentControllerName retrieves current controller name from the context.
func GetCurrentControllerName(ctx context.Context) string {
	if val := ctx.Value(currentControllerName); val != nil {
		if name, ok := val.(string); ok {
			return name
		}
	}
	return ""
}
