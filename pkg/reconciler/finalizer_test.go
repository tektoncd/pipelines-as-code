package reconciler

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/consoleui"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	queuepkg "github.com/openshift-pipelines/pipelines-as-code/pkg/queue"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	ghtesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/github"
	testkubernetestint "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/logging"
	rtesting "knative.dev/pkg/reconciler/testing"
	"knative.dev/pkg/system"
)

var (
	concurrency      = 1
	finalizeTestRepo = &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pac-app",
			Namespace: "pac-app-pipelines",
		},
		Spec: v1alpha1.RepositorySpec{
			URL:              "https://github.com/org/repo",
			ConcurrencyLimit: &concurrency,
			GitProvider: &v1alpha1.GitProvider{
				Secret: &v1alpha1.Secret{
					Name: "pac-git-basic-auth-owner-repo",
				},
			},
		},
	}
)

func getTestPR(name, state string) *tektonv1.PipelineRun {
	var status tektonv1.PipelineRunSpecStatus
	if state == kubeinteraction.StateQueued {
		status = tektonv1.PipelineRunSpecStatusPending
	}
	return &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: finalizeTestRepo.Namespace,
			Annotations: map[string]string{
				keys.State:         state,
				keys.Repository:    finalizeTestRepo.Name,
				keys.GitProvider:   "github",
				keys.SHA:           "123afc",
				keys.URLOrg:        "org",
				keys.URLRepository: "repo",
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			Status: status,
		},
	}
}

func TestControllerInfoForPipelineRun(t *testing.T) {
	fallback := &info.ControllerInfo{Name: "default", Secret: "default-secret"}
	tests := []struct {
		name       string
		annotation string
		fallback   *info.ControllerInfo
		want       *info.ControllerInfo
		wantErrSub string
	}{
		{
			name:       "controller annotation",
			annotation: `{"name":"secondary","configmap":"secondary-config","secret":"secondary-secret","gRepo":"secondary-global"}`,
			fallback:   fallback,
			want: &info.ControllerInfo{
				Name:             "secondary",
				Configmap:        "secondary-config",
				Secret:           "secondary-secret",
				GlobalRepository: "secondary-global",
			},
		},
		{
			name:     "fallback controller",
			fallback: fallback,
			want:     fallback,
		},
		{
			name:     "default controller without fallback",
			fallback: nil,
			want: &info.ControllerInfo{
				Name:             "default",
				Configmap:        info.DefaultPipelinesAscodeConfigmapName,
				Secret:           info.DefaultPipelinesAscodeSecretName,
				GlobalRepository: info.DefaultGlobalRepoName,
			},
		},
		{
			name:       "invalid annotation",
			annotation: "{",
			fallback:   fallback,
			wantErrSub: "failed to parse controllerInfo",
		},
		{
			name:       "null annotation",
			annotation: "null",
			fallback:   fallback,
			wantErrSub: "value must not be null",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PAC_CONTROLLER_LABEL", "default")
			t.Setenv("PAC_CONTROLLER_SECRET", info.DefaultPipelinesAscodeSecretName)
			t.Setenv("PAC_CONTROLLER_CONFIGMAP", info.DefaultPipelinesAscodeConfigmapName)
			t.Setenv("PAC_CONTROLLER_GLOBAL_REPOSITORY", info.DefaultGlobalRepoName)

			pr := &tektonv1.PipelineRun{}
			if tt.annotation != "" {
				pr.Annotations = map[string]string{keys.ControllerInfo: tt.annotation}
			}
			got, err := controllerInfoForPipelineRun(pr, tt.fallback)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
				return
			}
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
			if tt.fallback != nil {
				assert.Assert(t, got != tt.fallback, "controller info must be copied per reconciliation")
			}
		})
	}
}

