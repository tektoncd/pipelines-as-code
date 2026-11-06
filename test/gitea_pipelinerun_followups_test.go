//go:build e2e

package test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	packeys "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	prgroup "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerungroup"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	pacrepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

// startFollowupsPR runs a PR test of the follow-ups feature, which is enabled
// by default: no ConfigMap change is needed.
func startFollowupsPR(t *testing.T, topts *tgitea.TestOpts) (context.Context, func()) {
	t.Helper()
	ctx := context.Background()
	topts.TargetRefName = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
	topts.TargetNS = topts.TargetRefName
	topts.ParamsRun, topts.Opts, topts.GiteaCNX, _ = tgitea.Setup(ctx)
	assert.NilError(t, topts.ParamsRun.Clients.NewClients(ctx, &topts.ParamsRun.Info))
	ctx, err := cctx.GetControllerCtxInfo(ctx, topts.ParamsRun)
	assert.NilError(t, err)
	assert.NilError(t, pacrepo.CreateNS(ctx, topts.TargetNS, topts.ParamsRun))
	ctx, cleanup := tgitea.TestPR(t, topts)
	return ctx, cleanup
}

// waitForPendingFollowUp polls until a run of the event stores a pending
// follow-up for the given definition, and returns it. A waiting follow-up is
// a stored follow-up on its dependency, not a run. The dependency must still
// be running when this is called, or the follow-up may already have been
// created or consumed.
//
//nolint:unparam // every fixture's follow-up is named deploy, the parameter keeps the helper generic
func waitForPendingFollowUp(ctx context.Context, t *testing.T, topts *tgitea.TestOpts, definition string) prgroup.FollowUp {
	t.Helper()
	deadline := time.Now().Add(twait.DefaultTimeout)
	for {
		runs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
		assert.NilError(t, err)
		for i := range runs.Items {
			followUps, err := prgroup.FollowUpsOf(&runs.Items[i])
			assert.NilError(t, err)
			for _, followUp := range followUps {
				if followUp.Name == definition && followUp.Pending() {
					topts.ParamsRun.Clients.Log.Infof("follow-up %s is waiting as follow-up %s on pipelinerun %s",
						definition, followUp.ID, runs.Items[i].GetName())
					return followUp
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for a pending follow-up %s on the runs of the namespace", definition)
		}
		time.Sleep(2 * time.Second)
	}
}

// waitForFollowUpState polls until the follow-up is stored in the given state on
// any run of the namespace: a follow-up migrates between the runs of its
// dependencies while it waits.
func waitForFollowUpState(ctx context.Context, t *testing.T, topts *tgitea.TestOpts, id, state string) {
	t.Helper()
	deadline := time.Now().Add(twait.DefaultTimeout)
	for {
		list, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
		assert.NilError(t, err)
		runs := list.Items
		for i := range runs {
			followUps, err := prgroup.FollowUpsOf(&runs[i])
			assert.NilError(t, err)
			for _, followUp := range followUps {
				if followUp.ID == id && followUp.State == state {
					topts.ParamsRun.Clients.Log.Infof("follow-up %s reached the state %s on pipelinerun %s",
						id, state, runs[i].GetName())
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for the follow-up %s to reach the state %s", id, state)
		}
		time.Sleep(2 * time.Second)
	}
}

// listDefinitionRuns returns the created runs of a definition.
func listDefinitionRuns(ctx context.Context, t *testing.T, topts *tgitea.TestOpts, definition string) []tektonv1.PipelineRun {
	t.Helper()
	list, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx,
		metav1.ListOptions{LabelSelector: packeys.OriginalPRName + "=" + definition})
	assert.NilError(t, err)
	return list.Items
}

// assertNoDefinitionRuns asserts no run of a definition exists. It is only
// meaningful once the follow-up is consumed (see waitForFollowUpState): the
// state is terminal, so no run of it can be created afterwards.
func assertNoDefinitionRuns(ctx context.Context, t *testing.T, topts *tgitea.TestOpts, definition string) {
	t.Helper()
	prs := listDefinitionRuns(ctx, t, topts, definition)
	assert.Equal(t, 0, len(prs), "the consumed follow-up %s has %d pipelineruns", definition, len(prs))
}

// assertNoDefinitionStatus asserts the commit carries no status for a
// definition: a follow-up whose CEL condition is false leaves no check, like a
// definition that does not match the event. Like assertNoDefinitionRuns, it is
// only meaningful once the follow-up is consumed.
func assertNoDefinitionStatus(t *testing.T, topts *tgitea.TestOpts, definition string) {
	t.Helper()
	statuses, _, err := topts.GiteaCNX.Client().ListStatuses(topts.Opts.Organization, topts.Opts.Repo,
		topts.SHA, forgejo.ListStatusesOption{})
	assert.NilError(t, err)
	for _, status := range statuses {
		if strings.Contains(status.Context, definition) {
			t.Fatalf("a status %q was reported for the skipped definition %s on sha %s", status.Context, definition, topts.SHA)
		}
	}
}

// waitForDefinitionStatus polls until the commit carries a status for a
// definition and returns it: how a skipped or cancelled follow-up leaves its
// one and only check.
func waitForDefinitionStatus(t *testing.T, topts *tgitea.TestOpts, definition string) forgejo.Status {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		statuses, _, err := topts.GiteaCNX.Client().ListStatuses(topts.Opts.Organization, topts.Opts.Repo,
			topts.SHA, forgejo.ListStatusesOption{})
		assert.NilError(t, err)
		for _, status := range statuses {
			if strings.Contains(status.Context, definition) {
				return *status
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for a status of the definition %s on sha %s", definition, topts.SHA)
		}
		time.Sleep(2 * time.Second)
	}
}

// assertNoPipelineRunsCreated makes sure nothing was ever started: a fatal
// graph problem must reject the whole event rather than start it halfway.
func assertNoPipelineRunsCreated(ctx context.Context, t *testing.T, topts *tgitea.TestOpts) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
		assert.NilError(t, err)
		if len(prs.Items) > 0 {
			t.Fatalf("no pipelinerun should have been created for a broken follow-up graph, got %d", len(prs.Items))
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// definitionRun returns the run of a definition among the given runs, or nil.
func definitionRun(prs []tektonv1.PipelineRun, definition string) *tektonv1.PipelineRun {
	for i := range prs {
		if prs[i].GetAnnotations()[packeys.OriginalPRName] == definition {
			return &prs[i]
		}
	}
	return nil
}

// paramValue returns the string value of a spec param of a run.
//
//nolint:unparam // every fixture's param is named image, the parameter keeps the helper generic
func paramValue(pr *tektonv1.PipelineRun, name string) string {
	for _, param := range pr.Spec.Params {
		if param.Name == name {
			return param.Value.StringVal
		}
	}
	return ""
}

// TestGiteaPipelinerunFollowupsChainWithResults covers the happy path: the
// follow-up is stored as a pending follow-up on its dependency at event time, its
// run is created once at creation when the dependency succeeds, and the
// dependency results are injected into its params.
func TestGiteaPipelinerunFollowupsChainWithResults(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-chain.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// build sleeps long enough for the pending state of the follow-up to
	// be observed before it completes.
	followUp := waitForPendingFollowUp(ctx, t, topts, "deploy")
	assert.DeepEqual(t, []string{"build"}, followUp.Deps)

	// the follow-up waits as a stored follow-up, not as a run: no deploy run
	// exists while its dependency is still running
	assert.Equal(t, 0, len(listDefinitionRuns(ctx, t, topts, "deploy")))

	// the follow-up is stored on the run of its dependency, in the same group
	builds := listDefinitionRuns(ctx, t, topts, "build")
	assert.Assert(t, len(builds) == 1, "expected one build run, got %d", len(builds))
	groupID := builds[0].Labels[packeys.GroupID]
	assert.Assert(t, groupID != "", "the build run carries no group label")

	prs, err := twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)

	deploy := definitionRun(prs, "deploy")
	assert.Assert(t, deploy != nil, "no deploy run found among %d finished runs", len(prs))
	// the run is created once, at creation, with the deterministic
	// name the follow-up was given at event time, and in the same group
	assert.Equal(t, followUp.ID, deploy.GetName())
	assert.Equal(t, groupID, deploy.Labels[packeys.GroupID])
	assert.Equal(t, "Succeeded", deploy.Status.GetCondition(apis.ConditionSucceeded).Reason)
	// the dependency result was injected into the param, not left as a placeholder
	assert.Equal(t, "registry.example.com/app@sha256:followupse2e", paramValue(deploy, "image"))

	topts.CheckForStatus = "success"
	topts.CheckForNumberStatus = 2
	tgitea.WaitForStatus(t, topts, topts.SHA, "", false)
}

// TestGiteaPipelinerunFollowupsSkippedWhenDependencyFails covers the skip
// path: when the dependency fails and the CEL condition of the follow-up
// requires its success, no run is ever created for the follow-up and no
// status is reported for it — the condition is the pipeline author's, like
// any on-cel-expression, and a false one leaves no check.
func TestGiteaPipelinerunFollowupsSkippedWhenDependencyFails(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-fail.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	followUp := waitForPendingFollowUp(ctx, t, topts, "deploy")

	prs, err := twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)

	build := definitionRun(prs, "build")
	assert.Assert(t, build != nil, "no build run found among %d finished runs", len(prs))
	assert.Equal(t, "Failed", build.Status.GetCondition(apis.ConditionSucceeded).Reason)

	// the follow-up is skipped, never created: no run of it exists, and no
	// status of it is reported either
	waitForFollowUpState(ctx, t, topts, followUp.ID, prgroup.StateSkipped)
	assertNoDefinitionRuns(ctx, t, topts, "deploy")
	assertNoDefinitionStatus(t, topts, "deploy")
}

// TestGiteaPipelinerunFollowupsSkippedWhenDependencyNotStarted covers the
// event-time skip: a dependency that does not match the event leaves the
// follow-up unsatisfiable, and a skipped check is posted for it right away —
// the only check a waiting follow-up gets before its creation.
func TestGiteaPipelinerunFollowupsSkippedWhenDependencyNotStarted(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-missing-dep.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// the started dependency runs and succeeds on its own
	prs, err := twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)
	assert.Equal(t, "Succeeded", definitionRun(prs, "tests").Status.GetCondition(apis.ConditionSucceeded).Reason)

	// the skipped check is posted at event time, not on a later reconcile
	status := waitForDefinitionStatus(t, topts, "deploy")
	assert.Equal(t, "Skipped", status.Description)

	// neither the unmatched dependency nor the skipped follow-up ever runs
	assert.Equal(t, 0, len(listDefinitionRuns(ctx, t, topts, "build")))
	assert.Equal(t, 0, len(listDefinitionRuns(ctx, t, topts, "deploy")))
}

// TestGiteaPipelinerunFollowupsDeepChain covers chained follow-ups: a
// follow-up waiting on another follow-up is created only once that one has
// itself been created and finished.
func TestGiteaPipelinerunFollowupsDeepChain(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-deep-chain.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	prs, err := twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 3,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)

	build := definitionRun(prs, "build")
	tests := definitionRun(prs, "tests")
	deploy := definitionRun(prs, "deploy")
	for name, pr := range map[string]*tektonv1.PipelineRun{"build": build, "tests": tests, "deploy": deploy} {
		assert.Assert(t, pr != nil, "no %s run found among %d finished runs", name, len(prs))
		assert.Equal(t, "Succeeded", pr.Status.GetCondition(apis.ConditionSucceeded).Reason)
	}

	// every follow-up starts only after the run it waits on has completed;
	// each run sleeps so a creation that jumped the gun is caught. The
	// timestamps are second-granularity, so the start of a follow-up created
	// right after a completion can share its second: compare not-before.
	assert.Assert(t, !tests.Status.StartTime.Before(build.Status.CompletionTime),
		"tests started at %s, before build completed at %s", tests.Status.StartTime, build.Status.CompletionTime)
	assert.Assert(t, !deploy.Status.StartTime.Before(tests.Status.CompletionTime),
		"deploy started at %s, before tests completed at %s", deploy.Status.StartTime, tests.Status.CompletionTime)
}

