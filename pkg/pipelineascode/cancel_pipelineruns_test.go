package pipelineascode

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"

	"github.com/google/go-github/v84/github"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/selection"
	"knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

var (
	pullReqNumber = 11
	fooRepo       = &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "foo",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://github.com/fooorg/foo",
		},
	}
	fooRepoLabelsForPush = map[string]string{
		keys.URLRepository: formatting.CleanValueKubernetes("foo"),
		keys.SHA:           formatting.CleanValueKubernetes("foosha"),
	}
	fooRepoLabels = map[string]string{
		keys.OriginalPRName: "pr-foo",
		keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
		keys.SHA:            formatting.CleanValueKubernetes("foosha"),
		keys.PullRequest:    strconv.Itoa(pullReqNumber),
		keys.EventType:      string(triggertype.PullRequest),
	}
	fooRepoAnnotations = map[string]string{
		keys.URLRepository: "foo",
		keys.SHA:           "foosha",
		keys.PullRequest:   strconv.Itoa(pullReqNumber),
		keys.Repository:    "foo",
	}
	fooRepoLabelsPrFooAbc = map[string]string{
		keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
		keys.SHA:            formatting.CleanValueKubernetes("foosha"),
		keys.PullRequest:    strconv.Itoa(pullReqNumber),
		keys.OriginalPRName: "pr-foo-abc",
		keys.Repository:     "foo",
	}
	fooRepoAnnotationsPrFooAbc = map[string]string{
		keys.URLRepository:  "foo",
		keys.SHA:            "foosha",
		keys.PullRequest:    strconv.Itoa(pullReqNumber),
		keys.OriginalPRName: "pr-foo-abc",
		keys.Repository:     "foo",
	}
)