func TestFinalizeKindControllerInfoHandling(t *testing.T) {
	tests := []struct {
		name        string
		annotation  string
		wantErrSub  string
		wantControl *info.ControllerInfo
	}{
		{
			name:       "invalid controller annotation",
			annotation: "{",
			wantErrSub: "failed to parse controllerInfo",
		},
		{
			name:        "controller annotation does not mutate shared run",
			annotation:  `{"name":"secondary","configmap":"secondary-config","secret":"secondary-secret","gRepo":"secondary-global"}`,
			wantControl: &info.ControllerInfo{Name: "default", Secret: "default-secret", GlobalRepository: "default-global"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			controller := &info.ControllerInfo{Name: "default", Secret: "default-secret", GlobalRepository: "default-global"}
			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pr",
					Namespace: "test-ns",
					Annotations: map[string]string{
						keys.State:          kubeinteraction.StateStarted,
						keys.ControllerInfo: tt.annotation,
					},
				},
			}
			r := &Reconciler{
				run: &params.Run{
					Clients: clients.Clients{Log: logger},
					Info: info.Info{
						Kube:       &info.KubeOpts{Namespace: "global"},
						Controller: controller,
						Pac:        info.NewPacOpts(),
					},
				},
			}

			err := r.FinalizeKind(ctx, pr)
			if tt.wantErrSub != "" {
				assert.ErrorContains(t, err, tt.wantErrSub)
			} else {
				assert.NilError(t, err)
			}
			if tt.wantControl != nil {
				assert.DeepEqual(t, r.run.Info.Controller, tt.wantControl)
				assert.Assert(t, r.run.Info.Controller == controller, "finalize must keep the shared controller pointer")
			}
		})
	}
}

func TestReconcilerFinalizeKind(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()

	_, mux, mockServerURL, teardown := ghtesthelper.SetupGH()
	defer teardown()

	finalizeTestRepo.Spec.GitProvider.URL = mockServerURL

	// Mock status endpoint. Cancelling a running PipelineRun has to reach the
	// provider, and the failure to do so is swallowed by the finalizer, so the
	// call itself is what the test has to observe.
	var statusesReported atomic.Int32
	mux.HandleFunc("/repos/org/repo/statuses/123afc", func(rw http.ResponseWriter, _ *http.Request) {
		statusesReported.Add(1)
		fmt.Fprint(rw, `{"state":"pending"}`)
	})

	tests := []struct {
		name           string
		pipelinerun    *tektonv1.PipelineRun
		addToQueue     []*tektonv1.PipelineRun
		globalRepo     *v1alpha1.Repository
		skipAddingRepo bool
	}{
		{
			name: "completed pipelinerun",
			pipelinerun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						keys.State: kubeinteraction.StateCompleted,
					},
				},
			},
		},
		{
			name:        "queued pipelinerun",
			pipelinerun: getTestPR("pr3", kubeinteraction.StateQueued),
			globalRepo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pac",
					Namespace: "pac",
				},
				Spec: v1alpha1.RepositorySpec{
					Settings: &v1alpha1.Settings{
						PipelineRunProvenance: "default_branch",
					},
				},
			},
			addToQueue: []*tektonv1.PipelineRun{
				getTestPR("pr1", kubeinteraction.StateQueued),
				getTestPR("pr2", kubeinteraction.StateQueued),
				getTestPR("pr3", kubeinteraction.StateQueued),
			},
		},
		{
			name:        "repo was deleted",
			pipelinerun: getTestPR("pr3", kubeinteraction.StateQueued),
			addToQueue: []*tektonv1.PipelineRun{
				getTestPR("pr1", kubeinteraction.StateStarted),
				getTestPR("pr2", kubeinteraction.StateQueued),
				getTestPR("pr3", kubeinteraction.StateQueued),
			},
			skipAddingRepo: true,
		},
		{
			name:        "cancelled status reported",
			pipelinerun: getTestPR("pr3", kubeinteraction.StateStarted),
			addToQueue: []*tektonv1.PipelineRun{
				getTestPR("pr1", kubeinteraction.StateStarted),
				getTestPR("pr2", kubeinteraction.StateQueued),
				getTestPR("pr3", kubeinteraction.StateQueued),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			ctx = logging.WithLogger(ctx, fakelogger)
			// The controller only sends credentials to hostnames its ConfigMap
			// trusts, so the mock server has to be listed like any self hosted
			// instance would be.
			testData := testclient.Data{
				Repositories: []*v1alpha1.Repository{finalizeTestRepo},
				ConfigMap: []*corev1.ConfigMap{{
					ObjectMeta: metav1.ObjectMeta{Name: "pipelines-as-code", Namespace: system.Namespace()},
					Data: map[string]string{
						settings.TrustedProviderHostnamesKey: strings.TrimPrefix(mockServerURL, "http://"),
					},
				}},
			}
			if tt.globalRepo != nil {
				testData.Repositories = append(testData.Repositories, tt.globalRepo)
			}
			if tt.skipAddingRepo {
				testData.Repositories = []*v1alpha1.Repository{}
			}
			stdata, informers := testclient.SeedTestData(t, ctx, testData)
			// finalizeKind establishes the controller namespace itself, exactly
			// as knative hands it a bare context in production. Injecting one
			// here would hide a regression in that.
			kinterfaceTest := &testkubernetestint.KinterfaceTest{
				GetSecretResult: map[string]string{
					"pac-git-basic-auth-owner-repo": "https://whateveryousayboss",
				},
			}

			cs := &params.Run{
				Clients: clients.Clients{
					PipelineAsCode: stdata.PipelineAsCode,
					Kube:           stdata.Kube,
					Log:            fakelogger,
				},
				Info: info.Info{
					Kube:       &info.KubeOpts{Namespace: "pac"},
					Controller: &info.ControllerInfo{GlobalRepository: "pac"},
					Pac:        info.NewPacOpts(),
				},
			}
			cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
			r := Reconciler{
				repoLister: informers.Repository.Lister(),
				qm:         queuepkg.NewManager(fakelogger),
				run:        cs,
				kinteract:  kinterfaceTest,
			}

			if len(tt.addToQueue) != 0 {
				for _, pr := range tt.addToQueue {
					_, err := r.qm.AddListToRunningQueue(finalizeTestRepo, []string{pr.GetNamespace() + "/" + pr.GetName()})
					assert.NilError(t, err)
				}
			}
			before := statusesReported.Load()
			err := r.FinalizeKind(ctx, tt.pipelinerun)
			assert.NilError(t, err)
			state := tt.pipelinerun.GetAnnotations()[keys.State]
			if !tt.skipAddingRepo && (state == kubeinteraction.StateQueued || state == kubeinteraction.StateStarted) {
				assert.Assert(t, statusesReported.Load() > before,
					"cancelling a running PipelineRun must report to the provider")
			}

			// if repo was deleted then no queue will be there
			if tt.skipAddingRepo {
				assert.Equal(t, len(r.qm.RunningPipelineRuns(finalizeTestRepo)), 0)
				assert.Equal(t, len(r.qm.QueuedPipelineRuns(finalizeTestRepo)), 0)
				return
			}

			// if queue was populated then number of elements in it should
			// be one less than total added
			if len(tt.addToQueue) != 0 {
				totalInQueue := len(r.qm.QueuedPipelineRuns(finalizeTestRepo)) + len(r.qm.RunningPipelineRuns(finalizeTestRepo))
				assert.Equal(t, totalInQueue, len(tt.addToQueue)-1)
			}
			if tt.globalRepo != nil {
				cachedRepo, err := informers.Repository.Lister().Repositories(finalizeTestRepo.Namespace).Get(finalizeTestRepo.Name)
				assert.NilError(t, err)
				assert.Assert(t, cachedRepo.Spec.Settings == nil, "global settings should not mutate the cached Repository")
			}
		})
	}
}

