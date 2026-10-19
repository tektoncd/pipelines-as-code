//go:build e2e

package test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v91/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/random"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

// TestGithubGHEPullRequestConcurrencyMultiplePR runs PipelineRuns from several
// PRs through a Repository with a concurrency limit of one, then a /test on
// each PR. It checks that every original and rerun PipelineRun succeeds and
// that max-keep-runs prunes each pipeline down to its newest run. It does not
// assert the concurrency limit itself.
func TestGithubGHEPullRequestConcurrencyMultiplePR(t *testing.T) {
	ctx := context.Background()
	label := "Github Multiple PullRequest Concurrency-1 MaxKeepRun-1"
	numberOfPullRequest := 2
	numberOfPipelineRuns := 3
	numberOfReruns := 1
	maxNumberOfConcurrentPipelineRuns := 1
	maxKeepRun := 1
	allPipelinesRunsCnt := (numberOfPullRequest * numberOfPipelineRuns) + (numberOfPullRequest * numberOfReruns * numberOfPipelineRuns)
	allPipelinesRunAfterCleanUp := allPipelinesRunsCnt / (maxKeepRun + 1)
	numberOfOriginalRuns := numberOfPullRequest * numberOfPipelineRuns

	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	_, runcnx, opts, ghcnx, err := tgithub.Setup(ctx, true, false)
	assert.NilError(t, err)

	runcnx.Clients.Log.Infof("Starting %d pipelineruns, (numberOfPullRequest=%d*numberOfPipelineRuns=%d) + (numberOfPullRequest=%d*numberOfReruns=%d*numberOfPipelineRuns=%d) Should end after clean up (maxKeepRun=%d) with %d",
		allPipelinesRunsCnt, numberOfPullRequest, numberOfPipelineRuns, numberOfPullRequest, numberOfReruns, numberOfPipelineRuns, maxKeepRun, allPipelinesRunAfterCleanUp)

	repoinfo, resp, err := ghcnx.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Errorf("Repository %s not found in %s", opts.Organization, opts.Repo)
	}
	// set concurrency
	opts.Concurrency = maxNumberOfConcurrentPipelineRuns
	err = tgithub.CreateCRD(ctx, t, repoinfo, runcnx, opts, ghcnx, targetNS)
	assert.NilError(t, err)

	allPullRequests := []tgithub.PRTest{}
	for prc := 0; prc < numberOfPullRequest; prc++ {
		branchName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("branch")
		logmsg := fmt.Sprintf("Testing %s with Github APPS integration branch %s namespace %s", label, branchName, targetNS)
		yamlFiles := map[string]string{}
		randomAlphaString := strings.ToLower(random.AlphaString(4))
		for i := 1; i <= numberOfPipelineRuns; i++ {
			yamlFiles[fmt.Sprintf(".tekton/prlongrunnning-%s-%d.yaml", randomAlphaString, i)] = "testdata/pipelinerun_long_running_maxkeep_run.yaml"
		}

		entries, err := payload.GetEntries(yamlFiles, targetNS, options.MainBranch, triggertype.PullRequest.String(), map[string]string{
			"MaxKeepRun": fmt.Sprint(maxKeepRun),
		})
		assert.NilError(t, err)

		targetRefName := fmt.Sprintf("refs/heads/%s",
			names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test"))

		sha, vref, err := tgithub.PushFilesToRef(ctx, ghcnx.Client(), logmsg, repoinfo.GetDefaultBranch(), targetRefName,
			opts.Organization, opts.Repo, entries)
		assert.NilError(t, err)
		runcnx.Clients.Log.Infof("Commit %s has been created and pushed to %s", sha, vref.GetURL())

		prNumber, err := tgithub.PRCreate(ctx, runcnx, ghcnx, opts.Organization, opts.Repo, targetRefName, repoinfo.GetDefaultBranch(), logmsg)
		assert.NilError(t, err)

		g := tgithub.PRTest{
			Cnx:           runcnx,
			Options:       opts,
			Provider:      ghcnx,
			TargetRefName: targetRefName,
			PRNumber:      prNumber,
			SHA:           sha,
			Logger:        runcnx.Clients.Log,
		}
		defer g.TearDown(ctx, t)
		allPullRequests = append(allPullRequests, g)
	}

	shas := make([]string, 0, len(allPullRequests))
	for _, pr := range allPullRequests {
		shas = append(shas, pr.SHA)
	}
	labelSelector := fmt.Sprintf("%s in (%s)", keys.SHA, strings.Join(shas, ","))

	// Finish originals first so cleanup always retains the reruns
	originals, err := twait.UntilPipelineRunsFinished(ctx, runcnx.Clients, twait.Opts{
		Namespace:       targetNS,
		TargetSHA:       shas,
		MinNumberStatus: numberOfOriginalRuns,
		PollTimeout:     twait.DefaultTimeout,
	})
	assert.NilError(t, err)
	assert.Equal(t, len(originals), numberOfOriginalRuns, "unexpected original pipelineruns: %s", describeMultiPRRuns(originals))
	expectedPairs := map[string]bool{}
	runsPerSHA := map[string]int{}
	for _, pr := range originals {
		assert.Assert(t, strings.HasPrefix(pr.GetName(), pipelineRunFileNamePrefix), "unexpected pipelinerun %s", pr.GetName())
		assert.Equal(t, pr.GetAnnotations()[keys.EventType], triggertype.PullRequest.String(), "pipelinerun %s", pr.GetName())
		assert.Assert(t, pr.Status.GetCondition(apis.ConditionSucceeded).IsTrue(), "original pipelinerun did not succeed: %s", describeMultiPRRuns(originals))
		expectedPairs[multiPRPair(pr)] = true
		runsPerSHA[pr.GetLabels()[keys.SHA]]++
	}
	assert.Equal(t, len(expectedPairs), numberOfOriginalRuns, "duplicate original pipelineruns: %s", describeMultiPRRuns(originals))
	for _, sha := range shas {
		assert.Equal(t, runsPerSHA[sha], numberOfPipelineRuns, "unexpected number of original pipelineruns for %s", sha)
	}
	runcnx.Clients.Log.Infof("all %d original pipelineruns succeeded, sending /test", numberOfOriginalRuns)

	for i := 0; i < numberOfReruns; i++ {
		for _, g := range allPullRequests {
			_, _, err := g.Provider.Client().Issues.CreateComment(ctx,
				g.Options.Organization,
				g.Options.Repo, g.PRNumber,
				github.IssueCommentRequest{Body: "/test"})
			assert.NilError(t, err)
		}
	}

	rerunCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	var current []tektonv1.PipelineRun
	for {
		prs, err := runcnx.Clients.Tekton.TektonV1().PipelineRuns(targetNS).List(rerunCtx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			t.Fatalf("listing pipelineruns while waiting for %d reruns to be kept after cleanup: %v, last seen: %s",
				allPipelinesRunAfterCleanUp, err, describeMultiPRRuns(current))
		}
		current = current[:0]
		for _, pr := range prs.Items {
			if strings.HasPrefix(pr.GetName(), pipelineRunFileNamePrefix) {
				current = append(current, pr)
			}
		}
		done, err := multiPRRerunsKept(current, expectedPairs)
		if err != nil {
			t.Fatalf("%v: %s", err, describeMultiPRRuns(current))
		}
		if done {
			runcnx.Clients.Log.Infof("all %d rerun pipelineruns succeeded and the originals were cleaned up", allPipelinesRunAfterCleanUp)
			break
		}
		runcnx.Clients.Log.Infof("waiting for %d rerun pipelineruns to succeed and the originals to be cleaned up: %s",
			allPipelinesRunAfterCleanUp, describeMultiPRRuns(current))
		select {
		case <-rerunCtx.Done():
			t.Fatalf("timed out waiting for %d rerun pipelineruns to be kept after cleanup, last seen: %s",
				allPipelinesRunAfterCleanUp, describeMultiPRRuns(current))
		case <-time.After(5 * time.Second):
		}
	}

	if os.Getenv("TEST_NOCLEANUP") != "true" {
		repository.NSTearDown(ctx, t, runcnx, targetNS)
		return
	}
}

