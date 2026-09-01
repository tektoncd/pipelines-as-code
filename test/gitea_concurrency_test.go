//go:build e2e

package test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v90/github"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pacapi "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	tkubestuff "github.com/openshift-pipelines/pipelines-as-code/test/pkg/kubestuff"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	pacrepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	secret "github.com/openshift-pipelines/pipelines-as-code/test/pkg/secret"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"

	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// don't test concurrency limit here, just parallel pipeline.
func TestGiteaMultiplesParallelPipelines(t *testing.T) {
	maxParallel := 10
	yamlFiles := map[string]string{}
	for i := 0; i < maxParallel; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun.yaml"
	}
	topts := &tgitea.TestOpts{
		Regexp:               tgitea.SuccessRegexp,
		TargetEvent:          triggertype.PullRequest.String(),
		YAMLFiles:            yamlFiles,
		CheckForStatus:       "success",
		CheckForNumberStatus: maxParallel,
		ExpectEvents:         false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

// multiple pipelineruns in the same .tekton directory and a concurrency of 1.
func TestGiteaConcurrencyExclusivenessMultiplePipelines(t *testing.T) {
	numPipelines := 10
	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun.yaml"
	}
	topts := &tgitea.TestOpts{
		Regexp:               tgitea.SuccessRegexp,
		TargetEvent:          triggertype.PullRequest.String(),
		YAMLFiles:            yamlFiles,
		CheckForStatus:       "success",
		CheckForNumberStatus: numPipelines,
		ConcurrencyLimit:     github.Ptr(1),
		ExpectEvents:         false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	tgitea.AssertConcurrencyRespected(context.Background(), t, topts, 1)
	tkubestuff.SnapshotWatcherHealth(context.Background(), t, topts.ParamsRun).Assert(context.Background(), t)
}

// multiple push to the same  repo, concurrency should q them.
func TestGiteaConcurrencyExclusivenessMultipleRuns(t *testing.T) {
	numPipelines := 1
	topts := &tgitea.TestOpts{
		TargetEvent:          triggertype.PullRequest.String(),
		YAMLFiles:            map[string]string{".tekton/pr.yaml": "testdata/pipelinerun.yaml"},
		CheckForNumberStatus: numPipelines,
		ConcurrencyLimit:     github.Ptr(1),
		ExpectEvents:         false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.DefaultBranch,
		PushForce:     true,
	}
	processed, err := payload.ApplyTemplate("testdata/pipelinerun-alt.yaml", map[string]string{
		"TargetNamespace": topts.TargetNS,
		"TargetBranch":    topts.DefaultBranch,
		"TargetEvent":     topts.TargetEvent,
		"PipelineName":    "pr",
		"Command":         "sleep 10",
	})
	assert.NilError(t, err)
	entries := map[string]string{".tekton/pr.yaml": processed}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	processed, err = payload.ApplyTemplate("testdata/pipelinerun-alt.yaml", map[string]string{
		"TargetNamespace": topts.TargetNS,
		"TargetBranch":    topts.DefaultBranch,
		"TargetEvent":     topts.TargetEvent,
		"PipelineName":    "pr",
		"Command":         "echo SUCCESS",
	})
	assert.NilError(t, err)
	entries = map[string]string{".tekton/pr.yaml": processed}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)

	// loop until we get the status
	gotPipelineRunPending := false
	for i := 0; i < 30; i++ {
		prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
		assert.NilError(t, err)

		// range over prs
		for _, pr := range prs.Items {
			// check for status
			status := pr.Spec.Status
			if status == "PipelineRunPending" {
				gotPipelineRunPending = true
				break
			}
		}
		if gotPipelineRunPending {
			topts.ParamsRun.Clients.Log.Info("Found PipelineRunPending in PipelineRuns")
			break
		}
		time.Sleep(5 * time.Second)
	}
	if !gotPipelineRunPending {
		t.Fatalf("Did not find PipelineRunPending in PipelineRuns")
	}

	topts.CheckForStatus = "success"
	tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, "", false)

	topts.Regexp = tgitea.SuccessRegexp
	tgitea.WaitForPullRequestCommentMatch(t, topts)
}