// TestReconcilerFinalizeKindSkipsMalformedQueueKey is a regression test for a
// panic on a malformed key taken off the queue: FinalizeKind splits and
// indexes the "next" key returned by RemoveAndTakeItemFromQueue the same way
// queue_pipelineruns.go and queue_manager.go do for execution-order entries,
// so it needs the same guard against an entry that doesn't split into exactly
// namespace/name. A malformed queue entry is not reachable through normal
// annotation parsing today (see TestFilterPipelineRunByStateSkipsMalformedKeys),
// but the guard here is deliberately defensive rather than relying on that
// invariant holding everywhere a string is added to the queue.
func TestReconcilerFinalizeKindSkipsMalformedQueueKey(t *testing.T) {
	observer, logs := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()

	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = logging.WithLogger(ctx, fakelogger)

	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{finalizeTestRepo},
	})

	cs := &params.Run{
		Clients: clients.Clients{
			PipelineAsCode: stdata.PipelineAsCode,
			Kube:           stdata.Kube,
			Log:            fakelogger,
		},
		Info: info.Info{
			Kube:       &info.KubeOpts{Namespace: "pac"},
			Controller: &info.ControllerInfo{GlobalRepository: "pac"},
			Pac:        info.NewPacOpts(),
		},
	}
	cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
	r := Reconciler{
		repoLister: informers.Repository.Lister(),
		qm:         queuepkg.NewManager(fakelogger),
		run:        cs,
		kinteract:  &testkubernetestint.KinterfaceTest{},
	}

	running := getTestPR("running", kubeinteraction.StateStarted)
	_, err := r.qm.AddListToRunningQueue(finalizeTestRepo, []string{queuepkg.PrKey(running)})
	assert.NilError(t, err)
	// A malformed entry, as if something other than PrKey had added it to the
	// queue: no "/" separator to split on namespace/name.
	assert.NilError(t, r.qm.AddToPendingQueue(finalizeTestRepo, []string{"malformed-entry-with-no-slash"}))

	err = r.FinalizeKind(ctx, running)
	assert.NilError(t, err, "a malformed queue entry must not panic or fail the finalizer")

	found := false
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "invalid pipelineRun key") {
			found = true
		}
	}
	assert.Assert(t, found, "expected a warning about the malformed queue entry, got: %v", logs.All())
}