// TestGiteaPipelinerunFollowupsValidationErrors covers the static rejection:
// a dependency loop is reported as a validation error on the provider and no
// PipelineRun of the event is started at all.
func TestGiteaPipelinerunFollowupsValidationErrors(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-cycle.yaml"},
		ExpectEvents: true,
		Regexp:       regexp.MustCompile(`is part of a dependency loop`),
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	assertNoPipelineRunsCreated(ctx, t, topts)
}

// TestGiteaPipelinerunFollowupsTestCommandRejected covers the /test of a
// follow-up definition: it is rejected with an explanatory comment, since a
// follow-up cannot run outside a group, and no new run is created.
func TestGiteaPipelinerunFollowupsTestCommandRejected(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:          triggertype.PullRequest.String(),
		YAMLFiles:            map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-chain.yaml"},
		CheckForStatus:       "success",
		CheckForNumberStatus: 2,
		ExpectEvents:         false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	tgitea.PostCommentOnPullRequest(t, topts, "/test deploy")
	topts.Regexp = regexp.MustCompile(`cannot test the pipelinerun deploy.*it is a follow-up`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	// no new run of the follow-up was started by the rejected /test: the
	// comment above is posted by the same event handling that would have
	// created the run, so observing it closes the window
	assert.Equal(t, 1, len(listDefinitionRuns(ctx, t, topts, "deploy")))
}

// TestGiteaPipelinerunFollowupsCancelWhileWaiting covers cancelling a waiting
// follow-up with /cancel: the pending follow-up is consumed as cancelled on its
// run it is stored on, and no run is ever created for the follow-up, not even when its
// dependency later succeeds.
func TestGiteaPipelinerunFollowupsCancelWhileWaiting(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-cancel.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// build sleeps long enough for the follow-up to be cancelled while it waits
	followUp := waitForPendingFollowUp(ctx, t, topts, "deploy")
	tgitea.PostCommentOnPullRequest(t, topts, "/cancel deploy")

	// the follow-up is consumed as cancelled on the run it is stored on: no run of the
	// follow-up exists to patch, so the cancel lands on the stored follow-up
	waitForFollowUpState(ctx, t, topts, followUp.ID, prgroup.StateCancelled)

	// the cancelled check is posted: the only trace of a follow-up that
	// waited without one
	status := waitForDefinitionStatus(t, topts, "deploy")
	assert.Equal(t, forgejo.StatusWarning, status.State)
	assert.Equal(t, "Cancelled", status.Description)

	// the dependency later succeeds: its run is finished and reported
	prs, err := twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)
	build := definitionRun(prs, "build")
	assert.Assert(t, build != nil, "no build run found among %d finished runs", len(prs))
	assert.Equal(t, "Succeeded", build.Status.GetCondition(apis.ConditionSucceeded).Reason)

	// the cancelled follow-up stays dead: no run is created for it once its
	// group completes. The evaluation of the group happens in the same
	// reconcile that reports the dependency state the wait above polls on, so
	// by the time it returns the guard has already been exercised; the extra
	// window makes a late resurrection visible too.
	deadline := time.Now().Add(30 * time.Second)
	for {
		assert.Equal(t, 0, len(listDefinitionRuns(ctx, t, topts, "deploy")),
			"the cancelled follow-up was resurrected")
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}
}

// TestGiteaPipelinerunFollowupsCancelInProgress covers cancel-in-progress over
// a waiting follow-up: a newer event on the same pull request cancels the
// dependency runs of the superseded one, the follow-up waiting on them is
// consumed as cancelled with its check posted, and the newer event's own
// follow-up is left alone to run in its fresh group.
func TestGiteaPipelinerunFollowupsCancelInProgress(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-cancel-in-progress.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// build sleeps long enough for the newer event to cancel it while the
	// follow-up is still waiting on it
	followUp := waitForPendingFollowUp(ctx, t, topts, "deploy")

	// a newer event on the same pull request: cancel-in-progress is enabled
	// by the build annotation, so the superseded runs are cancelled
	tgitea.PostCommentOnPullRequest(t, topts, "/test")

	// the follow-up of the superseded event is consumed as cancelled on the
	// run it is stored on
	waitForFollowUpState(ctx, t, topts, followUp.ID, prgroup.StateCancelled)

	// the cancelled check is posted, the only trace of a follow-up that
	// waited without one
	status := waitForDefinitionStatus(t, topts, "deploy")
	assert.Equal(t, forgejo.StatusWarning, status.State)
	assert.Equal(t, "Cancelled", status.Description)

	// the newer event forms a fresh group: its build runs to success and its
	// own follow-up is created from it. All three runs of the two events must
	// finish: the cancelled build counts as finished too, so a minimum of two
	// would return while the deploy is still running.
	prs, err := twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 3,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)
	assert.Assert(t, definitionRun(prs, "build") != nil, "no build run found among %d finished runs", len(prs))

	// the superseded build was cancelled, the newer one succeeded
	builds := listDefinitionRuns(ctx, t, topts, "build")
	assert.Equal(t, 2, len(builds))
	reasons := map[string]int{}
	for i := range builds {
		reasons[builds[i].Status.GetCondition(apis.ConditionSucceeded).Reason]++
	}
	assert.Equal(t, 1, reasons["Cancelled"])
	assert.Equal(t, 1, reasons["Succeeded"])

	// the follow-up ran exactly once, for the newer group, under the
	// deterministic name of its own group and not the superseded one's
	deploys := listDefinitionRuns(ctx, t, topts, "deploy")
	assert.Equal(t, 1, len(deploys))
	assert.Equal(t, "Succeeded", deploys[0].Status.GetCondition(apis.ConditionSucceeded).Reason)
	assert.Assert(t, deploys[0].GetName() != followUp.ID,
		"the deploy run %s reused the superseded group name", deploys[0].GetName())
	assert.Equal(t, "registry.example.com/app@sha256:followupse2e", paramValue(&deploys[0], "image"))
}

// TestGiteaPipelinerunFollowupsTestAllRetriggersGroup covers the failure
// analysis pattern: a follow-up created by a failed dependency, and a bare
// /test re-triggering the whole group — every dependency runs again and the
// follow-up is re-created on the new runs.
func TestGiteaPipelinerunFollowupsTestAllRetriggersGroup(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-analysis.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// first event: the build fails and the analysis follow-up, which waits
	// on that failure, is created and succeeds
	prs, err := twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)
	assert.Equal(t, "Failed", definitionRun(prs, "build").Status.GetCondition(apis.ConditionSucceeded).Reason)
	analysis := definitionRun(prs, "analysis")
	assert.Assert(t, analysis != nil, "no analysis run found among %d finished runs", len(prs))
	assert.Equal(t, "Succeeded", analysis.Status.GetCondition(apis.ConditionSucceeded).Reason)

	// a bare /test re-runs every definition of the event: a second round of
	// both the build and the follow-up waiting on it
	tgitea.PostCommentOnPullRequest(t, topts, "/test")

	prs, err = twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 4,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)
	counts := map[string]int{}
	for i := range prs {
		counts[prs[i].GetAnnotations()[packeys.OriginalPRName]]++
	}
	assert.Equal(t, 2, counts["build"])
	assert.Equal(t, 2, counts["analysis"])
}

// TestGiteaPipelinerunFollowupsRetestAfterPartialSuccess covers /retest on an
// event where one dependency succeeded and another failed: the failed
// dependency is rerun, the follow-up is not re-created since its group cannot
// fully re-form around the succeeded dependency, and — most of all — the
// retest is not rejected as a broken graph.
func TestGiteaPipelinerunFollowupsRetestAfterPartialSuccess(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-retest.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// first event: build succeeds, tests fails, and deploy is skipped since
	// its CEL condition requires both to succeed
	followUp := waitForPendingFollowUp(ctx, t, topts, "deploy")
	prs, err := twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)
	assert.Equal(t, "Succeeded", definitionRun(prs, "build").Status.GetCondition(apis.ConditionSucceeded).Reason)
	assert.Equal(t, "Failed", definitionRun(prs, "tests").Status.GetCondition(apis.ConditionSucceeded).Reason)
	waitForFollowUpState(ctx, t, topts, followUp.ID, prgroup.StateSkipped)
	assertNoDefinitionRuns(ctx, t, topts, "deploy")
	assertNoDefinitionStatus(t, topts, "deploy")

	tgitea.PostCommentOnPullRequest(t, topts, "/retest")

	// the retest is accepted: a second run of the failed dependency is
	// created and finishes, which would not happen if the follow-up graph
	// were taken for broken on a retest
	prs, err = twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 3,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)
	numTestsRuns := 0
	for i := range prs {
		if prs[i].GetAnnotations()[packeys.OriginalPRName] == "tests" {
			numTestsRuns++
		}
	}
	assert.Equal(t, 2, numTestsRuns)

	// the follow-up was not re-created: one of its dependencies already
	// succeeded and is not rerun, so the retest drops it at event time
	assert.Equal(t, 0, len(listDefinitionRuns(ctx, t, topts, "deploy")))
}

// waitForDefinitionRunSucceeded polls until a run of a definition, optionally
// narrowed by an extra label selector, has succeeded, and returns it.
func waitForDefinitionRunSucceeded(ctx context.Context, t *testing.T, topts *tgitea.TestOpts, definition, extraSelector string) *tektonv1.PipelineRun {
	t.Helper()
	selector := packeys.OriginalPRName + "=" + definition
	if extraSelector != "" {
		selector += "," + extraSelector
	}
	deadline := time.Now().Add(twait.DefaultTimeout)
	for {
		list, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx,
			metav1.ListOptions{LabelSelector: selector})
		assert.NilError(t, err)
		for i := range list.Items {
			cond := list.Items[i].Status.GetCondition(apis.ConditionSucceeded)
			if cond != nil && cond.Reason == "Succeeded" {
				return &list.Items[i]
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for a succeeded pipelinerun of the definition %s (selector %s)", definition, selector)
		}
		time.Sleep(2 * time.Second)
	}
}

// TestGiteaPipelinerunFollowupsPushEvent covers a group triggered by a push:
// the definitions match the push to the default branch like any push
// PipelineRun, and the follow-up of the push event runs after them, with the
// results of the push runs of its dependencies.
func TestGiteaPipelinerunFollowupsPushEvent(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  "pull_request, push",
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-chain.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// the pull request event runs the whole group first: its follow-up is
	// created and finishes with the results of its dependency
	prs, err := twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)
	prDeploy := definitionRun(prs, "deploy")
	assert.Assert(t, prDeploy != nil, "no deploy run found among %d finished runs", len(prs))
	assert.Equal(t, "Succeeded", prDeploy.Status.GetCondition(apis.ConditionSucceeded).Reason)
	assert.Equal(t, "registry.example.com/app@sha256:followupse2e", paramValue(prDeploy, "image"))

	// merging the pull request triggers the push event on the default branch
	merged, resp, err := topts.GiteaCNX.Client().MergePullRequest(
		topts.Opts.Organization, topts.Opts.Repo, topts.PullRequest.Index,
		forgejo.MergePullRequestOption{
			Title: "Merged with Panache",
			Style: "merge",
		},
	)
	assert.NilError(t, err)
	assert.Assert(t, resp.StatusCode < 400, resp)
	assert.Assert(t, merged)

	// the push event runs the whole group again: its own follow-up run, of
	// the push event type, is created after the push runs of its dependencies
	// and finishes with their results
	pushDeploy := waitForDefinitionRunSucceeded(ctx, t, topts, "deploy", packeys.EventType+"=push")
	assert.Equal(t, "registry.example.com/app@sha256:followupse2e", paramValue(pushDeploy, "image"))

	// the push group is complete: its build run ran too, and the follow-up
	// was not created twice for the push event
	pushBuilds, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx,
		metav1.ListOptions{LabelSelector: packeys.OriginalPRName + "=build," + packeys.EventType + "=push"})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(pushBuilds.Items), "the push event should have one build run")
	pushDeploys, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx,
		metav1.ListOptions{LabelSelector: packeys.OriginalPRName + "=deploy," + packeys.EventType + "=push"})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(pushDeploys.Items), "the push event should have one deploy run")
}

