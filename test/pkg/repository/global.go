package repository

import (
	"context"
	"os"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateGlobalRepo(ctx context.Context) (context.Context, string, *params.Run, error) {
	runcnx := params.New()
	if err := runcnx.Clients.NewClients(ctx, &runcnx.Info); err != nil {
		return ctx, "", nil, err
	}

	ctx, err := cctx.GetControllerCtxInfo(ctx, runcnx)
	if err != nil {
		return ctx, "", nil, err
	}

	globalNS := info.GetNS(ctx)

	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: info.DefaultGlobalRepoName,
		},
		Spec: v1alpha1.RepositorySpec{
			ConcurrencyLimit: github.Ptr(2),
		},
	}

	if err := CreateRepo(ctx, globalNS, runcnx, repo); err != nil {
		return ctx, "", nil, err
	}

	return ctx, globalNS, runcnx, nil
}

func CleanUpGlobalRepo(runcnx *params.Run, globalNS string) error {
	if os.Getenv("TEST_NOCLEANUP") != "true" {
		runcnx.Clients.Log.Infof("Cleaning up global repo %s in %s", info.DefaultGlobalRepoName, globalNS)
		return runcnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(globalNS).Delete(
			context.Background(), info.DefaultGlobalRepoName, metav1.DeleteOptions{})
	}
	return nil
}
