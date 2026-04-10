package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	tprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	"knative.dev/pkg/apis"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestCreateStatusWithRetry(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	vcx := tprovider.TestProviderImp{}

	err := createStatusWithRetry(context.TODO(), fakelogger, &vcx, nil, providerstatus.StatusOpts{})
	assert.NilError(t, err)
}

func TestCreateStatusWithRetryErrorCase(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	vcx := tprovider.TestProviderImp{}
	vcx.CreateStatusErorring = true

	// Temporarily override backoffSchedule to speed up the test
	oldBackoffSchedule := backoffSchedule
	backoffSchedule = []time.Duration{time.Millisecond}
	defer func() { backoffSchedule = oldBackoffSchedule }()

	err := createStatusWithRetry(context.TODO(), fakelogger, &vcx, nil, providerstatus.StatusOpts{})
	assert.Error(t, err, "failed to report status: some provider error occurred while reporting status")
}

func TestPostFinalStatus(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	vcx := &tprovider.TestProviderImp{}

	labels := map[string]string{}
	ns := "namespace"
	clock := clockwork.NewFakeClock()
	pr1 := tektontest.MakePRCompletion(clock, "pipeline-newest", ns, tektonv1.PipelineRunReasonSuccessful.String(), nil, labels, 10)
	pr1.Status.Conditions = append(pr1.Status.Conditions, apis.Condition{Status: "False", Message: "Hello not good", Type: "Succeeded", Reason: "CouldntGetTask"})
	ctx, _ := rtesting.SetupFakeContext(t)
	tdata := testclient.Data{PipelineRuns: []*tektonv1.PipelineRun{pr1}}
	stdata, _ := testclient.SeedTestData(t, ctx, tdata)

	run := params.New()
	run.Clients = clients.Clients{
		Kube:   stdata.Kube,
		Tekton: stdata.Pipeline,
	}
	run.Clients.SetConsoleUI(consoleui.FallBackConsole{})

	r := &Reconciler{
		run: run,
	}
	pacInfo := &info.PacOpts{
		Settings: settings.Settings{
			ErrorLogSnippet: false,
		},
	}

	_, _, err := r.postFinalStatus(ctx, fakelogger, pacInfo, vcx, info.NewEvent(), pr1)
	assert.NilError(t, err)
}