func multiPRPair(pr tektonv1.PipelineRun) string {
	return pr.GetLabels()[keys.SHA] + "/" + pr.GetLabels()[keys.OriginalPRName]
}

func multiPRFinished(pr tektonv1.PipelineRun) bool {
	cond := pr.Status.GetCondition(apis.ConditionSucceeded)
	state := pr.GetAnnotations()[keys.State]
	return cond != nil && cond.Status != corev1.ConditionUnknown &&
		(state == kubeinteraction.StateCompleted || state == kubeinteraction.StateFailed)
}

func multiPRRerunsKept(prs []tektonv1.PipelineRun, expectedPairs map[string]bool) (bool, error) {
	kept := map[string]bool{}
	for _, pr := range prs {
		if pr.GetAnnotations()[keys.EventType] != opscomments.TestAllCommentEventType.String() || !multiPRFinished(pr) {
			continue
		}
		if !pr.Status.GetCondition(apis.ConditionSucceeded).IsTrue() {
			return false, fmt.Errorf("rerun pipelinerun %s did not succeed", pr.GetName())
		}
		if !expectedPairs[multiPRPair(pr)] {
			return false, fmt.Errorf("rerun pipelinerun %s does not match any original pipelinerun", pr.GetName())
		}
		kept[multiPRPair(pr)] = true
	}
	return len(prs) == len(expectedPairs) && len(kept) == len(expectedPairs), nil
}

