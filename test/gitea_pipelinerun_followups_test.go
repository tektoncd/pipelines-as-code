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
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/configmap"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	pacrepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

// startFollowupsPR starts a PR test with follow-ups enabled in the ConfigMap.
func startFollowupsPR(t *testing.T, topts *tgitea.TestOpts) (context.Context, func()) {
	t.Helper()
	ctx := context.Background()
	topts.TargetRefName = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
	topts.TargetNS = topts.TargetRefName
	var err error
	topts.ParamsRun, topts.Opts, topts.GiteaCNX, err = tgitea.Setup(ctx)
	assert.NilError(t, err)
	assert.NilError(t, topts.ParamsRun.Clients.NewClients(ctx, &topts.ParamsRun.Info))
	ctx, err = cctx.GetControllerCtxInfo(ctx, topts.ParamsRun)
	assert.NilError(t, err)
	restore := configmap.ChangeGlobalConfig(ctx, t, topts.ParamsRun, "pipelines-as-code",
		map[string]string{"enable-follow-up-runs": "true"})
	t.Cleanup(restore)
	assert.NilError(t, pacrepo.CreateNS(ctx, topts.TargetNS, topts.ParamsRun))
	return tgitea.TestPR(t, topts)
}

// waitForPendingFollowUp polls until a run stores a pending follow-up for the
// definition. The dependency must still be running, or the follow-up may
// already be consumed.
//
//nolint:unparam // every fixture's follow-up is named deploy, the parameter keeps the helper generic
func waitForPendingFollowUp(ctx context.Context, t *testing.T, topts *tgitea.TestOpts, definition string) prgroup.FollowUp {
	t.Helper()
	return waitForStoredFollowUp(ctx, t, topts, definition, true)
}

// waitForFollowUp returns the follow-up in any state.
//
//nolint:unparam // every fixture's follow-up is named deploy, the parameter keeps the helper generic
func waitForFollowUp(ctx context.Context, t *testing.T, topts *tgitea.TestOpts, definition string) prgroup.FollowUp {
	t.Helper()
	return waitForStoredFollowUp(ctx, t, topts, definition, false)
}

