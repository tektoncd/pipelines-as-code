//go:build e2e

package test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	tkubestuff "github.com/openshift-pipelines/pipelines-as-code/test/pkg/kubestuff"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	trepository "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const pipelineRunFileNamePrefix = "prlongrunnning-"

func TestGithubGHEPullRequestConcurrency1by1(t *testing.T) {
	ctx := context.Background()
	label := "Github PullRequest Concurrent, sequentially one by one"
	numberOfPipelineRuns := 5
	maxNumberOfConcurrentPipelineRuns := 1
	checkOrdering := true
	yamlFiles := map[string]string{}
	g := setupGithubConcurrency(ctx, t, maxNumberOfConcurrentPipelineRuns, numberOfPipelineRuns, label, yamlFiles)
	defer g.TearDown(ctx, t)
	testGithubConcurrency(ctx, t, g, numberOfPipelineRuns, checkOrdering)
}

// TestGithubGHEPullRequestConcurrencyRestartedWhenWatcherIsUp tests that
// when the watcher is down and a PR is created, the PipelineRuns are kept in
// Pending state. When the watcher is up again, the PipelineRuns are restarted.
func TestGithubGHEPullRequestConcurrencyRestartedWhenWatcherIsUp(t *testing.T) {
	ctx := context.Background()
	label := "Github PullRequest Concurrent, sequentially one by one"
	numberOfPipelineRuns := 2
	maxNumberOfConcurrentPipelineRuns := 1
	checkOrdering := true
	yamlFiles := map[string]string{}
	ctx, runCnxS, _, _, err := tgithub.Setup(ctx, true, false)
	assert.NilError(t, err)

	tkubestuff.ScaleDeployment(ctx, t, runCnxS, 0, "pipelines-as-code-watcher", "pipelines-as-code")
	time.Sleep(2 * time.Second)
	defer tkubestuff.ScaleDeployment(ctx, t, runCnxS, 1, "pipelines-as-code-watcher", "pipelines-as-code")
	g := setupGithubConcurrency(ctx, t, maxNumberOfConcurrentPipelineRuns, numberOfPipelineRuns, label, yamlFiles)
	defer g.TearDown(ctx, t)

	maxLoop := 30
	allPipelineRunsStarted := true
	for i := 0; i < maxLoop; i++ {
		prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
		})
		assert.NilError(t, err)

		assert.Assert(t, len(prs.Items) <= numberOfPipelineRuns, "Too many PipelineRuns have been created, expected: %d, got: %d", numberOfPipelineRuns, len(prs.Items))
		if len(prs.Items) != numberOfPipelineRuns {
			time.Sleep(2 * time.Second)
			g.Cnx.Clients.Log.Infof("Waiting for %d PipelineRuns to be created", numberOfPipelineRuns)
			allPipelineRunsStarted = false
			continue
		}
		allPipelineRunsStarted = true
		for _, pr := range prs.Items {
			for _, condition := range pr.Status.GetConditions() {
				assert.Assert(t, condition.GetReason() == tektonv1.PipelineRunSpecStatusPending, "PipelineRun %s is not in pending state", pr.GetName())
			}
		}
		break
	}
	assert.Assert(t, allPipelineRunsStarted, "Not all PipelineRuns have been created, expected: ", numberOfPipelineRuns)

	g.Cnx.Clients.Log.Info("All PipelineRuns are Pending")
	tkubestuff.ScaleDeployment(ctx, t, runCnxS, 1, "pipelines-as-code-watcher", "pipelines-as-code")
	testGithubConcurrency(ctx, t, g, numberOfPipelineRuns, checkOrdering)
}

func TestGithubGHEPullRequestConcurrency3by3(t *testing.T) {
	ctx := context.Background()
	label := "Github PullRequest Concurrent three at time"
	numberOfPipelineRuns := 10
	maxNumberOfConcurrentPipelineRuns := 3
	checkOrdering := false
	yamlFiles := map[string]string{}

	g := setupGithubConcurrency(ctx, t, maxNumberOfConcurrentPipelineRuns, numberOfPipelineRuns, label, yamlFiles)
	defer g.TearDown(ctx, t)
	testGithubConcurrency(ctx, t, g, numberOfPipelineRuns, checkOrdering)
}