func TestGiteaConcurrencyOrderedExecution(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelineruns-ordered-execution.yaml",
		},
		CheckForStatus:       "success",
		CheckForNumberStatus: 3,
		ConcurrencyLimit:     github.Ptr(1),
		ExpectEvents:         false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	prs, err := twait.UntilPipelineRunsFinished(context.Background(), topts.ParamsRun.Clients, twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 3,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.PullRequest.Head.Sha},
	})
	assert.NilError(t, err)

	sort.PipelineRunSortByCompletionTime(prs)
	assert.Assert(t, strings.HasPrefix(prs[len(prs)-1].Name, "abc"))
	assert.Assert(t, strings.HasPrefix(prs[len(prs)-2].Name, "pqr"))
	assert.Assert(t, strings.HasPrefix(prs[len(prs)-3].Name, "xyz"))

	tgitea.AssertConcurrencyRespected(context.Background(), t, topts, 1)
}

func TestGiteaConfigMaxKeepRun(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun-max-keep-run-1.yaml",
		},
		CheckForStatus: "success",
		ExpectEvents:   false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	tgitea.PostCommentOnPullRequest(t, topts, "/test")
	tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, "", false)

	waitOpts := twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1, // 1 means 2 🙃
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.PullRequest.Head.Sha},
	}
	_, err := twait.UntilPipelineRunHasReason(context.Background(), topts.ParamsRun.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)

	time.Sleep(15 * time.Second) // "Evil does not sleep. It waits." - Galadriel

	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(context.Background(), metav1.ListOptions{})
	assert.NilError(t, err)

	assert.Equal(t, len(prs.Items), 1, "should have only one pipelinerun, but we have: %d", len(prs.Items))
}

// TestGiteaConfigCancelInProgress will test the pipelinerun annotation
// `pipelinesascode.tekton.dev/cancel-in-progress: "true", it will first start
// one Pull Request which will run a PipelineRun and then send a /retest which
// should cancel the in progress one.
// It will create a new branch and push a new Pull Request with a PipelineRun of
// the same name of the first PR and make sure PipelineRun of the same name only
// acts on the same Pull Request and not on the one of the others.
// TestGiteaGlobalRepoConcurrencyLimit tests the concurrency_limit feature of the PipelineRun.
// It fetches the PipelineRun definition from the default branch of the repository
// as configured on the git platform (e.g., main).
// In this test, the concurrency_limit is enabled using a global repository instead of a local repository.
func TestGiteaGlobalRepoConcurrencyLimit(t *testing.T) {
	numPipelines := 10
	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun.yaml"
	}
	topts := &tgitea.TestOpts{
		TargetEvent:          triggertype.PullRequest.String(),
		YAMLFiles:            yamlFiles,
		CheckForNumberStatus: numPipelines,
		CheckForStatus:       "success",
	}

	tgitea.VerifyConcurrency(t, topts, github.Ptr(2))
}

// TestGiteaGlobalAndLocalRepoConcurrencyLimit verifies the concurrency_limit feature of the PipelineRun,
// ensuring that when concurrency_limit is defined in both global and local repository,
// the local repository limit takes precedence. This end-to-end test confirms that behavior.
func TestGiteaGlobalAndLocalRepoConcurrencyLimit(t *testing.T) {
	numPipelines := 10
	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun.yaml"
	}
	topts := &tgitea.TestOpts{
		TargetEvent:          triggertype.PullRequest.String(),
		YAMLFiles:            yamlFiles,
		CheckForNumberStatus: numPipelines,
		ConcurrencyLimit:     github.Ptr(3),
		CheckForStatus:       "success",
	}

	tgitea.VerifyConcurrency(t, topts, github.Ptr(2))
}

// TestGiteaConcurrencyLimitChangeTakesEffect raises the concurrency limit of a
// Repository while runs are queued behind it, and checks that the queue starts
// admitting more work straight away.
//
// The runs sleep for a long time on purpose. If the first one finished during
// the observation window, its completion alone would wake the queue and the
// test would pass even when the limit change was ignored.
func TestGiteaConcurrencyLimitChangeTakesEffect(t *testing.T) {
	const (
		numPipelines = 5
		raisedLimit  = 5
	)
	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun-alt.yaml"
	}
	topts := &tgitea.TestOpts{
		TargetEvent:      triggertype.PullRequest.String(),
		YAMLFiles:        yamlFiles,
		ExtraArgs:        map[string]string{"Command": "sleep 600"},
		ConcurrencyLimit: github.Ptr(1),
		ExpectEvents:     false,
	}
	ctx, f := tgitea.TestPR(t, topts)
	defer f()

	twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
		fmt.Sprintf("1 running and %d pending pipelineruns", numPipelines-1),
		twait.DefaultTimeout, func(c twait.Counts) bool {
			return c.Running == 1 && c.Pending == numPipelines-1
		})

	health := tkubestuff.SnapshotWatcherHealth(ctx, t, topts.ParamsRun)

	pacrepo.SetConcurrencyLimit(ctx, t, topts.ParamsRun, topts.TargetNS, topts.TargetNS, github.Ptr(raisedLimit))

	counts, _ := twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
		"at least 4 running pipelineruns after raising the limit",
		90*time.Second, func(c twait.Counts) bool {
			return c.Running >= 4
		})

	// If anything managed to finish, a completion could have woken the queue
	// and we would not know whether the limit change did anything.
	assert.Equal(t, counts.Done, 0, "a pipelinerun completed during the observation window, the result is inconclusive")

	health.Assert(ctx, t)
}

