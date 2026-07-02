//go:build e2e

package test

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	pacrepo "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/secret"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestGiteaCustomParamsInCELExpression tests that custom parameters from Repository CR
// can be used directly in CEL expressions for matching PipelineRuns. This test verifies:
// - Custom params work in CEL expressions (enable_ci and environment)
// - Only PipelineRuns with matching CEL expressions are triggered
// - PipelineRuns with non-matching custom params are skipped (enable_ci == "false").
// - Custom params are available for template expansion in the PipelineRun.
func TestGiteaCustomParamsInCELExpression(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      successRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml":      "testdata/pipelinerun-cel-custom-params-pullrequest.yaml",
			".tekton/nomatch.yaml": "testdata/pipelinerun-cel-custom-params-nomatch.yaml",
		},
		CheckForStatus: "success",
		ExpectEvents:   false,
		RepoCRParams: &[]v1alpha1.Params{
			{
				Name:  "enable_ci",
				Value: "true",
			},
			{
				Name:  "environment",
				Value: "staging",
			},
		},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	// List PipelineRuns to verify only one was created
	// The pr.yaml should match (event == "pull_request" && enable_ci == "true" && environment != "")
	// The nomatch.yaml should NOT match (enable_ci == "false" && environment == "production")
	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(
		context.Background(), metav1.ListOptions{},
	)
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 1, "Expected exactly 1 PipelineRun to match (nomatch.yaml should be skipped)")

	// Verify the output contains the custom params expanded via template
	output := `Custom params: enable_ci=true environment=staging`
	err = twait.RegexpMatchingInPodLog(context.Background(), topts.ParamsRun, topts.TargetNS,
		fmt.Sprintf("tekton.dev/pipelineRun=%s,tekton.dev/pipelineTask=custom-params-cel-test", prs.Items[0].Name),
		"step-test-custom-params-cel", *regexp.MustCompile(output), "", 2, nil)
	assert.NilError(t, err)
}

// TestGiteaCustomParamsFromSecretsInCEL tests that custom parameters from secrets
// in Repository CR can be used directly in CEL expressions. This verifies:
// - Secret values are resolved and available as CEL variables
// - CEL expressions can check for non-empty secret values
// - Secret values are properly expanded in templates
// - Security: Secret values should not be leaked in controller logs.
func TestGiteaCustomParamsFromSecretsInCEL(t *testing.T) {
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

	assert.NilError(t, secret.Create(ctx, topts.ParamsRun,
		map[string]string{
			"api_key":      "super-secret-api-key-12345",
			"deploy_token": "deploy-token-xyz-789",
		},
		topts.TargetNS, "cel-secret-params"))

	topts.TargetEvent = triggertype.PullRequest.String()
	topts.YAMLFiles = map[string]string{
		".tekton/pr.yaml": "testdata/pipelinerun-cel-secret-params.yaml",
	}
	topts.CheckForStatus = "success"
	topts.StatusOnlyLatest = true
	topts.ExpectEvents = false

	topts.RepoCRParams = &[]v1alpha1.Params{
		{
			Name: "api_key",
			SecretRef: &v1alpha1.Secret{
				Name: "cel-secret-params",
				Key:  "api_key",
			},
		},
		{
			Name: "deploy_token",
			SecretRef: &v1alpha1.Secret{
				Name: "cel-secret-params",
				Key:  "deploy_token",
			},
		},
	}

	_, f := tgitea.TestPR(t, topts)
	defer f()

	// Verify the output contains the secret params expanded via template
	output := `Secret params verified: api_key=super-secret-api-key-12345 deploy_token=deploy-token-xyz-789`
	err = twait.RegexpMatchingInPodLog(context.Background(), topts.ParamsRun, topts.TargetNS,
		"tekton.dev/pipelineTask=cel-secret-params-test",
		"step-test-cel-secret-params", *regexp.MustCompile(output), "", 2, nil)
	assert.NilError(t, err)

	// Verify secrets are not leaked in controller logs
	maxLines := int64(1000)
	secretLeakRegex := regexp.MustCompile(`super-secret-api-key-12345|deploy-token-xyz-789`)
	err = twait.RegexpMatchingInControllerLog(ctx, topts.ParamsRun, *secretLeakRegex, 2, "controller", &maxLines, nil)
	if err == nil {
		t.Fatal("Secret values were found in controller logs - this is a security issue!")
	}
}