func TestGithubGHEPullRequestConcurrency1by1WithError(t *testing.T) {
	ctx := context.Background()
	label := "Github PullRequest Concurrent, sequentially one by one with one bad apple"
	numberOfPipelineRuns := 1
	maxNumberOfConcurrentPipelineRuns := 1
	checkOrdering := true
	yamlFiles := map[string]string{
		".tekton/00-bad-apple.yaml": "testdata/failures/bad-runafter-task.yaml",
	}

	g := setupGithubConcurrency(ctx, t, maxNumberOfConcurrentPipelineRuns, numberOfPipelineRuns, label, yamlFiles)
	defer g.TearDown(ctx, t)
	testGithubConcurrency(ctx, t, g, numberOfPipelineRuns, checkOrdering)
}

func TestGithubGlobalRepoConcurrencyLimit(t *testing.T) {
	label := "Github PullRequest Concurrent Two at a Time Set by Global Repo"
	// in this test we don't set `concurrency_limit` in local repo as our goal
	// here is to verify `concurrency_limit` for global repository.
	localRepoMaxConcurrentRuns := -1
	testGlobalRepoConcurrency(t, label, localRepoMaxConcurrentRuns)
}

func TestGithubGlobalAndLocalRepoConcurrencyLimit(t *testing.T) {
	label := "Github PullRequest Concurrent Three at a Time Set by Local Repo"
	testGlobalRepoConcurrency(t, label, 3)
}

func testGlobalRepoConcurrency(t *testing.T, label string, localRepoMaxConcurrentRuns int) {
	ctx := context.Background()
	// create global repo
	ctx, globalNS, runcnx, err := trepository.CreateGlobalRepo(ctx)
	assert.NilError(t, err)
	defer (func() {
		err = trepository.CleanUpGlobalRepo(runcnx, globalNS)
		assert.NilError(t, err)
	})()

	numberOfPipelineRuns := 10
	checkOrdering := false
	yamlFiles := map[string]string{}

	g := setupGithubConcurrency(ctx, t, localRepoMaxConcurrentRuns, numberOfPipelineRuns, label, yamlFiles)
	defer g.TearDown(ctx, t)
	testGithubConcurrency(ctx, t, g, numberOfPipelineRuns, checkOrdering)
}

func setupGithubConcurrency(ctx context.Context, t *testing.T, maxNumberOfConcurrentPipelineRuns, numberOfPipelineRuns int, label string, yamlFiles map[string]string) tgithub.PRTest {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	_, runcnx, opts, ghcnx, err := tgithub.Setup(ctx, true, false)
	assert.NilError(t, err)

	logmsg := fmt.Sprintf("Testing %s with Github APPS integration on %s", label, targetNS)
	runcnx.Clients.Log.Info(logmsg)

	repoinfo, resp, err := ghcnx.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Errorf("Repository %s not found in %s", opts.Organization, opts.Repo)
	}

	// set concurrency
	if maxNumberOfConcurrentPipelineRuns >= 0 {
		opts.Concurrency = maxNumberOfConcurrentPipelineRuns
	}

	err = tgithub.CreateCRD(ctx, t, repoinfo, runcnx, opts, ghcnx, targetNS)
	assert.NilError(t, err)

	for i := 1; i <= numberOfPipelineRuns; i++ {
		yamlFiles[fmt.Sprintf(".tekton/%s%d.yaml", pipelineRunFileNamePrefix, i)] = "testdata/pipelinerun_long_running.yaml"
	}

	entries, err := payload.GetEntries(yamlFiles, targetNS, options.MainBranch, triggertype.PullRequest.String(), map[string]string{})
	assert.NilError(t, err)

	targetRefName := fmt.Sprintf("refs/heads/%s",
		names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test"))

	sha, vref, err := tgithub.PushFilesToRef(ctx, ghcnx.Client(), logmsg, repoinfo.GetDefaultBranch(), targetRefName,
		opts.Organization, opts.Repo, entries)
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Commit %s has been created and pushed to %s", sha, vref.GetURL())

	prNumber, err := tgithub.PRCreate(ctx, runcnx, ghcnx, opts.Organization,
		opts.Repo, targetRefName, repoinfo.GetDefaultBranch(), logmsg)
	assert.NilError(t, err)

	return tgithub.PRTest{
		Cnx:             runcnx,
		Options:         opts,
		Provider:        ghcnx,
		TargetNamespace: targetNS,
		TargetRefName:   targetRefName,
		PRNumber:        prNumber,
		SHA:             sha,
		Logger:          runcnx.Clients.Log,
	}
}