// TestGiteaConcurrencyLimitRemoval drops the concurrency limit from a
// Repository entirely while runs are queued behind it.
//
// The watcher health check is the point of this test: without it the test would
// pass against the broken code, because Kubernetes restarts the crashed watcher
// and the runs complete anyway.
func TestGiteaConcurrencyLimitRemoval(t *testing.T) {
	const numPipelines = 5
	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun-alt.yaml"
	}
	topts := &tgitea.TestOpts{
		TargetEvent:      triggertype.PullRequest.String(),
		YAMLFiles:        yamlFiles,
		ExtraArgs:        map[string]string{"Command": "sleep 20"},
		ConcurrencyLimit: github.Ptr(1),
		ExpectEvents:     false,
	}
	ctx, f := tgitea.TestPR(t, topts)
	defer f()

	twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
		"at least 2 pending pipelineruns", twait.DefaultTimeout,
		func(c twait.Counts) bool { return c.Pending >= 2 })

	health := tkubestuff.SnapshotWatcherHealth(ctx, t, topts.ParamsRun)

	pacrepo.SetConcurrencyLimit(ctx, t, topts.ParamsRun, topts.TargetNS, topts.TargetNS, nil)

	twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
		fmt.Sprintf("all %d pipelineruns finished", numPipelines),
		twait.DefaultTimeout, func(c twait.Counts) bool {
			return c.Total == numPipelines && c.Done == numPipelines
		})

	health.Assert(ctx, t)
}

// TestGiteaConcurrencyLimitHoldsWhenStartFails breaks the Git provider call
// that happens after a run has already been started, and checks the limit still
// holds.
//
// PAC un-pends a PipelineRun before it talks to the provider, so once the
// provider call fails the run is already going while PAC thinks it never
// started. Releasing the slot there admits the next run on top of it.
func TestGiteaConcurrencyLimitHoldsWhenStartFails(t *testing.T) {
	const numPipelines = 6
	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun-alt.yaml"
	}
	topts := &tgitea.TestOpts{
		TargetEvent:      triggertype.PullRequest.String(),
		YAMLFiles:        yamlFiles,
		ExtraArgs:        map[string]string{"Command": "sleep 30"},
		ConcurrencyLimit: github.Ptr(1),
		ExpectEvents:     false,
	}
	ctx, f := tgitea.TestPR(t, topts)
	defer f()

	twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
		"the first pipelinerun to be running", twait.DefaultTimeout,
		func(c twait.Counts) bool { return c.Running >= 1 })

	health := tkubestuff.SnapshotWatcherHealth(ctx, t, topts.ParamsRun)

	// Deleting the token makes every subsequent start fail inside the provider
	// client setup, which is exactly the window we care about.
	err := topts.ParamsRun.Clients.Kube.CoreV1().Secrets(topts.TargetNS).Delete(ctx, topts.TargetNS, metav1.DeleteOptions{})
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("deleted the git provider secret in %s, starts will now fail", topts.TargetNS)

	time.Sleep(90 * time.Second)

	assert.NilError(t, secret.Create(ctx, topts.ParamsRun,
		map[string]string{"token": topts.Token}, topts.TargetNS, topts.TargetNS))
	topts.ParamsRun.Clients.Log.Infof("restored the git provider secret in %s", topts.TargetNS)

	twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
		fmt.Sprintf("all %d pipelineruns finished", numPipelines),
		twait.DefaultTimeout, func(c twait.Counts) bool {
			return c.Total == numPipelines && c.Done == numPipelines
		})

	tgitea.AssertConcurrencyRespected(ctx, t, topts, 1)
	health.Assert(ctx, t)
}