// TestGiteaPipelinerunFollowupsConcurrencyLimit covers follow-ups under a
// repository concurrency limit: the waiting follow-ups hold no slot, and the
// created runs join the queue like any other run, so the limit is respected
// across the whole group.
func TestGiteaPipelinerunFollowupsConcurrencyLimit(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:      triggertype.PullRequest.String(),
		YAMLFiles:        map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-concurrency.yaml"},
		ConcurrencyLimit: new(1),
		ExpectEvents:     false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// both follow-ups wait on the build without any footprint: while the
	// build runs, it is the only run of the namespace
	followUp := waitForPendingFollowUp(ctx, t, topts, "deploy")
	_ = waitForPendingFollowUp(ctx, t, topts, "notify")
	list, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(list.Items), "the waiting follow-ups must not exist as pipelineruns")

	// once the build is done, both follow-up runs are created and queued:
	// with a limit of 1 they run one after the other
	prs, err := twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 3,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)
	for _, definition := range []string{"build", "deploy", "notify"} {
		run := definitionRun(prs, definition)
		assert.Assert(t, run != nil, "no %s run found among %d finished runs", definition, len(prs))
		assert.Equal(t, "Succeeded", run.Status.GetCondition(apis.ConditionSucceeded).Reason)
	}
	assert.Equal(t, followUp.ID, definitionRun(prs, "deploy").GetName())

	// the follow-up runs joined the queue: no two runs of the group, the
	// event-started one and the watcher-created ones, ever ran at the same time
	tgitea.AssertConcurrencyRespected(ctx, t, topts, 1)
}

// TestGiteaPipelinerunFollowupsPrunedDependency covers a dependency that
// disappears from the cluster while the follow-up waits on it, like a run
// pruned past its max-keep: it surfaces as the Error status in the runs map,
// and the CEL condition of the follow-up decides on it.
func TestGiteaPipelinerunFollowupsPrunedDependency(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-pruned.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// the follow-up waits on the build, which sleeps long enough for the
	// tests run to finish and be removed first
	followUp := waitForPendingFollowUp(ctx, t, topts, "deploy")
	testsRun := waitForDefinitionRunSucceeded(ctx, t, topts, "tests", "")

	// the completed tests run disappears from the cluster while the follow-up
	// is still waiting on it
	assert.NilError(t, topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).Delete(
		ctx, testsRun.GetName(), metav1.DeleteOptions{},
	))

	// the build later finishes: the follow-up resolves the missing tests as
	// Error, its CEL condition is true, and its run is created
	deploy := waitForDefinitionRunSucceeded(ctx, t, topts, "deploy", "")
	assert.Equal(t, followUp.ID, deploy.GetName())
}
