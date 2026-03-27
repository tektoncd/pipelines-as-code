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

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/random"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestGithubGHEPullRequestConcurrencyMultiplePR concurrency for the same Repository over multiples PR including a /retest
// and a max-keep-run, may be a bit slow (180s at least) but it's worth it.
func TestGithubGHEPullRequestConcurrencyMultiplePR(t *testing.T) {
	ctx := context.Background()
	label := "Github Multiple PullRequest Concurrency-1 MaxKeepRun-1 Multiple"
	numberOfPullRequest := 3
	numberOfPipelineRuns := 3
	numberOfRetests := 1
	maxNumberOfConcurrentPipelineRuns := 1
	maxKeepRun := 1
	allPipelinesRunsCnt := (numberOfPullRequest * numberOfPipelineRuns) + (numberOfPullRequest * numberOfRetests * numberOfPipelineRuns)
	allPipelinesRunAfterCleanUp := allPipelinesRunsCnt / (maxKeepRun + 1)
	loopMax := 35

	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	_, runcnx, opts, ghcnx, err := tgithub.Setup(ctx, true, false)
	assert.NilError(t, err)

	runcnx.Clients.Log.Infof("Starting %d pipelineruns, (numberOfPullRequest=%d*numberOfPipelineRuns=%d) + (numberOfPullRequest=%d*numberOfRetests=%d*numberOfPipelineRuns=%d) Should end after clean up (maxKeepRun=%d) with %d",
		allPipelinesRunsCnt, numberOfPullRequest, numberOfPipelineRuns, numberOfPullRequest, numberOfRetests, numberOfPipelineRuns, maxKeepRun, allPipelinesRunAfterCleanUp)

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

	// send some retest to spice things up on concurrency and test the maxKeepRun
	for i := 0; i < numberOfRetests; i++ {
		for _, g := range allPullRequests {
			_, _, err := g.Provider.Client().Issues.CreateComment(ctx,
				g.Options.Organization,
				g.Options.Repo, g.PRNumber,
				&github.IssueComment{Body: github.Ptr("/retest")})
			assert.NilError(t, err)
		}
	}

	shas := make([]string, 0, len(allPullRequests))
	for _, pr := range allPullRequests {
		shas = append(shas, pr.SHA)
	}
	labelSelector := fmt.Sprintf("%s in (%s)", keys.SHA, strings.Join(shas, ","))

	finished := false
	for i := 0; i < loopMax; i++ {
		unsuccessful := 0
		prs, err := runcnx.Clients.Tekton.TektonV1().PipelineRuns(targetNS).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		assert.NilError(t, err)
		for _, pr := range prs.Items {
			if pr.Status.GetConditions() == nil {
				unsuccessful++
				continue
			}
			for _, condition := range pr.Status.GetConditions() {
				if condition.Status == "Unknown" || condition.GetReason() == tektonv1.PipelineRunSpecStatusPending {
					unsuccessful++
					continue
				}
			}
		}
		if unsuccessful == 0 {
			finished = true
			break
		}
		runcnx.Clients.Log.Infof("number of unsuccessful PR %d out of %d, waiting 10s more, %d/%d", unsuccessful, allPipelinesRunsCnt, i, loopMax)
		// it's high because it takes time to process on kind
		time.Sleep(10 * time.Second)
	}
	if !finished {
		t.Errorf("we didn't get %d pipelineruns as successful, some of them are still pending or it's abnormally slow to process the Q", allPipelinesRunsCnt)
	}

	maxWaitLoopRun := 10
	success := false
	allPipelineRunsNamesAndStatus := []string{}
	for i := range maxWaitLoopRun {
		prs, err := runcnx.Clients.Tekton.TektonV1().PipelineRuns(targetNS).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		assert.NilError(t, err)
		for _, pr := range prs.Items {
			allPipelineRunsNamesAndStatus = append(allPipelineRunsNamesAndStatus, fmt.Sprintf("%s %s", pr.Name, pr.Status.GetConditions()))
		}
		// Filter out PipelineRuns that don't match our pattern
		matchingPRs := []tektonv1.PipelineRun{}
		for _, pr := range prs.Items {
			if strings.HasPrefix(pr.Name, "prlongrunnning-") {
				matchingPRs = append(matchingPRs, pr)
			}
		}
		if len(matchingPRs) == allPipelinesRunAfterCleanUp {
			runcnx.Clients.Log.Infof("we have the expected number of pipelineruns %d/%d, %d/%d", len(matchingPRs), allPipelinesRunAfterCleanUp, i, maxWaitLoopRun)
			success = true
			break
		}

		runcnx.Clients.Log.Infof("we are still waiting for pipelineruns to be cleaned up, we have %d/%d, sleeping 10s, %d/%d", len(matchingPRs), allPipelinesRunsCnt, i, maxWaitLoopRun)
		time.Sleep(10 * time.Second)
	}
	assert.Assert(t, success, "we didn't get %d pipelineruns as successful, some of them are still pending or it's abnormally slow to process the Q: %s", allPipelinesRunsCnt, allPipelineRunsNamesAndStatus)

	if os.Getenv("TEST_NOCLEANUP") != "true" {
		repository.NSTearDown(ctx, t, runcnx, targetNS)
		return
	}
}