// TestGiteaConcurrencyQueueRebuildSurvivesBadPipelineRun checks that one
// unusable PipelineRun in one namespace does not stop the watcher from
// rebuilding the queues of every other repository.
//
// The bad PipelineRun is written straight into the cluster rather than produced
// through a provider, because there is no reliable way to make a real event
// generate one. Namespaces are listed in name order, so the "aaa" one is
// guaranteed to be reached before the "zzz" one that carries the real test.
func TestGiteaConcurrencyQueueRebuildSurvivesBadPipelineRun(t *testing.T) {
	const numPipelines = 4
	ctx := context.Background()
	runcnx, _, _, err := tgitea.Setup(ctx)
	assert.NilError(t, err)

	poisonNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("aaa-pac-e2e-poison")
	assert.NilError(t, pacrepo.CreateNS(ctx, poisonNS, runcnx))
	defer pacrepo.NSTearDown(ctx, t, runcnx, poisonNS)

	assert.NilError(t, pacrepo.CreateRepo(ctx, poisonNS, runcnx, &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: poisonNS, Namespace: poisonNS},
		Spec: v1alpha1.RepositorySpec{
			URL:              "https://forge.invalid/never/matched",
			ConcurrencyLimit: github.Ptr(1),
		},
	}))

	// Marked as started but with no execution order annotation, which is what
	// the queue rebuild used to choke on. Left pending so it never runs.
	_, err = runcnx.Clients.Tekton.TektonV1().PipelineRuns(poisonNS).Create(ctx, &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "poison-no-execution-order",
			Namespace: poisonNS,
			Labels:    map[string]string{pacapi.State: kubeinteraction.StateStarted},
		},
		Spec: tektonv1.PipelineRunSpec{
			Status: tektonv1.PipelineRunSpecStatusPending,
			PipelineSpec: &tektonv1.PipelineSpec{
				Tasks: []tektonv1.PipelineTask{{
					Name: "noop",
					TaskSpec: &tektonv1.EmbeddedTask{
						TaskSpec: tektonv1.TaskSpec{
							Steps: []tektonv1.Step{{
								Name:   "noop",
								Image:  "registry.access.redhat.com/ubi10/ubi-micro",
								Script: "true",
							}},
						},
					},
				}},
			},
		},
	}, metav1.CreateOptions{})
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("created a pipelinerun with no execution order in %s", poisonNS)

	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun-alt.yaml"
	}
	targetRef := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("zzz-pac-e2e-test")
	topts := &tgitea.TestOpts{
		TargetEvent:      triggertype.PullRequest.String(),
		TargetRefName:    targetRef,
		TargetNS:         targetRef,
		YAMLFiles:        yamlFiles,
		ExtraArgs:        map[string]string{"Command": "sleep 60"},
		ConcurrencyLimit: github.Ptr(1),
		ExpectEvents:     false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
		"1 running and some pending pipelineruns", twait.DefaultTimeout,
		func(c twait.Counts) bool { return c.Running == 1 && c.Pending >= 2 })

	// Forces the queue to be rebuilt from what is already in the cluster. The
	// watcher used to abort here, so simply coming back ready is an assertion.
	tkubestuff.BounceWatcher(ctx, t, topts.ParamsRun)

	health := tkubestuff.SnapshotWatcherHealth(ctx, t, topts.ParamsRun)

	twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
		fmt.Sprintf("all %d pipelineruns finished", numPipelines),
		twait.DefaultTimeout, func(c twait.Counts) bool {
			return c.Total == numPipelines && c.Done == numPipelines
		})

	tgitea.AssertConcurrencyRespected(ctx, t, topts, 1)
	health.Assert(ctx, t)
}