func TestCancelPipelinerunOpsComment(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	tests := []struct {
		name                  string
		event                 *info.Event
		repo                  *v1alpha1.Repository
		pipelineRuns          []*pipelinev1.PipelineRun
		cancelledPipelineRuns map[string]bool
	}{
		{
			name: "cancel running",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				TriggerTarget:     "pull_request",
				PullRequestNumber: pullReqNumber,
				State: info.State{
					CancelPipelineRuns: true,
				},
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels:    fooRepoLabels,
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo": true,
			},
		},
		{
			name: "cancel specific run does not affect other repository in shared namespace",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				TriggerTarget:     "pull_request",
				PullRequestNumber: pullReqNumber,
				State: info.State{
					CancelPipelineRuns:      true,
					TargetCancelPipelineRun: "pr-shared",
				},
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-shared-foo-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.URLRepository: formatting.CleanValueKubernetes("foo"),
							keys.PullRequest:   strconv.Itoa(pullReqNumber),
						},
						Annotations: map[string]string{
							keys.OriginalPRName: "pr-shared",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-shared-other-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.URLRepository: formatting.CleanValueKubernetes("other"),
							keys.PullRequest:   strconv.Itoa(pullReqNumber),
						},
						Annotations: map[string]string{
							keys.OriginalPRName: "pr-shared",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-shared-foo-1": true,
			},
		},
		{
			name: "no pipelineruns found",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				TriggerTarget:     "pull_request",
				PullRequestNumber: pullReqNumber,
				State: info.State{
					CancelPipelineRuns: true,
				},
			},
			pipelineRuns:          []*pipelinev1.PipelineRun{},
			repo:                  fooRepo,
			cancelledPipelineRuns: map[string]bool{},
		},
		{
			name: "cancel a specific run",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				TriggerTarget:     "pull_request",
				PullRequestNumber: pullReqNumber,
				State: info.State{
					CancelPipelineRuns:      true,
					TargetCancelPipelineRun: "pr-foo-abc",
				},
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "pr-foo",
						Namespace:   "foo",
						Labels:      fooRepoLabels,
						Annotations: fooRepoAnnotations,
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "pr-foo-abc-123",
						Namespace:   "foo",
						Labels:      fooRepoLabelsPrFooAbc,
						Annotations: fooRepoAnnotationsPrFooAbc,
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "pr-foo-pqr",
						Namespace:   "foo",
						Labels:      fooRepoLabels,
						Annotations: fooRepoAnnotations,
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-abc-123": true,
			},
		},
		{
			name: "cancelling a done pipelinerun or already cancelled pipelinerun",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				TriggerTarget:     "pull_request",
				PullRequestNumber: pullReqNumber,
				State: info.State{
					CancelPipelineRuns: true,
				},
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels:    fooRepoLabels,
					},
					Spec: pipelinev1.PipelineRunSpec{},
					Status: pipelinev1.PipelineRunStatus{
						Status: knativeduckv1.Status{
							Conditions: knativeduckv1.Conditions{
								apis.Condition{
									Type:   apis.ConditionSucceeded,
									Status: corev1.ConditionTrue,
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels:    fooRepoLabels,
					},
					Spec: pipelinev1.PipelineRunSpec{
						Status: pipelinev1.PipelineRunSpecStatusStoppedRunFinally,
					},
				},
			},
			repo:                  fooRepo,
			cancelledPipelineRuns: map[string]bool{},
		},
		{
			name: "cancel running for push event",
			event: &info.Event{
				Repository:    "foo",
				SHA:           "foosha",
				TriggerTarget: "push",
				State: info.State{
					CancelPipelineRuns: true,
				},
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels:    fooRepoLabelsForPush,
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo": true,
			},
		},
		{
			name: "cancel a specific run for push event",
			event: &info.Event{
				Repository:    "foo",
				SHA:           "foosha",
				TriggerTarget: "push",
				State: info.State{
					CancelPipelineRuns:      true,
					TargetCancelPipelineRun: "pr-foo-abc",
				},
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "pr-foo",
						Namespace:   "foo",
						Labels:      fooRepoLabelsForPush,
						Annotations: fooRepoAnnotations,
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "pr-foo-abc-123",
						Namespace:   "foo",
						Labels:      fooRepoLabelsPrFooAbc,
						Annotations: fooRepoAnnotationsPrFooAbc,
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "pr-foo-pqr",
						Namespace:   "foo",
						Labels:      fooRepoLabelsForPush,
						Annotations: fooRepoAnnotations,
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-abc-123": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			tdata := testclient.Data{
				PipelineRuns: tt.pipelineRuns,
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					Log:    logger,
					Tekton: stdata.Pipeline,
					Kube:   stdata.Kube,
				},
			}
			pac := NewPacs(tt.event, nil, cs, &info.PacOpts{}, nil, logger, nil)
			err := pac.cancelPipelineRunsOpsComment(ctx, tt.repo)
			assert.NilError(t, err)

			got, err := cs.Clients.Tekton.TektonV1().PipelineRuns("foo").List(ctx, metav1.ListOptions{})
			assert.NilError(t, err)

			for _, pr := range got.Items {
				// from the list only the ones which are in cancelled map should have cancel status
				if _, ok := tt.cancelledPipelineRuns[pr.Name]; ok {
					assert.Equal(t, string(pr.Spec.Status), pipelinev1.PipelineRunSpecStatusCancelledRunFinally)
					continue
				}
				assert.Assert(t, string(pr.Spec.Status) != pipelinev1.PipelineRunSpecStatusCancelledRunFinally)
			}
		})
	}
}

