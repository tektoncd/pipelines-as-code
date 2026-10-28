package kubeinteraction

import (
	"context"

	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	ktypes "github.com/tektoncd/pipelines-as-code/pkg/secrets/types"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
)

type Interface interface {
	CleanupPipelines(context.Context, *zap.SugaredLogger, *v1alpha1.Repository, *pipelinev1.PipelineRun, int) error
	CreateSecret(ctx context.Context, ns string, secret *corev1.Secret) error
	DeleteSecret(context.Context, *zap.SugaredLogger, string, string) error
	UpdateSecretWithOwnerRef(context.Context, *zap.SugaredLogger, string, string, *pipelinev1.PipelineRun) error
	GetSecret(context.Context, ktypes.GetSecretOpt) (string, error)
	GetPodLogs(context.Context, string, string, string, int64) (string, error)
}

type Interaction struct {
	Run *params.Run
}

// validate the interface implementation.
var _ Interface = (*Interaction)(nil)

func NewKubernetesInteraction(c *params.Run) (*Interaction, error) {
	return &Interaction{
		Run: c,
	}, nil
}