// TestGiteaConcurrencyWatcherSurvivesParallelRepos runs several limited
// repositories at once and deletes some of them while their queues are still
// active, then checks the watcher is still standing.
//
// Deleting a Repository while its runs are being reconciled is what used to
// collide with the queue bookkeeping. The collision is timing dependent, so
// this test makes it likely rather than certain, and asserts on the symptom.
func TestGiteaConcurrencyWatcherSurvivesParallelRepos(t *testing.T) {
	const (
		numRepos     = 6
		numDeleted   = 3
		numPipelines = 3
	)
	ctx := context.Background()
	runcnx, opts, giteacnx, err := tgitea.Setup(ctx)
	assert.NilError(t, err)

	health := tkubestuff.SnapshotWatcherHealth(ctx, t, runcnx)

	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun-alt.yaml"
	}

	all := make([]*tgitea.TestOpts, 0, numRepos)
	for i := 0; i < numRepos; i++ {
		topts := &tgitea.TestOpts{
			ParamsRun:        runcnx,
			GiteaCNX:         giteacnx,
			Opts:             opts,
			TargetEvent:      triggertype.PullRequest.String(),
			YAMLFiles:        yamlFiles,
			ExtraArgs:        map[string]string{"Command": "sleep 20"},
			ConcurrencyLimit: github.Ptr(1),
			ExpectEvents:     false,
		}
		_, f := tgitea.TestPR(t, topts)
		defer f()
		all = append(all, topts)
	}

	// Wait until every repository has work in flight, so the deletions below
	// land while the queues are busy rather than before they fill up.
	for _, topts := range all {
		twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
			"pipelineruns in flight", twait.DefaultTimeout,
			func(c twait.Counts) bool { return c.Running >= 1 })
	}

	for _, topts := range all[:numDeleted] {
		err := runcnx.Clients.PipelineAsCode.PipelinesascodeV1alpha1().
			Repositories(topts.TargetNS).Delete(ctx, topts.TargetNS, metav1.DeleteOptions{})
		assert.NilError(t, err)
		runcnx.Clients.Log.Infof("deleted repository %s while its queue was active", topts.TargetNS)
	}

	for _, topts := range all[numDeleted:] {
		twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
			fmt.Sprintf("all %d pipelineruns finished", numPipelines),
			twait.DefaultTimeout, func(c twait.Counts) bool {
				return c.Total == numPipelines && c.Done == numPipelines
			})
		tgitea.AssertConcurrencyRespected(ctx, t, topts, 1)
	}

	health.Assert(ctx, t)
}

// TestGiteaConcurrencyTransientAPIFailureDoesNotWedgeQueue makes writes to
// PipelineRuns fail for a while, and checks the repository recovers once they
// are allowed again.
//
// A concurrency slot is taken before the run is actually un-pended. If the slot
// is kept when that write fails, the run never starts, so it never completes,
// so the slot is never given back: one bad minute permanently shrinks the
// limit. At a limit of 1 the repository stops running anything at all.
//
// Unlike the other tests here this one covers a regression introduced by the
// fix rather than by the original code, so it is checked against this branch.
func TestGiteaConcurrencyTransientAPIFailureDoesNotWedgeQueue(t *testing.T) {
	const numPipelines = 5
	ctx := context.Background()
	runcnx, opts, giteacnx, err := tgitea.Setup(ctx)
	assert.NilError(t, err)

	// Restarts the watcher, so it has to happen before there is anything
	// queued: enabling it later, after the scenario, would reset the
	// in-memory queue state this test relies on AssertQueueDrained to check.
	tkubestuff.EnsureQueueDebugEnabled(ctx, t, runcnx)

	yamlFiles := map[string]string{}
	for i := 0; i < numPipelines; i++ {
		yamlFiles[fmt.Sprintf(".tekton/pr%d.yaml", i)] = "testdata/pipelinerun-alt.yaml"
	}
	topts := &tgitea.TestOpts{
		ParamsRun:        runcnx,
		GiteaCNX:         giteacnx,
		Opts:             opts,
		TargetEvent:      triggertype.PullRequest.String(),
		YAMLFiles:        yamlFiles,
		ExtraArgs:        map[string]string{"Command": "sleep 20"},
		ConcurrencyLimit: github.Ptr(1),
		ExpectEvents:     false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	// Waiting for one to finish means a slot has just been freed and the next
	// run is about to be started, which is the window we want to break.
	twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
		"the first pipelinerun to finish", twait.DefaultTimeout,
		func(c twait.Counts) bool { return c.Done >= 1 })

	health := tkubestuff.SnapshotWatcherHealth(ctx, t, topts.ParamsRun)

	allow := tkubestuff.DenyPipelineRunWrites(ctx, t, topts.ParamsRun, topts.TargetNS)
	time.Sleep(60 * time.Second)
	allow()

	twait.UntilCounts(ctx, t, topts.ParamsRun.Clients, topts.TargetNS,
		fmt.Sprintf("all %d pipelineruns finished", numPipelines),
		twait.DefaultTimeout, func(c twait.Counts) bool {
			return c.Total == numPipelines && c.Done == numPipelines
		})

	tgitea.AssertConcurrencyRespected(ctx, t, topts, 1)
	tkubestuff.AssertQueueDrained(ctx, t, topts.ParamsRun, topts.TargetNS, topts.TargetNS)
	health.Assert(ctx, t)
}