func TestCancelPipelineRunsOpsCommentResolvesTargetNamespaceFromTemplate(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	ctx, _ := rtesting.SetupFakeContext(t)

	fooRepo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "foo",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://ghe.pipelinesascode.com/pipelines-as-code/e2e",
		},
	}
	barRepo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "bar",
			Name:      "bar",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://ghe.pipelinesascode.com/pipelines-as-code/e2e",
		},
	}

	targetPR := &pipelinev1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-gitops-comment-ztrg2",
			Namespace: "bar",
			Labels: map[string]string{
				keys.URLRepository: formatting.CleanValueKubernetes("e2e"),
				keys.PullRequest:   strconv.Itoa(pullReqNumber),
			},
			Annotations: map[string]string{
				keys.OriginalPRName: "pr-gitops-comment",
			},
		},
		Spec: pipelinev1.PipelineRunSpec{},
	}

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{fooRepo, barRepo},
		PipelineRuns: []*pipelinev1.PipelineRun{targetPR},
	})

	run := &params.Run{
		Clients: clients.Clients{
			Log:            logger,
			Tekton:         stdata.Pipeline,
			Kube:           stdata.Kube,
			PipelineAsCode: stdata.PipelineAsCode,
		},
	}

	event := &info.Event{
		Repository:        "e2e",
		URL:               "https://ghe.pipelinesascode.com/pipelines-as-code/e2e",
		SHA:               "123",
		TriggerTarget:     triggertype.PullRequest,
		PullRequestNumber: pullReqNumber,
		State: info.State{
			CancelPipelineRuns:      true,
			TargetCancelPipelineRun: "pr-gitops-comment",
		},
	}

	template := `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: pr-gitops-comment
  annotations:
    pipelinesascode.tekton.dev/target-namespace: "bar"
spec:
  pipelineSpec:
    tasks:
    - name: task
      taskSpec:
        steps:
        - name: task
          image: quay.io/prometheus/busybox
          script: |
            echo "HELLOMOTO"
            exit 0
`

	pac := NewPacs(event, &testprovider.TestProviderImp{TektonDirTemplate: template}, run, &info.PacOpts{}, nil, logger, nil)
	err := pac.cancelPipelineRunsOpsComment(ctx, fooRepo)
	assert.NilError(t, err)

	got, err := run.Clients.Tekton.TektonV1().PipelineRuns("bar").Get(ctx, targetPR.GetName(), metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, string(got.Spec.Status), pipelinev1.PipelineRunSpecStatusCancelledRunFinally)
}

func TestCancelPipelineRunsOpsCommentFallsBackWhenTargetNamespaceRepoMissing(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	ctx, _ := rtesting.SetupFakeContext(t)

	fooRepo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "foo",
			Name:      "foo",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://ghe.pipelinesascode.com/pipelines-as-code/e2e",
		},
	}

	targetPR := &pipelinev1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-gitops-comment-ztrg2",
			Namespace: "foo",
			Labels: map[string]string{
				keys.URLRepository: formatting.CleanValueKubernetes("e2e"),
				keys.PullRequest:   strconv.Itoa(pullReqNumber),
			},
			Annotations: map[string]string{
				keys.OriginalPRName: "pr-gitops-comment",
			},
		},
		Spec: pipelinev1.PipelineRunSpec{},
	}

	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{fooRepo},
		PipelineRuns: []*pipelinev1.PipelineRun{targetPR},
	})

	run := &params.Run{
		Clients: clients.Clients{
			Log:            logger,
			Tekton:         stdata.Pipeline,
			Kube:           stdata.Kube,
			PipelineAsCode: stdata.PipelineAsCode,
		},
	}

	event := &info.Event{
		Repository:        "e2e",
		URL:               "https://ghe.pipelinesascode.com/pipelines-as-code/e2e",
		SHA:               "123",
		TriggerTarget:     triggertype.PullRequest,
		PullRequestNumber: pullReqNumber,
		State: info.State{
			CancelPipelineRuns:      true,
			TargetCancelPipelineRun: "pr-gitops-comment",
		},
	}

	template := `apiVersion: tekton.dev/v1beta1
kind: PipelineRun
metadata:
  name: pr-gitops-comment
  annotations:
    pipelinesascode.tekton.dev/target-namespace: "does-not-exist"
spec:
  pipelineSpec:
    tasks:
    - name: task
      taskSpec:
        steps:
        - name: task
          image: quay.io/prometheus/busybox
          script: |
            echo "HELLOMOTO"
            exit 0
`

	pac := NewPacs(event, &testprovider.TestProviderImp{TektonDirTemplate: template}, run, &info.PacOpts{}, nil, logger, nil)
	err := pac.cancelPipelineRunsOpsComment(ctx, fooRepo)
	assert.NilError(t, err)

	got, err := run.Clients.Tekton.TektonV1().PipelineRuns("foo").Get(ctx, targetPR.GetName(), metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, string(got.Spec.Status), pipelinev1.PipelineRunSpecStatusCancelledRunFinally)
}

