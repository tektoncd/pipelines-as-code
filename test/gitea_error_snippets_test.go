//go:build e2e

package test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/configmap"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	pacrepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/secret"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestGiteaErrorSnippet(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun-error-snippet.yaml",
		},
		CheckForStatus: "failure",
		ExpectEvents:   false,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	topts.Regexp = regexp.MustCompile(`Hey man i just wanna to say i am not such a failure, i am useful in my failure`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)
}

func TestGiteaErrorSnippetCustomLines(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun-error-snippet.yaml",
		},
		CheckForStatus:   "failure",
		ExpectEvents:     false,
		SkipEventsCheck:  true,
		StatusOnlyLatest: true,
	}
	topts.TargetRefName = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
	topts.TargetNS = topts.TargetRefName
	topts.ParamsRun, topts.Opts, topts.GiteaCNX, _ = tgitea.Setup(ctx)
	assert.NilError(t, topts.ParamsRun.Clients.NewClients(ctx, &topts.ParamsRun.Info))
	ctx, err := cctx.GetControllerCtxInfo(ctx, topts.ParamsRun)
	assert.NilError(t, err)
	assert.NilError(t, pacrepo.CreateNS(ctx, topts.TargetNS, topts.ParamsRun))
	cfgMapData := map[string]string{
		"error-log-snippet-number-of-lines": "5",
	}
	defer configmap.ChangeGlobalConfig(ctx, t, topts.ParamsRun, "pipelines-as-code", cfgMapData)()

	_, f := tgitea.TestPR(t, topts)
	defer f()

	topts.Regexp = regexp.MustCompile(`Hey man i just wanna to say i am not such a failure, i am useful in my failure`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	comments, _, err := topts.GiteaCNX.Client().ListRepoIssueComments(topts.PullRequest.Base.Repository.Owner.UserName, topts.PullRequest.Base.Repository.Name, forgejo.ListIssueCommentOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(comments) > 0)
	lastComment := comments[len(comments)-1]
	body := lastComment.Body

	// Keep only the content from `<h4>Failure snippet:</h4>` onwards, if present; otherwise, we cannot perform a comparison due to the random e2e test name.
	const marker = "<h4>Failure snippet:</h4>"
	if idx := strings.Index(body, marker); idx != -1 {
		body = body[idx:]
	}
	golden.Assert(t, body, strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"))
}

func TestGiteaErrorSnippetWithSecret(t *testing.T) {
	var err error
	ctx := context.Background()
	topts := &tgitea.TestOpts{
		TargetRefName: names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test"),
	}
	topts.TargetNS = topts.TargetRefName
	topts.ParamsRun, topts.Opts, topts.GiteaCNX, err = tgitea.Setup(ctx)
	assert.NilError(t, err, fmt.Errorf("cannot do gitea setup: %w", err))
	ctx, err = cctx.GetControllerCtxInfo(ctx, topts.ParamsRun)
	assert.NilError(t, err)
	assert.NilError(t, pacrepo.CreateNS(ctx, topts.TargetNS, topts.ParamsRun))
	assert.NilError(t, secret.Create(ctx, topts.ParamsRun, map[string]string{"secret": "SHHHHHHH"}, topts.TargetNS, "pac-secret"))
	topts.TargetEvent = triggertype.PullRequest.String()
	topts.YAMLFiles = map[string]string{
		".tekton/pr.yaml": "testdata/pipelinerun-error-snippet-with-secret.yaml",
	}
	topts.CheckForStatus = "failure"
	_, f := tgitea.TestPR(t, topts)
	defer f()

	topts.Regexp = regexp.MustCompile(`I WANT TO SAY \*\*\*\*\* OUT LOUD BUT NOBODY UNDERSTAND ME`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)
}
