package reconciler

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-github/v81/github"
	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	prmetrics "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerunmetrics"
	ghprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/secrets"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	testkubernetestint "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"
	knativeapi "knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

var (
	randomURL          = "https://github.com/random/app"
	finalSuccessStatus = "success"
	finalFailureStatus = "failure"
)

func testSetupGHReplies(t *testing.T, mux *http.ServeMux, runevent *info.Event, checkrunID, finalStatus string) {
	t.Helper()
	mux.HandleFunc(fmt.Sprintf("/repos/%s/%s/check-runs/%s", runevent.Organization, runevent.Repository, checkrunID),
		func(_ http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			created := github.CreateCheckRunOptions{}
			err := json.Unmarshal(body, &created)
			assert.NilError(t, err)
			if created.GetStatus() == "completed" {
				assert.Equal(t, created.GetConclusion(), finalStatus, "we got the status `%s` but we should have get the status `%s`", created.GetConclusion(), finalStatus)
				golden.Assert(t, created.GetOutput().GetText(), strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
			}
		})
}

func TestReconciler_ReconcileKind(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	fakeclient, mux, _, teardown := ghtesthelper.SetupGH()
	defer teardown()

	vcx := &ghprovider.Provider{
		Token: github.Ptr("None"),
	}

	vcx.SetGithubClient(fakeclient)

	tests := []struct {
		name          string
		finalStatus   string
		checkRunID    string
		exitCode      int32
		taskCondition knativeduckv1.Conditions
	}{
		{
			name:        "success pipelinerun",
			checkRunID:  "6566930541",
			finalStatus: finalSuccessStatus,
			exitCode:    0,
			taskCondition: knativeduckv1.Conditions{
				{
					Type:   knativeapi.ConditionSucceeded,
					Status: corev1.ConditionTrue,
					Reason: string(tektonv1.PipelineRunReasonSuccessful),
				},
			},
		},
		{
			name:        "failed pipelinerun",
			finalStatus: finalFailureStatus,
			exitCode:    1,
			checkRunID:  "6566930542",
			taskCondition: knativeduckv1.Conditions{
				{
					Type:   knativeapi.ConditionSucceeded,
					Status: corev1.ConditionFalse,
					Reason: string(tektonv1.PipelineRunReasonFailed),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			clock := clockwork.NewFakeClock()
			statusPR := tektonv1.PipelineRunReasonSuccessful
			if tt.finalStatus == finalFailureStatus {
				statusPR = tektonv1.PipelineRunReasonFailed
			}
			pr := tektontest.MakePRCompletion(clock, "pipeline-newest", "ns", string(statusPR), nil, make(map[string]string), 10)
			pr.Status.ChildReferences = []tektonv1.ChildStatusReference{
				{
					TypeMeta: runtime.TypeMeta{
						Kind: "TaskRun",
					},
					Name:             "task1",
					PipelineTaskName: "task1",
				},
			}

			secretName := secrets.GenerateBasicAuthSecretName()
			ctx = info.StoreCurrentControllerName(ctx, "default")

			pr.Annotations = map[string]string{
				keys.GitAuthSecret:  secretName,
				keys.State:          kubeinteraction.StateCompleted,
				keys.InstallationID: "1234",
				keys.RepoURL:        randomURL,
				keys.Repository:     pr.GetName(),
				keys.OriginalPRName: pr.GetName(),
				keys.CheckRunID:     tt.checkRunID,
				keys.URLOrg:         "random",
				keys.URLRepository:  "app",
			}
			pr.Labels = map[string]string{
				keys.Repository: pr.GetName(),
			}

			testRepo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pr.GetName(),
					Namespace: pr.GetNamespace(),
				},
				Spec: v1alpha1.RepositorySpec{
					URL: randomURL,
				},
			}

			taskStatus := tektonv1.TaskRunStatusFields{
				PodName: "task1",
				Steps: []tektonv1.StepState{
					{
						Name: "step1",
						ContainerState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: tt.exitCode,
							},
						},
					},
				},
			}
			testData := testclient.Data{
				Repositories: []*v1alpha1.Repository{testRepo},
				PipelineRuns: []*tektonv1.PipelineRun{pr},
				TaskRuns: []*tektonv1.TaskRun{
					tektontest.MakeTaskRunCompletion(clock, "task1", "ns", "pipeline-newest",
						map[string]string{}, taskStatus, tt.taskCondition, 10),
				},
				Secret: []*corev1.Secret{
					{
						ObjectMeta: metav1.ObjectMeta{
							Namespace: testRepo.Namespace,
							Name:      secretName,
						},
					},
				},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)

			metrics, err := prmetrics.NewRecorder()
			assert.NilError(t, err)

			r := Reconciler{
				repoLister: informers.Repository.Lister(),
				qm:         queuepkg.NewManager(fakelogger),
				run: &params.Run{
					Clients: clients.Clients{
						PipelineAsCode: stdata.PipelineAsCode,
						Tekton:         stdata.Pipeline,
						Kube:           stdata.Kube,
					},
					Info: info.Info{
						Kube: &info.KubeOpts{},
						Controller: &info.ControllerInfo{
							Secret: secretName,
						},
					},
				},
				pipelineRunLister: stdata.PipelineLister,
				kinteract: &kubeinteraction.Interaction{
					Run: &params.Run{
						Clients: clients.Clients{
							Kube:   stdata.Kube,
							Tekton: stdata.Pipeline,
						},
					},
				},
				metrics: metrics,
			}
			r.run.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			pacInfo := &info.PacOpts{
				Settings: settings.Settings{
					ErrorLogSnippet:    true,
					SecretAutoCreation: true,
				},
			}
			vcx.SetPacInfo(pacInfo)

			event := buildEventFromPipelineRun(pr)
			testSetupGHReplies(t, mux, event, tt.checkRunID, tt.finalStatus)

			_, err = r.reportFinalStatus(ctx, fakelogger, pacInfo, event, pr, vcx)
			assert.NilError(t, err)

			got, err := stdata.Pipeline.TektonV1().PipelineRuns(pr.Namespace).Get(ctx, pr.Name, metav1.GetOptions{})
			assert.NilError(t, err)

			// state must be updated to completed
			assert.Equal(t, got.Annotations[keys.State], kubeinteraction.StateCompleted)
		})
	}
}