func testGithubConcurrency(ctx context.Context, t *testing.T, g tgithub.PRTest, numberOfPipelineRuns int, checkOrdering bool) {
	g.Cnx.Clients.Log.Info("waiting to let controller process the event")
	time.Sleep(1 * time.Second)

	waitOpts := wait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 1,
		PollTimeout:     wait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	assert.NilError(t, wait.UntilMinPRAppeared(ctx, g.Cnx.Clients, waitOpts, numberOfPipelineRuns))

	waitForPipelineRunsHasStarted(ctx, t, g, numberOfPipelineRuns)

	// sort all the PR by when they have started
	if checkOrdering {
		prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
		})
		assert.NilError(t, err)

		// Verify we have the expected number of PipelineRuns
		assert.Assert(t, len(prs.Items) == numberOfPipelineRuns,
			"Expected %d PipelineRuns with SHA %s, but found %d",
			numberOfPipelineRuns, g.SHA, len(prs.Items))

		sort.PipelineRunSortByStartTime(prs.Items)
		for i := range make([]int, numberOfPipelineRuns) {
			prExpectedName := fmt.Sprintf("%s%d", pipelineRunFileNamePrefix, len(prs.Items)-i)
			prActualName := prs.Items[i].GetName()
			assert.Assert(t, strings.HasPrefix(prActualName, prExpectedName),
				"prActualName: %s does not start with expected prefix %s, ordering check failed",
				prActualName, prExpectedName)
		}
	}
}

func waitForPipelineRunsHasStarted(ctx context.Context, t *testing.T, g tgithub.PRTest, numberOfPipelineRuns int) {
	finished := false
	maxLoop := 30
	for i := range make([]int, maxLoop) {
		unsuccessful := 0
		prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
		})
		assert.NilError(t, err)

		for _, pr := range prs.Items {
			if pr.Status.GetConditions() == nil {
				unsuccessful++
				continue
			}
			for _, condition := range pr.Status.GetConditions() {
				if condition.IsUnknown() || condition.IsFalse() || condition.GetReason() == tektonv1.PipelineRunSpecStatusPending {
					unsuccessful++
					continue
				}
			}
		}
		if unsuccessful == 0 {
			g.Cnx.Clients.Log.Infof("the %d pipelineruns has successfully finished", numberOfPipelineRuns)
			finished = true
			break
		}
		g.Cnx.Clients.Log.Infof("number of unsuccessful PR %d out of %d, waiting 10s more in the waiting loop: %d/%d", unsuccessful, numberOfPipelineRuns, i, maxLoop)
		// it's high because it takes time to process on kind
		time.Sleep(2 * time.Second)
	}
	if !finished {
		prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
		})
		assert.NilError(t, err)

		for _, pr := range prs.Items {
			t.Logf("PipelineRun %s has conditions: %v", pr.GetName(), pr.Status.Conditions)
		}
		t.Errorf("the %d pipelineruns has not successfully finished, some of them are still pending or it's abnormally slow to process the Q", numberOfPipelineRuns)
	}
}