// TestReconcilerFinalizeKindPromotesSuccessorPastMalformedKey is a regression
// test for a slot leak: RemoveAndTakeItemFromQueue already moves the "next"
// key into the running set before FinalizeKind gets a chance to validate it,
// so simply discarding a malformed key without releasing that reservation
// would strand it forever, since nothing ever completes to release a slot
// that was never a real running PipelineRun. FinalizeKind must keep retrying
// past the malformed entry and promote the next valid candidate instead of
// leaving the running queue with a phantom occupant.
func TestReconcilerFinalizeKindPromotesSuccessorPastMalformedKey(t *testing.T) {
	observer, logs := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()

	ctx, _ := rtesting.SetupFakeContext(t)
	ctx = logging.WithLogger(ctx, fakelogger)

	_, mux, mockServerURL, teardown := ghtesthelper.SetupGH()
	defer teardown()
	mux.HandleFunc("/repos/org/repo/statuses/123afc", func(rw http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(rw, `{"state":"pending"}`)
	})

	repo := finalizeTestRepo.DeepCopy()
	repo.Spec.GitProvider.URL = mockServerURL

	successor := getTestPR("successor", kubeinteraction.StateQueued)

	stdata, informers := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{repo},
		PipelineRuns: []*tektonv1.PipelineRun{successor},
		ConfigMap: []*corev1.ConfigMap{{
			ObjectMeta: metav1.ObjectMeta{Name: "pipelines-as-code", Namespace: system.Namespace()},
			Data: map[string]string{
				settings.TrustedProviderHostnamesKey: strings.TrimPrefix(mockServerURL, "http://"),
			},
		}},
	})

	cs := &params.Run{
		Clients: clients.Clients{
			PipelineAsCode: stdata.PipelineAsCode,
			Kube:           stdata.Kube,
			Tekton:         stdata.Pipeline,
			Log:            fakelogger,
		},
		Info: info.Info{
			Kube:       &info.KubeOpts{Namespace: "pac"},
			Controller: &info.ControllerInfo{GlobalRepository: "pac"},
			Pac:        info.NewPacOpts(),
		},
	}
	cs.Clients.SetConsoleUI(consoleui.FallBackConsole{})
	r := Reconciler{
		repoLister: informers.Repository.Lister(),
		qm:         queuepkg.NewManager(fakelogger),
		run:        cs,
		kinteract: &testkubernetestint.KinterfaceTest{
			GetSecretResult: map[string]string{
				"pac-git-basic-auth-owner-repo": "https://whateveryousayboss",
			},
		},
	}

	running := getTestPR("running", kubeinteraction.StateStarted)
	_, err := r.qm.AddListToRunningQueue(repo, []string{queuepkg.PrKey(running)})
	assert.NilError(t, err)
	// The malformed entry is queued ahead of the valid successor so
	// RemoveAndTakeItemFromQueue picks it first and FinalizeKind has to skip
	// past it rather than stopping there.
	assert.NilError(t, r.qm.AddToPendingQueue(repo, []string{"malformed-entry-with-no-slash"}))
	assert.NilError(t, r.qm.AddToPendingQueue(repo, []string{queuepkg.PrKey(successor)}))

	err = r.FinalizeKind(ctx, running)
	assert.NilError(t, err, "a malformed queue entry must not panic or fail the finalizer")

	found := false
	for _, entry := range logs.All() {
		if strings.Contains(entry.Message, "invalid pipelineRun key") {
			found = true
		}
	}
	assert.Assert(t, found, "expected a warning about the malformed queue entry, got: %v", logs.All())

	runningKeys := r.qm.RunningPipelineRuns(repo)
	assert.DeepEqual(t, runningKeys, []string{queuepkg.PrKey(successor)})
	assert.Equal(t, len(r.qm.QueuedPipelineRuns(repo)), 0, "the successor must have been promoted, not left pending")
}
