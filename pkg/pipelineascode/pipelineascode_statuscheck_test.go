package pipelineascode

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	providerstatus "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/status"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

type statusCapturingProvider struct {
	testprovider.TestProviderImp
	mu       sync.Mutex
	statuses []providerstatus.StatusOpts
}

func (s *statusCapturingProvider) CreateStatus(_ context.Context, _ *info.Event, opts providerstatus.StatusOpts) error {
	if s.CreateStatusErorring {
		return fmt.Errorf("some provider error occurred while reporting status")
	}
	s.mu.Lock()
	s.statuses = append(s.statuses, opts)
	s.mu.Unlock()
	return nil
}

func TestReportStatusCheckFromRepoSettings(t *testing.T) {
	tests := []struct {
		name                 string
		noMatchConclusion    string
		unmatchedPRs         []*tektonv1.PipelineRun
		createStatusErorring bool
		expectedConclusion   providerstatus.Conclusion
		expectedStatusCount  int
		expectedLogSnippet   string
	}{
		{
			name:              "default conclusion is skipped when not set",
			noMatchConclusion: "",
			unmatchedPRs: []*tektonv1.PipelineRun{
				{ObjectMeta: metav1.ObjectMeta{GenerateName: "pr-one-"}},
			},
			expectedConclusion:  providerstatus.ConclusionSkipped,
			expectedStatusCount: 1,
		},
		{
			name:              "custom conclusion success",
			noMatchConclusion: "success",
			unmatchedPRs: []*tektonv1.PipelineRun{
				{ObjectMeta: metav1.ObjectMeta{GenerateName: "pr-one-"}},
			},
			expectedConclusion:  providerstatus.ConclusionSuccess,
			expectedStatusCount: 1,
		},
		{
			name:              "custom conclusion neutral",
			noMatchConclusion: "neutral",
			unmatchedPRs: []*tektonv1.PipelineRun{
				{ObjectMeta: metav1.ObjectMeta{Name: "pr-with-name"}},
			},
			expectedConclusion:  providerstatus.ConclusionNeutral,
			expectedStatusCount: 1,
		},
		{
			name:              "multiple unmatched pipelineruns",
			noMatchConclusion: "skipped",
			unmatchedPRs: []*tektonv1.PipelineRun{
				{ObjectMeta: metav1.ObjectMeta{GenerateName: "pr-one-"}},
				{ObjectMeta: metav1.ObjectMeta{GenerateName: "pr-two-"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pr-three"}},
			},
			expectedConclusion:  providerstatus.ConclusionSkipped,
			expectedStatusCount: 3,
		},
		{
			name:                "empty unmatched pipelineruns",
			noMatchConclusion:   "skipped",
			unmatchedPRs:        []*tektonv1.PipelineRun{},
			expectedStatusCount: 0,
		},
		{
			name:              "uses name over generateName",
			noMatchConclusion: "skipped",
			unmatchedPRs: []*tektonv1.PipelineRun{
				{ObjectMeta: metav1.ObjectMeta{Name: "my-named-pr", GenerateName: "should-not-use-"}},
			},
			expectedConclusion:  providerstatus.ConclusionSkipped,
			expectedStatusCount: 1,
		},
		{
			name:                 "create status error emits log message",
			noMatchConclusion:    "skipped",
			createStatusErorring: true,
			unmatchedPRs: []*tektonv1.PipelineRun{
				{ObjectMeta: metav1.ObjectMeta{GenerateName: "pr-fail-"}},
			},
			expectedStatusCount: 0,
			expectedLogSnippet:  "error reporting status check from repo settings",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observerCore, logCatcher := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observerCore).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})

			vcx := &statusCapturingProvider{}
			vcx.CreateStatusErorring = tt.createStatusErorring

			event := &info.Event{
				TriggerTarget: triggertype.PullRequest,
			}

			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						StatusChecks: &v1alpha1.StatusChecks{
							Enabled:             true,
							Mode:                v1alpha1.StatusCheckModePerPipelineRun,
							UnmatchedConclusion: tt.noMatchConclusion,
						},
					},
				},
			}

			run := &params.Run{
				Clients: clients.Clients{},
			}
			run.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			p := &PacRun{
				event:        event,
				vcx:          vcx,
				run:          run,
				logger:       logger,
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}

			p.reportStatusCheckFromRepoSettings(ctx, repo, tt.unmatchedPRs, "")

			assert.Equal(t, len(vcx.statuses), tt.expectedStatusCount)

			for _, s := range vcx.statuses {
				assert.Equal(t, s.Status, CompletedStatus)
				assert.Equal(t, s.Conclusion, tt.expectedConclusion)
				assert.Assert(t, s.PipelineRun != nil)
				assert.Assert(t, s.PipelineRunName != "")
				assert.Equal(t, s.PipelineRunName, s.OriginalPipelineRunName)
			}

			if tt.name == "uses name over generateName" && len(vcx.statuses) > 0 {
				assert.Equal(t, vcx.statuses[0].PipelineRunName, "my-named-pr")
			}

			if tt.expectedLogSnippet != "" {
				assert.Assert(t, logCatcher.FilterMessageSnippet(tt.expectedLogSnippet).Len() > 0, logCatcher.All())
			}
		})
	}
}