func TestUpdatePipelineRunState(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()

	tests := []struct {
		name        string
		pipelineRun *tektonv1.PipelineRun
		state       string
	}{
		{
			name: "queued to started",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
					Annotations: map[string]string{
						keys.State: kubeinteraction.StateQueued,
					},
				},
				Spec: tektonv1.PipelineRunSpec{
					Status: tektonv1.PipelineRunSpecStatusPending,
				},
				Status: tektonv1.PipelineRunStatus{},
			},
			state: kubeinteraction.StateStarted,
		},
		{
			name: "started to completed",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test",
					Annotations: map[string]string{
						keys.State: kubeinteraction.StateStarted,
					},
				},
				Spec:   tektonv1.PipelineRunSpec{},
				Status: tektonv1.PipelineRunStatus{},
			},
			state: kubeinteraction.StateCompleted,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			testData := testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{tt.pipelineRun},
			}
			stdata, _ := testclient.SeedTestData(t, ctx, testData)
			r := &Reconciler{
				run: &params.Run{
					Clients: clients.Clients{
						Tekton: stdata.Pipeline,
					},
				},
			}

			updatedPR, err := r.updatePipelineRunState(ctx, fakelogger, tt.pipelineRun, tt.state)
			assert.NilError(t, err)

			assert.Equal(t, updatedPR.Annotations[keys.State], tt.state)
			assert.Equal(t, updatedPR.Spec.Status, tektonv1.PipelineRunSpecStatus(""))

			// Test SCMReportingPLRStarted annotation for started state
			if tt.state == kubeinteraction.StateStarted {
				scmStarted, exists := updatedPR.GetAnnotations()[keys.SCMReportingPLRStarted]
				assert.Assert(t, exists, "SCMReportingPLRStarted annotation should exist when state is started")
				assert.Equal(t, scmStarted, "true", "SCMReportingPLRStarted should be 'true'")
			} else {
				_, exists := updatedPR.GetAnnotations()[keys.SCMReportingPLRStarted]
				assert.Assert(t, !exists, "SCMReportingPLRStarted annotation should not exist for non-started states")
			}
		})
	}
}