func TestCancelInProgressMatchingPipelineRun(t *testing.T) {
	observer, catcher := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	tests := []struct {
		name                   string
		nocancelcheck          bool
		event                  *info.Event
		repo                   *v1alpha1.Repository
		pipelineRuns           []*pipelinev1.PipelineRun
		cancelInProgressOnPR   bool
		cancelInProgressOnPush bool
		cancelledPipelineRuns  map[string]bool
		wantErrString          string
		wantLog                string
	}{
		{
			name:         "skipped/no pr",
			pipelineRuns: nil,
		},
		{
			name: "skipped/no cancel in progress annotations",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.PullRequest),
				TriggerTarget:     triggertype.PullRequest,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels:    fooRepoLabels,
					},
				},
			},
		},
		{
			name: "skipped/no original pr name",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.PullRequest),
				TriggerTarget:     triggertype.PullRequest,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels:    map[string]string{},
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
						},
					},
				},
			},
		},
		{
			name: "skip/finished pr",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.PullRequest),
				TriggerTarget:     triggertype.PullRequest,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels:    fooRepoLabels,
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels:    fooRepoLabels,
						Annotations: map[string]string{
							keys.CancelInProgress: "true", keys.OriginalPRName: "pr-foo",
							keys.Repository:   "foo",
							keys.SourceBranch: "head",
						},
					},
					Status: pipelinev1.PipelineRunStatus{
						Status: knativeduckv1.Status{
							Conditions: knativeduckv1.Conditions{
								apis.Condition{
									Type:   apis.ConditionSucceeded,
									Status: corev1.ConditionTrue,
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels:    fooRepoLabels,
						Annotations: map[string]string{
							keys.CancelInProgress: "true", keys.OriginalPRName: "pr-foo",
							keys.Repository:   "foo",
							keys.SourceBranch: "head",
						},
					},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-2": true,
			},
		},
		{
			name:          "skip/cancelled pr",
			nocancelcheck: true,
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.PullRequest),
				TriggerTarget:     triggertype.PullRequest,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels:    fooRepoLabels,
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-cancelled",
						Namespace: "foo",
						Labels:    fooRepoLabels,
						Annotations: map[string]string{
							keys.CancelInProgress: "true", keys.OriginalPRName: "pr-foo",
							keys.Repository:   "foo",
							keys.SourceBranch: "head",
						},
					},
					Status: pipelinev1.PipelineRunStatus{
						Status: knativeduckv1.Status{
							Conditions: knativeduckv1.Conditions{
								apis.Condition{
									Type:   apis.ConditionSucceeded,
									Status: corev1.ConditionTrue,
									Reason: pipelinev1.PipelineRunSpecStatusCancelled,
								},
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-fou",
						Namespace: "foo",
						Labels:    fooRepoLabels,
						Annotations: map[string]string{
							keys.CancelInProgress: "true", keys.OriginalPRName: "pr-foo",
							keys.Repository:   "foo",
							keys.SourceBranch: "head",
						},
					},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-2": true,
			},
		},
		{
			name: "match/cancel in progress",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.PullRequest),
				TriggerTarget:     triggertype.PullRequest,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels:    fooRepoLabels,
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels:    fooRepoLabels,
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-1": true,
			},
			wantLog: "cancel-in-progress: cancelling pipelinerun foo/",
		},
		{
			name: "match/cancel in progress on PipelineRun generateName",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.PullRequest),
				TriggerTarget:     triggertype.PullRequest,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pr-foo-",
						Name:         "pr-foo-1",
						Namespace:    "foo",
						Labels:       fooRepoLabels,
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pr-foo-",
						Name:         "pr-foo-2",
						Namespace:    "foo",
						Labels:       fooRepoLabels,
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-2": true,
			},
			wantLog: "cancel-in-progress: cancelling pipelinerun foo/pr-foo-2",
		},
		{
			name: "match/cancel in progress from /retest",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.PullRequest),
				TriggerTarget:     triggertype.PullRequest,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels:    fooRepoLabels,
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(opscomments.RetestAllCommentEventType),
						},
						Annotations: map[string]string{
							keys.CancelInProgress: "true", keys.OriginalPRName: "pr-foo",
							keys.Repository:   "foo",
							keys.SourceBranch: "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-1": true,
			},
			wantLog: "cancel-in-progress: cancelling pipelinerun foo/pr-foo-1",
		},
		{
			name: "match/cancel in progress exclude not belonging to same push branch",
			event: &info.Event{
				Repository:    "foo",
				SHA:           "foosha",
				HeadBranch:    "head",
				EventType:     string(triggertype.Push),
				TriggerTarget: triggertype.Push,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.EventType:      string(triggertype.Push),
						},
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.EventType:      string(triggertype.Push),
						},
						Annotations: map[string]string{
							keys.CancelInProgress: "true", keys.OriginalPRName: "pr-foo",
							keys.Repository:   "foo",
							keys.SourceBranch: "anotherhead",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.EventType:      string(triggertype.Push),
						},
						Annotations: map[string]string{
							keys.CancelInProgress: "true", keys.OriginalPRName: "pr-foo",
							keys.Repository:   "foo",
							keys.SourceBranch: "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-2": true,
			},
			wantLog: "skipping pipelinerun foo/pr-foo-1 as it is not from the same branch",
		},
		{
			name: "match/cancel in progress exclude not belonging to same pr",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.PullRequest),
				TriggerTarget:     triggertype.PullRequest,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(triggertype.PullRequest),
						},
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(10),
							keys.EventType:      string(triggertype.PullRequest),
						},
						Annotations: map[string]string{
							keys.CancelInProgress: "true", keys.OriginalPRName: "pr-foo",
							keys.Repository:   "foo",
							keys.SourceBranch: "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(triggertype.PullRequest),
						},
						Annotations: map[string]string{
							keys.CancelInProgress: "true", keys.OriginalPRName: "pr-foo",
							keys.Repository:   "foo",
							keys.SourceBranch: "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-2": true,
			},
			wantLog: "cancel-in-progress: cancelling pipelinerun foo/",
		},
		{
			name: "match/cancel in progress on PR is enable via ConfigMap",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.PullRequest),
				TriggerTarget:     triggertype.PullRequest,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(triggertype.PullRequest),
						},
						Annotations: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.Repository:     "foo",
							keys.SourceBranch:   "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(triggertype.PullRequest),
						},
						Annotations: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.Repository:     "foo",
							keys.SourceBranch:   "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo:                 fooRepo,
			cancelInProgressOnPR: true,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-2": true,
			},
			wantLog: "cancel-in-progress for event pull_request is enabled globally via Pipelines-as-Code ConfigMap",
		},
		{
			name: "match/cancel in progress on push is enable via ConfigMap",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.Push),
				TriggerTarget:     triggertype.Push,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.EventType:      string(triggertype.Push),
						},
						Annotations: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.Repository:     "foo",
							keys.SourceBranch:   "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.EventType:      string(triggertype.Push),
						},
						Annotations: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.Repository:     "foo",
							keys.SourceBranch:   "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo:                   fooRepo,
			cancelInProgressOnPush: true,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-2": true,
			},
			wantLog: "cancel-in-progress for event push is enabled globally via Pipelines-as-Code ConfigMap",
		},
		{
			name: "match/cancel in progress settings on PR is overridden by PR annotation",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.PullRequest),
				TriggerTarget:     triggertype.PullRequest,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(triggertype.PullRequest),
						},
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(triggertype.PullRequest),
						},
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo:                 fooRepo,
			cancelInProgressOnPR: false, // make it explicitly false
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-2": true,
			},
			wantLog: "cancel-in-progress for event pull_request is enabled via PipelineRun annotation",
		},
		{
			name: "match/cancel in progress settings on push is overridden by PR annotation",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.Push),
				TriggerTarget:     triggertype.Push,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.EventType:      string(triggertype.Push),
						},
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.EventType:      string(triggertype.Push),
						},
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo:                   fooRepo,
			cancelInProgressOnPush: false, // make it explicitly false
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-2": true,
			},
			wantLog: "cancel-in-progress for event push is enabled via PipelineRun annotation",
		},
		{
			name: "matching PipelineRun when source branch annotation is having full path refs/heads/",
			event: &info.Event{
				Repository:        "foo",
				SHA:               "foosha",
				HeadBranch:        "head",
				EventType:         string(triggertype.Push),
				TriggerTarget:     triggertype.Push,
				PullRequestNumber: pullReqNumber,
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.EventType:      string(triggertype.Push),
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
						},
						Annotations: map[string]string{
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "refs/heads/head",
							keys.CancelInProgress: "true",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.EventType:      string(triggertype.Push),
						},
						Annotations: map[string]string{
							keys.OriginalPRName:   "pr-foo",
							keys.Repository:       "foo",
							keys.SourceBranch:     "head",
							keys.CancelInProgress: "true",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: fooRepo,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-2": true,
			},
			wantLog: "cancel-in-progress for event push is enabled via PipelineRun annotation",
		},
		{
			name: "skip/cancel in progress with concurrency limit",
			event: &info.Event{
				Repository: "foo",
				SHA:        "foosha",
			},
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:        "pr-foo",
						Namespace:   "foo",
						Labels:      fooRepoLabels,
						Annotations: map[string]string{keys.CancelInProgress: "true", keys.OriginalPRName: "pr-foo", keys.Repository: "foo"},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "foo",
					Name:      "foo",
				},
				Spec: v1alpha1.RepositorySpec{
					URL:              "https://github.com/fooorg/foo",
					ConcurrencyLimit: github.Ptr(1),
				},
			},
			cancelledPipelineRuns: map[string]bool{},
			wantErrString:         "cancel in progress is not supported with concurrency limit",
			wantLog:               "cancel-in-progress: cancelling pipelinerun foo/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			tdata := testclient.Data{
				PipelineRuns: tt.pipelineRuns,
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					Log:    logger,
					Tekton: stdata.Pipeline,
					Kube:   stdata.Kube,
				},
			}
			pacInfo := &info.PacOpts{
				Settings: settings.Settings{
					EnableCancelInProgressOnPullRequests: tt.cancelInProgressOnPR,
					EnableCancelInProgressOnPush:         tt.cancelInProgressOnPush,
				},
			}
			pac := NewPacs(tt.event, nil, cs, pacInfo, nil, logger, nil)
			var firstPr *pipelinev1.PipelineRun
			if len(tt.pipelineRuns) > 0 {
				firstPr = tt.pipelineRuns[0]
			}
			err := pac.cancelInProgressMatchingPipelineRun(ctx, firstPr, tt.repo)
			if tt.wantErrString != "" {
				assert.ErrorContains(t, err, tt.wantErrString)
				return
			}
			assert.NilError(t, err)

			// the fake k8 test library don't set cancellation status, so we can't check the status :\
			got, err := cs.Clients.Tekton.TektonV1().PipelineRuns("foo").List(ctx, metav1.ListOptions{})
			assert.NilError(t, err)

			for _, pr := range got.Items {
				// from the list only the ones which are in cancelled map should have cancel status
				if _, ok := tt.cancelledPipelineRuns[pr.Name]; ok {
					assert.Equal(t, string(pr.Spec.Status), pipelinev1.PipelineRunSpecStatusCancelledRunFinally, pr.GetName())
					continue
				}
				if !tt.nocancelcheck {
					assert.Assert(t, string(pr.Spec.Status) != pipelinev1.PipelineRunSpecStatusCancelledRunFinally)
				}
			}

			if tt.wantLog != "" {
				assert.Assert(t, len(catcher.FilterMessageSnippet(tt.wantLog).TakeAll()) > 0, fmt.Sprintf("could not find log message: got %+v", catcher.TakeAll()))
			}
		})
	}
}