func TestConcurrencyMultiPRRerunsKept(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		status    corev1.ConditionStatus
		done      bool
		err       string
	}{
		{
			name:      "successful test command rerun",
			eventType: opscomments.TestAllCommentEventType.String(),
			status:    corev1.ConditionTrue,
			done:      true,
		},
		{
			name:      "successful original is not a rerun",
			eventType: triggertype.PullRequest.String(),
			status:    corev1.ConditionTrue,
		},
		{
			name:      "retest event does not satisfy test command wait",
			eventType: opscomments.RetestAllCommentEventType.String(),
			status:    corev1.ConditionTrue,
		},
		{
			name:      "unfinished test command rerun",
			eventType: opscomments.TestAllCommentEventType.String(),
			status:    corev1.ConditionUnknown,
		},
		{
			name:      "failed test command rerun",
			eventType: opscomments.TestAllCommentEventType.String(),
			status:    corev1.ConditionFalse,
			err:       "rerun pipelinerun rerun did not succeed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "rerun",
					Labels:      map[string]string{keys.SHA: "sha", keys.OriginalPRName: "pipeline"},
					Annotations: map[string]string{keys.EventType: tt.eventType, keys.State: kubeinteraction.StateCompleted},
				},
			}
			pr.Status.SetConditions([]apis.Condition{{Type: apis.ConditionSucceeded, Status: tt.status}})
			done, err := multiPRRerunsKept([]tektonv1.PipelineRun{pr}, map[string]bool{"sha/pipeline": true})
			if tt.err != "" {
				assert.ErrorContains(t, err, tt.err)
			} else {
				assert.NilError(t, err)
			}
			assert.Equal(t, done, tt.done)
		})
	}
}

func describeMultiPRRuns(prs []tektonv1.PipelineRun) string {
	out := make([]string, 0, len(prs))
	for _, pr := range prs {
		sha := pr.GetLabels()[keys.SHA]
		if len(sha) > 7 {
			sha = sha[:7]
		}
		status, reason := "", ""
		if cond := pr.Status.GetCondition(apis.ConditionSucceeded); cond != nil {
			status, reason = string(cond.Status), cond.Reason
		}
		out = append(out, fmt.Sprintf("%s(sha=%s original=%s event=%s state=%s status=%s reason=%s)",
			pr.GetName(), sha, pr.GetLabels()[keys.OriginalPRName], pr.GetAnnotations()[keys.EventType],
			pr.GetAnnotations()[keys.State], status, reason))
	}
	return fmt.Sprintf("%d pipelinerun(s) [%s]", len(prs), strings.Join(out, ", "))
}