func TestReconcileKind_SCMReportingLogic(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	_, mux, ghTestServerURL, teardown := ghtesthelper.SetupGH()
	defer teardown()

	// Mock status endpoint
	mux.HandleFunc("/repos/random/app/statuses/123afc", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{"state":"pending"}`)
	})

	tests := []struct {
		name                         string
		pipelineRun                  *tektonv1.PipelineRun
		shouldCallUpdateToInProgress bool
		description                  string
	}{
		{
			name: "Running reason without SCMReportingPLRStarted - should call updatePipelineRunToInProgress",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test-pr",
					Annotations: map[string]string{
						keys.State:         kubeinteraction.StateQueued,
						keys.Repository:    "test-repo",
						keys.GitProvider:   "github",
						keys.SHA:           "123afc",
						keys.URLOrg:        "random",
						keys.URLRepository: "app",
						keys.SecretCreated: "true",
					},
				},
				Spec: tektonv1.PipelineRunSpec{},
				Status: tektonv1.PipelineRunStatus{
					Status: knativeduckv1.Status{
						Conditions: knativeduckv1.Conditions{
							{
								Type:   knativeapi.ConditionSucceeded,
								Status: corev1.ConditionUnknown,
								Reason: string(tektonv1.PipelineRunReasonRunning),
							},
						},
					},
				},
			},
			shouldCallUpdateToInProgress: true,
			description:                  "PipelineRun is Running but no SCM reporting done yet",
		},
		{
			name: "Running reason with SCMReportingPLRStarted=true - should NOT call updatePipelineRunToInProgress",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test-pr",
					Annotations: map[string]string{
						keys.State:                  kubeinteraction.StateStarted,
						keys.Repository:             "test-repo",
						keys.SCMReportingPLRStarted: "true",
						keys.GitProvider:            "github",
						keys.SHA:                    "123afc",
						keys.URLOrg:                 "random",
						keys.URLRepository:          "app",
						keys.SecretCreated:          "true",
					},
				},
				Spec: tektonv1.PipelineRunSpec{},
				Status: tektonv1.PipelineRunStatus{
					Status: knativeduckv1.Status{
						Conditions: knativeduckv1.Conditions{
							{
								Type:   knativeapi.ConditionSucceeded,
								Status: corev1.ConditionUnknown,
								Reason: string(tektonv1.PipelineRunReasonRunning),
							},
						},
					},
				},
			},
			shouldCallUpdateToInProgress: false,
			description:                  "PipelineRun is Running and SCM reporting already done",
		},
		{
			name: "Non-Running reason - should NOT call updatePipelineRunToInProgress",
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
					Name:      "test-pr",
					Annotations: map[string]string{
						keys.State:         kubeinteraction.StateQueued,
						keys.Repository:    "test-repo",
						keys.GitProvider:   "github",
						keys.SHA:           "123afc",
						keys.URLOrg:        "random",
						keys.URLRepository: "app",
						keys.SecretCreated: "true",
					},
				},
				Spec: tektonv1.PipelineRunSpec{
					Status: tektonv1.PipelineRunSpecStatusPending,
				},
				Status: tektonv1.PipelineRunStatus{
					Status: knativeduckv1.Status{
						Conditions: knativeduckv1.Conditions{
							{
								Type:   knativeapi.ConditionSucceeded,
								Status: corev1.ConditionUnknown,
								Reason: string(tektonv1.PipelineRunReasonPending),
							},
						},
					},
				},
			},
			shouldCallUpdateToInProgress: false,
			description:                  "PipelineRun is not in Running state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			testRepo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: tt.pipelineRun.GetNamespace(),
				},
				Spec: v1alpha1.RepositorySpec{
					URL: randomURL,
					GitProvider: &v1alpha1.GitProvider{
						URL: ghTestServerURL,
						Secret: &v1alpha1.Secret{
							Name: "pac-git-basic-auth-owner-repo",
						},
					},
				},
			}

			testData := testclient.Data{
				Repositories: []*v1alpha1.Repository{testRepo},
				PipelineRuns: []*tektonv1.PipelineRun{tt.pipelineRun},
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)

			// Track if updatePipelineRunToInProgress was called by checking state changes
			originalState := tt.pipelineRun.GetAnnotations()[keys.State]

			kinterfaceTest := &testkubernetestint.KinterfaceTest{
				GetSecretResult: map[string]string{
					"pac-git-basic-auth-owner-repo": "https://whateveryousayboss",
				},
			}

			cs := &params.Run{
				Clients: clients.Clients{
					Tekton: stdata.Pipeline,
					Log:    logger,
				},
				Info: info.Info{
					Pac: &info.PacOpts{
						Settings: settings.Settings{},
					},
				},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})

			r := &Reconciler{
				repoLister: informers.Repository.Lister(),
				run:        cs,
				kinteract:  kinterfaceTest,
			}

			err := r.ReconcileKind(ctx, tt.pipelineRun)

			// For test cases that should call updatePipelineRunToInProgress,
			// we expect no error and the state should be updated
			if tt.shouldCallUpdateToInProgress {
				assert.NilError(t, err, tt.description)

				// Get the updated PipelineRun to check if state was changed
				updatedPR, getErr := stdata.Pipeline.TektonV1().PipelineRuns(tt.pipelineRun.GetNamespace()).Get(ctx, tt.pipelineRun.GetName(), metav1.GetOptions{})
				assert.NilError(t, getErr)

				// Should have been updated to started state with SCMReportingPLRStarted annotation
				assert.Equal(t, updatedPR.GetAnnotations()[keys.State], kubeinteraction.StateStarted)
				scmStarted, exists := updatedPR.GetAnnotations()[keys.SCMReportingPLRStarted]
				assert.Assert(t, exists, "SCMReportingPLRStarted should be set")
				assert.Equal(t, scmStarted, "true")
			} else {
				// For cases that should NOT call updatePipelineRunToInProgress,
				// the state should remain unchanged
				updatedPR, getErr := stdata.Pipeline.TektonV1().PipelineRuns(tt.pipelineRun.GetNamespace()).Get(ctx, tt.pipelineRun.GetName(), metav1.GetOptions{})
				assert.NilError(t, getErr)

				// State should be unchanged
				assert.Equal(t, updatedPR.GetAnnotations()[keys.State], originalState, tt.description)

				// Check SCMReportingPLRStarted annotation based on original state
				scmStarted, exists := updatedPR.GetAnnotations()[keys.SCMReportingPLRStarted]
				if originalState == kubeinteraction.StateStarted {
					// If original state was already 'started', SCMReportingPLRStarted should exist and be "true"
					assert.Assert(t, exists, "SCMReportingPLRStarted should exist when original state is started")
					assert.Equal(t, scmStarted, "true", "SCMReportingPLRStarted should be 'true'")
				} else {
					// If original state was not 'started', SCMReportingPLRStarted should not exist
					assert.Assert(t, !exists, "SCMReportingPLRStarted should not exist when original state is not started")
				}
			}
		})
	}
}

func TestCreateSecretForPipelineRun(t *testing.T) {
	// Base annotations required by initGitProviderClient (detectProvider + buildEventFromPipelineRun)
	baseAnnotations := map[string]string{
		keys.GitProvider:   "github",
		keys.RepoURL:       "https://github.com/org/repo",
		keys.URLOrg:        "org",
		keys.URLRepository: "repo",
		keys.SHA:           "abc123",
	}

	providerSecretName := "pac-git-basic-auth-owner-repo"

	tests := []struct {
		name              string
		prAnnotations     map[string]string
		repoUser          string
		createSecretError error
		updateSecretError error
		simulatePatchErr  bool
		wantErr           string
		wantLogSnippet    string
		verifyPatched     bool
	}{
		{
			name:          "missing git-auth-secret annotation",
			prAnnotations: map[string]string{},
			wantErr:       "cannot get annotation",
		},
		{
			name: "MakeBasicAuthSecret failure with malformed URL",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
				keys.RepoURL:       "http://[invalid",
			},
			wantErr: "making basic auth secret",
		},
		{
			name: "CreateSecret generic failure",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			createSecretError: fmt.Errorf("connection timeout"),
			wantErr:           "creating basic auth secret",
		},
		{
			name: "CreateSecret AlreadyExists succeeds with warning",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			createSecretError: errors.NewAlreadyExists(schema.GroupResource{Group: "", Resource: "secrets"}, "test-secret"),
			wantLogSnippet:    "already exists",
			verifyPatched:     true,
		},
		{
			name: "UpdateSecretWithOwnerRef failure",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			updateSecretError: fmt.Errorf("failed to update owner ref"),
			wantErr:           "cannot update secret",
		},
		{
			name: "PatchPipelineRun failure returns error",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			simulatePatchErr: true,
			wantErr:          "failed to patch pipelinerun",
		},
		{
			name: "happy path with default git user",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			verifyPatched: true,
		},
		{
			name: "happy path with custom git user",
			prAnnotations: map[string]string{
				keys.GitAuthSecret: "test-secret",
			},
			repoUser:      "custom-user",
			verifyPatched: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, log := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			// Merge base annotations with test-specific annotations (test-specific overrides base)
			annotations := maps.Clone(baseAnnotations)
			maps.Copy(annotations, tt.prAnnotations)

			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-pr",
					Namespace:   "test-ns",
					Annotations: annotations,
				},
			}

			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
				Spec: v1alpha1.RepositorySpec{
					GitProvider: &v1alpha1.GitProvider{
						Secret: &v1alpha1.Secret{
							Name: providerSecretName,
						},
						User: tt.repoUser,
					},
				},
			}

			testData := testclient.Data{
				PipelineRuns: []*tektonv1.PipelineRun{pr},
			}
			stdata, _ := testclient.SeedTestData(t, ctx, testData)

			if tt.simulatePatchErr {
				stdata.Pipeline.PrependReactor("patch", "pipelineruns", func(_ k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, fmt.Errorf("etcd unavailable")
				})
			}

			kint := &testkubernetestint.KinterfaceTest{
				GetSecretResult: map[string]string{
					providerSecretName: "test-token",
				},
				CreateSecretError: tt.createSecretError,
				UpdateSecretError: tt.updateSecretError,
			}

			r := &Reconciler{
				run: &params.Run{
					Clients: clients.Clients{
						Tekton: stdata.Pipeline,
						Log:    logger,
					},
					Info: info.Info{
						Pac: &info.PacOpts{
							Settings: settings.Settings{},
						},
					},
				},
				kinteract:    kint,
				eventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}

			err := r.createSecretForPipelineRun(ctx, logger, pr, repo)

			if tt.wantErr != "" {
				assert.Assert(t, err != nil, "expected error containing: %s", tt.wantErr)
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)

			if tt.wantLogSnippet != "" {
				logEntries := log.FilterMessageSnippet(tt.wantLogSnippet).TakeAll()
				assert.Assert(t, len(logEntries) > 0, "expected log snippet %q not found", tt.wantLogSnippet)
			}

			if tt.verifyPatched {
				updatedPR, getErr := stdata.Pipeline.TektonV1().PipelineRuns(pr.Namespace).Get(ctx, pr.Name, metav1.GetOptions{})
				assert.NilError(t, getErr)
				assert.Equal(t, updatedPR.Annotations[keys.SecretCreated], "true")
			}
		})
	}
}
