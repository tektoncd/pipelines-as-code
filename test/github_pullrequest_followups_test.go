//go:build e2e

package test

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/configmap"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	"knative.dev/pkg/apis"
)

// TestGithubGHEPullRequestFollowupsMultipleDependencies covers a follow-up
// waiting on two definitions: it is created only once both succeed, and gets
// the results of both.
func TestGithubGHEPullRequestFollowupsMultipleDependencies(t *testing.T) {
	ctx := context.Background()
	ctx, runcnx, _, _, err := tgithub.Setup(ctx, true, true)
	assert.NilError(t, err)

	// The GHE controller reads its own ConfigMap, the shared watcher the default one.
	for _, name := range []string{tgithub.ControllerConfigMapName(true), info.DefaultPipelinesAscodeConfigmapName} {
		t.Cleanup(configmap.ChangeGlobalConfig(
			ctx, t, runcnx, name,
			map[string]string{"enable-follow-up-runs": "true"},
		))
	}

	g := &tgithub.PRTest{
		Label:         "Github GHE PullRequest follow-up onWebhook",
		YamlFiles:     []string{"testdata/pipelinerun-followups-multi-dep.yaml"},
		GHE:           true,
		NoStatusCheck: true,
	}
	defer g.TearDown(ctx, t)
	g.RunPullRequest(ctx, t)

	prs, err := twait.UntilPipelineRunsFinished(ctx, g.Cnx.Clients, twait.Opts{
		Namespace:       g.TargetNamespace,
		MinNumberStatus: 3,
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{g.SHA},
	})
	assert.NilError(t, err)

	build := definitionRun(prs, "build")
	tests := definitionRun(prs, "tests")
	deploy := definitionRun(prs, "deploy")
	assert.Assert(t, build != nil && tests != nil && deploy != nil, "expected build, tests and deploy among %d runs", len(prs))
	for _, pr := range []string{"build", "tests", "deploy"} {
		run := definitionRun(prs, pr)
		assert.Equal(t, "Succeeded", run.Status.GetCondition(apis.ConditionSucceeded).Reason, pr)
	}

	// deploy waited for the slower dependency
	assert.Assert(t, !deploy.CreationTimestamp.Before(tests.Status.CompletionTime),
		"deploy was created before tests completed")
	assert.Assert(t, !deploy.CreationTimestamp.Before(build.Status.CompletionTime),
		"deploy was created before build completed")

	assert.Equal(t, "registry.example.com/app@sha256:build", paramValue(deploy, "image"))
	assert.Equal(t, "passed", paramValue(deploy, "report"))
}