func TestCancelAllInProgressBelongingToClosedPullRequest(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()

	tests := []struct {
		name                  string
		event                 *info.Event
		repo                  *v1alpha1.Repository
		pipelineRuns          []*pipelinev1.PipelineRun
		cancelInProgressOnPR  bool
		cancelledPipelineRuns map[string]bool
	}{
		{
			name: "cancel all in progress PipelineRuns with annotation set to true",
			event: &info.Event{
				Repository:        "foo",
				TriggerTarget:     "pull_request",
				PullRequestNumber: pullReqNumber,
			},
			repo: fooRepo,
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName:   "pr-foo",
							keys.URLRepository:    formatting.CleanValueKubernetes("foo"),
							keys.SHA:              formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:      strconv.Itoa(pullReqNumber),
							keys.EventType:        string(triggertype.PullRequest),
							keys.CancelInProgress: "true",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName:   "pr-foo",
							keys.URLRepository:    formatting.CleanValueKubernetes("foo"),
							keys.SHA:              formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:      strconv.Itoa(pullReqNumber),
							keys.EventType:        string(triggertype.PullRequest),
							keys.CancelInProgress: "true",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-1": true,
				"pr-foo-2": true,
			},
		},
		{
			name: "cancel all in progress PipelineRuns with annotation set to false",
			event: &info.Event{
				Repository:        "foo",
				TriggerTarget:     "pull_request",
				PullRequestNumber: pullReqNumber,
			},
			repo: fooRepo,
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName:   "pr-foo",
							keys.URLRepository:    formatting.CleanValueKubernetes("foo"),
							keys.SHA:              formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:      strconv.Itoa(pullReqNumber),
							keys.EventType:        string(triggertype.PullRequest),
							keys.CancelInProgress: "false",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName:   "pr-foo",
							keys.URLRepository:    formatting.CleanValueKubernetes("foo"),
							keys.SHA:              formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:      strconv.Itoa(pullReqNumber),
							keys.EventType:        string(triggertype.PullRequest),
							keys.CancelInProgress: "false",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			cancelledPipelineRuns: map[string]bool{},
		},
		{
			name: "cancel all in progress PipelineRuns with no annotation",
			event: &info.Event{
				Repository:        "foo",
				TriggerTarget:     "pull_request",
				PullRequestNumber: pullReqNumber,
			},
			repo: fooRepo,
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(triggertype.PullRequest),
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(triggertype.PullRequest),
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			cancelledPipelineRuns: map[string]bool{},
		},
		// this can happen when a pull request is merged and a new push is made to the branch
		// so PaC assign the merged pull request number to the push PipelineRun
		{
			name: "do not cancel push-triggered PipelineRuns on PR close",
			event: &info.Event{
				Repository:        "foo",
				PullRequestNumber: pullReqNumber,
				TriggerTarget:     "push",
			},
			repo: fooRepo,
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					Spec: pipelinev1.PipelineRunSpec{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(triggertype.Push),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
						},
					},
				},
			},
			cancelledPipelineRuns: map[string]bool{},
		},
		{
			name: "exclude PipelineRun having cancel-in-progress set to false when global setting is true",
			event: &info.Event{
				Repository:        "foo",
				TriggerTarget:     "pull_request",
				PullRequestNumber: pullReqNumber,
			},
			repo: fooRepo,
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName:   "pr-foo-1",
							keys.URLRepository:    formatting.CleanValueKubernetes("foo"),
							keys.SHA:              formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:      strconv.Itoa(pullReqNumber),
							keys.EventType:        string(triggertype.PullRequest),
							keys.CancelInProgress: "true",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo-2",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(triggertype.PullRequest),
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				// this must not be cancelled as it has 'cancel-in-progress' annotation set to false
				// overriding global setting.
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-3",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName:   "pr-foo-3",
							keys.URLRepository:    formatting.CleanValueKubernetes("foo"),
							keys.SHA:              formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:      strconv.Itoa(pullReqNumber),
							keys.EventType:        string(triggertype.PullRequest),
							keys.CancelInProgress: "false",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			cancelInProgressOnPR: true,
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-1": true,
				"pr-foo-2": true,
			},
		},
		{
			name: "include only PipelineRuns having cancel-in-progress set to true when global setting is false",
			event: &info.Event{
				Repository:        "foo",
				TriggerTarget:     "pull_request",
				PullRequestNumber: pullReqNumber,
			},
			repo: fooRepo,
			pipelineRuns: []*pipelinev1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-1",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName:   "pr-foo-1",
							keys.URLRepository:    formatting.CleanValueKubernetes("foo"),
							keys.SHA:              formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:      strconv.Itoa(pullReqNumber),
							keys.EventType:        string(triggertype.PullRequest),
							keys.CancelInProgress: "true",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				// this must not be included as it has 'cancel-in-progress' annotation set to false.
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-2",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName:   "pr-foo-2",
							keys.URLRepository:    formatting.CleanValueKubernetes("foo"),
							keys.SHA:              formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:      strconv.Itoa(pullReqNumber),
							keys.EventType:        string(triggertype.PullRequest),
							keys.CancelInProgress: "false",
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
				// this must not be included as it doesn't have 'cancel-in-progress' annotation
				// and global setting is disabled as well.
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pr-foo-3",
						Namespace: "foo",
						Labels: map[string]string{
							keys.OriginalPRName: "pr-foo-3",
							keys.URLRepository:  formatting.CleanValueKubernetes("foo"),
							keys.SHA:            formatting.CleanValueKubernetes("foosha"),
							keys.PullRequest:    strconv.Itoa(pullReqNumber),
							keys.EventType:      string(triggertype.PullRequest),
						},
					},
					Spec: pipelinev1.PipelineRunSpec{},
				},
			},
			cancelledPipelineRuns: map[string]bool{
				"pr-foo-1": true,
			},
		},
		{
			name: "no PipelineRuns to cancel",
			event: &info.Event{
				Repository:        "foo",
				TriggerTarget:     "pull_request",
				PullRequestNumber: pullReqNumber,
			},
			repo:                  fooRepo,
			pipelineRuns:          []*pipelinev1.PipelineRun{},
			cancelledPipelineRuns: map[string]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)

			tdata := testclient.Data{
				PipelineRuns: tt.pipelineRuns,
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{
				Clients: clients.Clients{
					Log:    logger,
					Tekton: stdata.Pipeline,
					Kube:   stdata.Kube,
				},
			}
			pacInfo := &info.PacOpts{
				Settings: settings.Settings{
					EnableCancelInProgressOnPullRequests: tt.cancelInProgressOnPR,
				},
			}
			pac := NewPacs(tt.event, nil, cs, pacInfo, nil, logger, nil)
			err := pac.cancelAllInProgressBelongingToClosedPullRequest(ctx, tt.repo)
			assert.NilError(t, err)

			got, err := cs.Clients.Tekton.TektonV1().PipelineRuns("foo").List(ctx, metav1.ListOptions{})
			assert.NilError(t, err)

			for _, pr := range got.Items {
				if _, ok := tt.cancelledPipelineRuns[pr.Name]; ok {
					assert.Equal(t, string(pr.Spec.Status), pipelinev1.PipelineRunSpecStatusCancelledRunFinally)
				} else {
					assert.Assert(t, string(pr.Spec.Status) != pipelinev1.PipelineRunSpecStatusCancelledRunFinally)
				}
			}
		})
	}
}

func TestGetLabelSelector(t *testing.T) {
	tests := []struct {
		name      string
		labelsMap map[string]string
		want      string
		operator  selection.Operator
	}{
		{
			name: "single label",
			labelsMap: map[string]string{
				"app": "nginx",
			},
			want: "app=nginx",
		},
		{
			name: "multiple labels",
			labelsMap: map[string]string{
				"app":     "nginx",
				"version": "1.14.2",
			},
			want: "app=nginx,version=1.14.2",
		},
		{
			name:      "empty labels",
			labelsMap: map[string]string{},
			want:      "",
		},
		{
			name: "not in operator",
			labelsMap: map[string]string{
				keys.CancelInProgress: "false",
			},
			operator: selection.NotIn,                                               //codespell:ignore 'NotIn'
			want:     "pipelinesascode.tekton.dev/cancel-in-progress notin (false)", //codespell:ignore 'NotIn'
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.operator == "" {
				tt.operator = selection.Equals
			}
			if got := getLabelSelector(tt.labelsMap, tt.operator); got != tt.want {
				t.Errorf("getLabelSelector() = %v, want %v", got, tt.want)
			}
		})
	}
}
