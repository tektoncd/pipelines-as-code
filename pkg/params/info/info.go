package info

import (
	"sync"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"go.uber.org/zap"
)

type Info struct {
	pacMutex   *sync.Mutex
	Pac        *PacOpts
	Kube       *KubeOpts
	Controller *ControllerInfo
}

func NewInfo() Info {
	return Info{
		pacMutex:   &sync.Mutex{},
		Pac:        NewPacOpts(),
		Kube:       &KubeOpts{},
		Controller: GetControllerInfoFromEnvOrDefault(),
	}
}

func (i *Info) InitInfo() {
	i.pacMutex = &sync.Mutex{}
}

func (i *Info) GetPacOpts() PacOpts {
	if i.pacMutex == nil {
		i.pacMutex = &sync.Mutex{}
	}
	i.pacMutex.Lock()
	defer i.pacMutex.Unlock()
	return *i.Pac
}

func (i *Info) UpdatePacOpts(logger *zap.SugaredLogger, configData map[string]string) (*settings.Settings, error) {
	if i.pacMutex == nil {
		i.pacMutex = &sync.Mutex{}
	}
	i.pacMutex.Lock()
	defer i.pacMutex.Unlock()

	if err := settings.SyncConfig(logger, &i.Pac.Settings, configData, settings.DefaultValidators()); err != nil {
		return nil, err
	}
	return &i.Pac.Settings, nil
}

func (i *Info) DeepCopy(out *Info) {
	*out = *i
}

type (
	contextKey string
)