// TestGiteaFilteredParamsUndefinedInCEL tests that when custom params have filters
// that don't match the current event type, they become undefined and cause CEL
// evaluation errors (not silent failures). This ensures users get clear feedback
// when their CEL expression references params that aren't available.
func TestGiteaFilteredParamsUndefinedInCEL(t *testing.T) {
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

	// Configure param with a filter that only matches push events
	// When we trigger a pull_request event, this param won't be available
	topts.RepoCRParams = &[]v1alpha1.Params{
		{
			Name:   "docker_registry",
			Value:  "registry.staging.example.com",
			Filter: "pac.event_type == \"push\"",
		},
	}

	topts.TargetEvent = triggertype.PullRequest.String()
	topts.YAMLFiles = map[string]string{
		".tekton/pr.yaml": "testdata/pipelinerun-cel-filtered-param-undefined.yaml",
	}
	// We expect this to fail because the CEL expression uses docker_registry
	// which isn't defined for pull_request events (filter requires push)
	topts.ExpectEvents = true
	topts.CheckForStatus = ""
	topts.CheckForNumberStatus = 0

	_, f := tgitea.TestPR(t, topts)
	defer f()

	// Verify a CEL evaluation error reported on the PR
	topts.Regexp = regexp.MustCompile(`(?i)(undeclared reference|undefined|no such attribute|docker_registry|CEL)`)
	tgitea.WaitForPullRequestCommentMatch(t, topts)

	// Verify no PipelineRuns were created (because CEL evaluation failed)
	prs, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(
		context.Background(), metav1.ListOptions{},
	)
	assert.NilError(t, err)
	assert.Equal(t, len(prs.Items), 0, "Expected no PipelineRuns since CEL expression has undefined variable")
}

// TestGiteaOnCommentParamsReResolved tests that when a PipelineRun has both
// on-comment and on-cel-expression annotations, the custom params are re-resolved
// after the event type changes to "on-comment". This ensures that params with
// filters based on pac.event_type == "on-comment" are available in the CEL expression.
func TestGiteaOnCommentParamsReResolved(t *testing.T) {
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

	topts.RepoCRParams = &[]v1alpha1.Params{
		{
			Name:   "trigger_type",
			Value:  "comment",
			Filter: "pac.event_type == \"on-comment\"",
		},
	}

	topts.TargetEvent = triggertype.PullRequest.String()
	topts.YAMLFiles = map[string]string{
		".tekton/on-comment.yaml": "testdata/pipelinerun-cel-on-comment-param.yaml",
	}
	topts.ExpectEvents = false
	topts.CheckForStatus = ""
	topts.CheckForNumberStatus = 0
	topts.SkipEventsCheck = true

	_, f := tgitea.TestPR(t, topts)
	defer f()

	tgitea.PostCommentOnPullRequest(t, topts, "/test-comment")

	// Wait for status on gitea (indicates PipelineRun completed)
	topts.CheckForStatus = "success"
	tgitea.WaitForStatus(t, topts, "heads/"+topts.TargetRefName, "", true)

	// Verify the output contains the trigger_type param (which was only available after re-resolving)
	output := `Trigger type verified: trigger_type=comment`
	err = twait.RegexpMatchingInPodLog(context.Background(), topts.ParamsRun, topts.TargetNS,
		"tekton.dev/pipelineTask=on-comment-cel-test",
		"step-test-on-comment-cel", *regexp.MustCompile(output), "", 2, nil)
	assert.NilError(t, err)
}
