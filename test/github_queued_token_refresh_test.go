//go:build e2e

package test

import (
	"context"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

func TestGithubPullRequestRefreshQueuedToken(t *testing.T) {
	ctx, run, opts, provider, err := tgithub.Setup(t.Context(), false, false)
	assert.NilError(t, err)
	repo, _, err := provider.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
	assert.NilError(t, err)
	if !repo.GetPrivate() {
		t.Skip("TEST_GITHUB_REPO_OWNER_GITHUBAPP must point to a private repository for this test")
	}

	g := &tgithub.PRTest{
		Label:         "Renew a queued GitHub App token",
		YamlFiles:     []string{"testdata/pipelinerun_git_clone_private_pending.yaml"},
		NoStatusCheck: true,
		Cnx:           run,
		Options:       opts,
		Provider:      provider,
	}
	defer g.TearDown(ctx, t)
	g.RunPullRequest(ctx, t)

	waitOpts := wait.Opts{
		Namespace:                 g.TargetNamespace,
		TargetSHA:                 []string{g.SHA},
		PollTimeout:               5 * time.Minute,
		FailOnOtherTerminalReason: true,
	}
	pending, err := wait.UntilPipelineRunHasReason(ctx, run.Clients, tektonv1.PipelineRunReasonPending, waitOpts)
	assert.NilError(t, err)
	assert.Equal(t, len(pending), 1)
	pr := pending[0]
	secretName := pr.Annotations[keys.GitAuthSecret]
	assert.Assert(t, secretName != "", "PaC did not record the git-auth Secret name")
	secrets := run.Clients.Kube.CoreV1().Secrets(pr.Namespace)
	var secret *corev1.Secret
	err = kwait.PollUntilContextTimeout(ctx, time.Second, time.Minute, true, func(ctx context.Context) (bool, error) {
		var err error
		secret, err = secrets.Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		return slices.ContainsFunc(secret.OwnerReferences, func(ref metav1.OwnerReference) bool {
			return ref.Kind == "PipelineRun" && ref.UID == pr.UID
		}), nil
	})
	assert.NilError(t, err, "wait for PaC to attach the Secret to the PipelineRun")
	_, err = time.Parse(time.RFC3339, secret.Annotations[keys.GitHubTokenExpiry])
	assert.NilError(t, err, "PaC did not record the GitHub token expiry")

	credential, err := url.Parse(string(secret.Data[".git-credentials"]))
	if err != nil {
		t.Fatal("git-auth Secret has an invalid clone credential URL")
	}
	assert.Assert(t, credential.User != nil, "git-auth Secret has no clone credential")
	const invalidToken = "invalid-e2e-token"
	credential.User = url.UserPassword(credential.User.Username(), invalidToken)
	secret.Data[".git-credentials"] = []byte(credential.String())
	secret.Data["git-provider-token"] = []byte(invalidToken)
	secret.Annotations[keys.GitHubTokenExpiry] = "invalid-expiry"
	secret, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	assert.NilError(t, err)

	release := []byte(`[{"op":"test","path":"/spec/status","value":"PipelineRunPending"},{"op":"remove","path":"/spec/status"}]`)
	runs := run.Clients.Tekton.TektonV1().PipelineRuns(pr.Namespace)
	_, err = runs.Patch(ctx, pr.Name, types.JSONPatchType, release, metav1.PatchOptions{})
	assert.ErrorContains(t, err, "invalid git auth token expiry")
	stillPending, err := runs.Get(ctx, pr.Name, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, stillPending.Spec.Status, tektonv1.PipelineRunSpecStatus(tektonv1.PipelineRunSpecStatusPending))

	secret.Annotations[keys.GitHubTokenExpiry] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	_, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	assert.NilError(t, err)
	_, err = runs.Patch(ctx, pr.Name, types.JSONPatchType, release, metav1.PatchOptions{})
	assert.NilError(t, err, "release the pending PipelineRun through admission")

	refreshed, err := secrets.Get(ctx, secretName, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, refreshed.UID, secret.UID)
	expiry, err := time.Parse(time.RFC3339, refreshed.Annotations[keys.GitHubTokenExpiry])
	assert.NilError(t, err)
	assert.Assert(t, expiry.After(time.Now()), "admission did not save a fresh expiry before release")

	finished, err := wait.UntilPipelineRunHasReason(ctx, run.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err, "the private clone must succeed on its first attempt")
	assert.Equal(t, len(finished), 1)
	assert.Equal(t, finished[0].UID, pr.UID)
}
