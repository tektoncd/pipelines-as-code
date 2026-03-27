//go:build e2e

package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
)

func TestGithubGHEMaxKeepRuns(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github MaxKeepRun config",
		YamlFiles: []string{"testdata/pipelinerun-max-keep-run-1.yaml"},
		GHE:       true,
	}
	g.RunPullRequest(ctx, t)
	defer g.TearDown(ctx, t)

	g.Cnx.Clients.Log.Infof("Creating /test in PullRequest to create a second run")
	_, _, err := g.Provider.Client().Issues.CreateComment(ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
		&github.IssueComment{Body: github.Ptr("/test")})
	assert.NilError(t, err)

	g.Cnx.Clients.Log.Infof("Wait for the second repository update to be updated")
	waitOpts := twait.Opts{
		RepoName:        g.TargetNamespace,
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 2,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       g.SHA,
	}
	_, err = twait.UntilRepositoryUpdated(ctx, g.Cnx.Clients, waitOpts)
	assert.NilError(t, err)

	count := 0
	for {
		prs, err := g.Cnx.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, g.SHA),
		})
		if err == nil && len(prs.Items) == 1 {
			if prs.Items[0].GetStatusCondition().GetCondition(apis.ConditionSucceeded).GetReason() == "Running" {
				t.Logf("skipping %s since currently running", prs.Items[0].GetName())
				continue
			}
			// making sure secret is not deleted for existing pipelinerun
			if secretName, ok := prs.Items[0].GetAnnotations()[keys.GitAuthSecret]; ok {
				sData, err := g.Cnx.Clients.Kube.CoreV1().Secrets(g.TargetNamespace).Get(ctx, secretName, metav1.GetOptions{})
				assert.NilError(t, err, "Secret should not have been deleted while running pipelinerun")
				assert.Assert(t, sData.Name != "")
			}
			break
		}
		time.Sleep(10 * time.Second)
		if count > 10 {
			t.Fatalf("PipelineRun cleanups has not been done, we found %d in %s", len(prs.Items), g.TargetNamespace)
		}
		count++
	}
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -run ^TestGithubMaxKeepRuns$"
// End:
