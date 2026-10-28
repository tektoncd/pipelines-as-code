package action

import (
	"path"
	"testing"

	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	apipac "github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/tektoncd/pipelines-as-code/pkg/consoleui"
	"github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	tektontest "github.com/tektoncd/pipelines-as-code/pkg/test/tekton"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestPatchPipelineRun(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	testPR := tektontest.MakePRStatus("namespace", "force-me", []pipelinev1.ChildStatusReference{
		tektontest.MakeChildStatusReference("first"),
		tektontest.MakeChildStatusReference("last"),
		tektontest.MakeChildStatusReference("middle"),
	}, nil)

	tdata := testclient.Data{
		PipelineRuns: []*pipelinev1.PipelineRun{testPR},
	}

	stdata, _ := testclient.SeedTestData(t, ctx, tdata)
	fakeClients := clients.Clients{
		Tekton: stdata.Pipeline,
	}
	fakeClients.SetConsoleUI(&consoleui.TektonDashboard{BaseURL: "https://localhost.console"})

	patchedPR, err := PatchPipelineRun(ctx, logger, "log URL", fakeClients.Tekton, testPR, getLogURLMergePatch(fakeClients, testPR))
	assert.NilError(t, err)
	assert.Equal(t, patchedPR.Annotations[path.Join(apipac.GroupName, "log-url")], "https://localhost.console/#/namespaces/namespace/pipelineruns/force-me")
}

func getLogURLMergePatch(clients clients.Clients, pr *pipelinev1.PipelineRun) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				keys.LogURL: clients.ConsoleUI().DetailURL(pr),
			},
		},
	}
}