func waitForStoredFollowUp(ctx context.Context, t *testing.T, topts *tgitea.TestOpts, definition string, pendingOnly bool) prgroup.FollowUp {
	t.Helper()
	deadline := time.Now().Add(twait.DefaultTimeout)
	for {
		runs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
		assert.NilError(t, err)
		for i := range runs.Items {
			followUps, err := prgroup.FollowUpsOf(&runs.Items[i])
			assert.NilError(t, err)
			for _, followUp := range followUps {
				if followUp.Name == definition && (!pendingOnly || followUp.Pending()) {
					topts.ParamsRun.Clients.Log.Infof("follow-up %s is stored as follow-up %s on pipelinerun %s",
						definition, followUp.ID, runs.Items[i].GetName())
					return followUp
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for a stored follow-up %s on the runs of the namespace", definition)
		}
		time.Sleep(2 * time.Second)
	}
}

// waitForFollowUpState polls until the follow-up is stored in the given state.
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

// assertNoDefinitionRuns asserts no run of a definition exists. Call it once
// the follow-up is consumed.
func assertNoDefinitionRuns(ctx context.Context, t *testing.T, topts *tgitea.TestOpts, definition string) {
	t.Helper()
	prs := listDefinitionRuns(ctx, t, topts, definition)
	assert.Equal(t, 0, len(prs), "the consumed follow-up %s has %d pipelineruns", definition, len(prs))
}

// assertNoDefinitionStatus asserts the commit has no status for a definition.
// Call it once the follow-up is consumed.
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

// waitForDefinitionStatus polls until the commit has a status for a definition.
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

// assertNoPipelineRunsCreated asserts nothing was started.
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
// follow-up waits stored on its dependency, then runs with its results.
func TestGiteaPipelinerunFollowupsChainWithResults(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-chain.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// build sleeps long enough to observe the pending follow-up
	followUp := waitForPendingFollowUp(ctx, t, topts, "deploy")
	assert.DeepEqual(t, []string{"build"}, followUp.Deps)

	// no deploy run exists while its dependency runs
	assert.Equal(t, 0, len(listDefinitionRuns(ctx, t, topts, "deploy")))

	// stored on the dependency's run, in the same group
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
	// created once, with the deterministic name, in the same group
	assert.Equal(t, followUp.ID, deploy.GetName())
	assert.Equal(t, groupID, deploy.Labels[packeys.GroupID])
	assert.Equal(t, "Succeeded", deploy.Status.GetCondition(apis.ConditionSucceeded).Reason)
	// the dependency result is injected, not left as a placeholder
	assert.Equal(t, "registry.example.com/app@sha256:followupse2e", paramValue(deploy, "image"))

	topts.CheckForStatus = "success"
	topts.CheckForNumberStatus = 2
	tgitea.WaitForStatus(t, topts, topts.SHA, "", false)
}

// TestGiteaPipelinerunFollowupsSkippedWhenDependencyFails covers a failed
// dependency whose CEL condition requires success: no run and no check.
func TestGiteaPipelinerunFollowupsSkippedWhenDependencyFails(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-fail.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	followUp := waitForFollowUp(ctx, t, topts, "deploy")

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

	// skipped: no run and no status
	waitForFollowUpState(ctx, t, topts, followUp.ID, prgroup.StateSkipped)
	assertNoDefinitionRuns(ctx, t, topts, "deploy")
	assertNoDefinitionStatus(t, topts, "deploy")
}

// TestGiteaPipelinerunFollowupsSkippedWhenDependencyNotStarted covers a
// dependency that does not match the event: a skipped check is posted at once.
func TestGiteaPipelinerunFollowupsSkippedWhenDependencyNotStarted(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-missing-dep.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// the started dependency still runs
	prs, err := twait.UntilPipelineRunsFinished(ctx, topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.SHA},
	})
	assert.NilError(t, err)
	assert.Equal(t, "Succeeded", definitionRun(prs, "tests").Status.GetCondition(apis.ConditionSucceeded).Reason)

	// the skipped check is posted at event time
	status := waitForDefinitionStatus(t, topts, "deploy")
	assert.Equal(t, "Skipped", status.Description)

	// neither the unmatched dependency nor the follow-up runs
	assert.Equal(t, 0, len(listDefinitionRuns(ctx, t, topts, "build")))
	assert.Equal(t, 0, len(listDefinitionRuns(ctx, t, topts, "deploy")))
}

// TestGiteaPipelinerunFollowupsValidationErrors covers a follow-up waiting on
// another follow-up: a validation error, and no run is started.
func TestGiteaPipelinerunFollowupsValidationErrors(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-cycle.yaml"},
		ExpectEvents: true,
		Regexp:       regexp.MustCompile(`is itself a follow-up`),
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	assertNoPipelineRunsCreated(ctx, t, topts)
}

// TestGiteaPipelinerunFollowupsTestCommandRejected covers /test of a
// follow-up: rejected with a comment, no run created.
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

	// the rejection comment is posted by the handler that would create the run
	assert.Equal(t, 1, len(listDefinitionRuns(ctx, t, topts, "deploy")))
}

// TestGiteaPipelinerunFollowupsCancelWhileWaiting covers /cancel on a waiting
// follow-up: it is consumed as cancelled and never runs.
func TestGiteaPipelinerunFollowupsCancelWhileWaiting(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-cancel.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// build sleeps long enough to cancel the follow-up while it waits
	followUp := waitForPendingFollowUp(ctx, t, topts, "deploy")
	tgitea.PostCommentOnPullRequest(t, topts, "/cancel deploy")

	// consumed as cancelled on its stored run
	waitForFollowUpState(ctx, t, topts, followUp.ID, prgroup.StateCancelled)

	// the cancelled check is the only trace of the follow-up
	status := waitForDefinitionStatus(t, topts, "deploy")
	assert.Equal(t, forgejo.StatusWarning, status.State)
	assert.Equal(t, "Cancelled", status.Description)

	// the dependency later succeeds
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

	// the cancelled follow-up is not resurrected once the group completes
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

// TestGiteaPipelinerunFollowupsCancelInProgress covers a newer event
// superseding a waiting follow-up: it is cancelled with a check, and the newer
// event's follow-up is left alone.
func TestGiteaPipelinerunFollowupsCancelInProgress(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-cancel-in-progress.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// build sleeps long enough for the newer event to supersede it
	followUp := waitForPendingFollowUp(ctx, t, topts, "deploy")

	// a newer event on the same pull request supersedes the runs
	tgitea.PostCommentOnPullRequest(t, topts, "/test")

	// the superseded follow-up is consumed as cancelled
	waitForFollowUpState(ctx, t, topts, followUp.ID, prgroup.StateCancelled)

	// the cancelled check is posted
	status := waitForDefinitionStatus(t, topts, "deploy")
	assert.Equal(t, forgejo.StatusWarning, status.State)
	assert.Equal(t, "Cancelled", status.Description)

	// the newer group runs to success. All three runs must finish: the cancelled
	// build counts as finished too.
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

	// the follow-up ran once, under the newer group's name
	deploys := listDefinitionRuns(ctx, t, topts, "deploy")
	assert.Equal(t, 1, len(deploys))
	assert.Equal(t, "Succeeded", deploys[0].Status.GetCondition(apis.ConditionSucceeded).Reason)
	assert.Assert(t, deploys[0].GetName() != followUp.ID,
		"the deploy run %s reused the superseded group name", deploys[0].GetName())
	assert.Equal(t, "registry.example.com/app@sha256:followupse2e", paramValue(&deploys[0], "image"))
}

// TestGiteaPipelinerunFollowupsTestAllRetriggersGroup covers a bare /test
// re-running the whole group, follow-up included.
func TestGiteaPipelinerunFollowupsTestAllRetriggersGroup(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-analysis.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// first event: the build fails and the follow-up waiting on it runs
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

	// /test runs a second round of the build and the follow-up
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

// TestGiteaPipelinerunFollowupsRetestAfterPartialSuccess covers /retest after
// one dependency succeeded and another failed: the failed one reruns, the
// follow-up is dropped, and the retest is not rejected.
func TestGiteaPipelinerunFollowupsRetestAfterPartialSuccess(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  triggertype.PullRequest.String(),
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-retest.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// first event: tests fails, so deploy is skipped
	followUp := waitForFollowUp(ctx, t, topts, "deploy")
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

	// the retest is accepted: the failed dependency runs again
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

	// the succeeded dependency is not rerun, so the follow-up is dropped
	assert.Equal(t, 0, len(listDefinitionRuns(ctx, t, topts, "deploy")))
}

// waitForDefinitionRunSucceeded polls until a run of a definition succeeded.
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

// TestGiteaPipelinerunFollowupsPushEvent covers a group triggered by a push,
// with the follow-up using the push runs' results.
func TestGiteaPipelinerunFollowupsPushEvent(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:  "pull_request, push",
		YAMLFiles:    map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-chain.yaml"},
		ExpectEvents: false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// the pull request event runs the whole group first
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

	// merging triggers the push event
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

	// the push group runs again, with its own follow-up run
	pushDeploy := waitForDefinitionRunSucceeded(ctx, t, topts, "deploy", packeys.EventType+"=push")
	assert.Equal(t, "registry.example.com/app@sha256:followupse2e", paramValue(pushDeploy, "image"))

	// the push group is complete, the follow-up ran once
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
// concurrency limit: waiting follow-ups hold no slot, created runs queue.
func TestGiteaPipelinerunFollowupsConcurrencyLimit(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:      triggertype.PullRequest.String(),
		YAMLFiles:        map[string]string{".tekton/pipelinerun.yaml": "testdata/pipelinerun-followups-concurrency.yaml"},
		ConcurrencyLimit: new(1),
		ExpectEvents:     false,
	}
	ctx, f := startFollowupsPR(t, topts)
	defer f()

	// both follow-ups wait without a footprint
	followUp := waitForPendingFollowUp(ctx, t, topts, "deploy")
	_ = waitForPendingFollowUp(ctx, t, topts, "notify")
	list, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Equal(t, 1, len(list.Items), "the waiting follow-ups must not exist as pipelineruns")

	// once the build is done, both follow-up runs are queued
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

	// the runs of the group never ran at the same time
	tgitea.AssertConcurrencyRespected(ctx, t, topts, 1)
}

// TestGiteaPipelinerunFollowupsPrunedDependency covers a dependency pruned
// while the follow-up waits: it shows as Error, and the CEL condition decides.
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
