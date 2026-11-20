//go:build e2e

package test

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	tlogs "github.com/openshift-pipelines/pipelines-as-code/test/pkg/podlogs"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

func TestGithubPullRequestRefreshQueuedToken(t *testing.T) {
	startTime := time.Now()
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
	assertQueuedTokenRefreshLogged(ctx, t, run, secretName, startTime)

	finished, err := wait.UntilPipelineRunHasReason(ctx, run.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err, "the private clone must succeed on its first attempt")
	assert.Equal(t, len(finished), 1)
	assert.Equal(t, finished[0].UID, pr.UID)
}

func TestGithubPullRequestRefreshQueuedTokenOnQueueRelease(t *testing.T) {
	startTime := time.Now()
	ctx, run, opts, provider, err := tgithub.Setup(t.Context(), false, false)
	assert.NilError(t, err)
	repo, _, err := provider.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
	assert.NilError(t, err)
	if !repo.GetPrivate() {
		t.Skip("TEST_GITHUB_REPO_OWNER_GITHUBAPP must point to a private repository for this test")
	}
	opts.Concurrency = 1

	g := &tgithub.PRTest{
		Label: "Renew a queued GitHub App token released by concurrency",
		YamlFiles: []string{
			"testdata/queued-token-00-long-running.yaml",
			"testdata/queued-token-01-git-clone.yaml",
		},
		NoStatusCheck: true,
		Cnx:           run,
		Options:       opts,
		Provider:      provider,
	}
	defer g.TearDown(ctx, t)
	g.RunPullRequest(ctx, t)

	runs := run.Clients.Tekton.TektonV1().PipelineRuns(g.TargetNamespace)

	// Both PipelineRuns are created pending under the concurrency limit, so wait
	// for the controller to start the first one instead of sampling the list
	// once. PaC resolves repo PipelineRuns with a generateName, so the created
	// objects carry a random suffix and are matched on GenerateName.
	const (
		longRunningGenName = "queued-token-00-long-running-"
		cloneGenName       = "queued-token-01-git-clone-"
	)
	var queued tektonv1.PipelineRun
	err = kwait.PollUntilContextTimeout(ctx, time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		prList, err := runs.List(ctx, metav1.ListOptions{LabelSelector: keys.SHA + "=" + g.SHA})
		if err != nil {
			return false, err
		}
		var pending []tektonv1.PipelineRun
		for _, pr := range prList.Items {
			if len(pr.Status.Conditions) > 0 && pr.Status.Conditions[0].Reason == tektonv1.PipelineRunReasonPending.String() {
				pending = append(pending, pr)
			}
		}
		if len(pending) != 1 || pending[0].GenerateName != cloneGenName {
			return false, nil
		}
		queued = pending[0]
		return true, nil
	})
	assert.NilError(t, err, "the clone PipelineRun should be the only one queued under the concurrency limit")
	secretName := queued.Annotations[keys.GitAuthSecret]
	assert.Assert(t, secretName != "", "PaC did not record the git-auth Secret name on queued run")

	prList, err := runs.List(ctx, metav1.ListOptions{LabelSelector: keys.SHA + "=" + g.SHA})
	assert.NilError(t, err)
	assert.Equal(t, len(prList.Items), 2)
	var longRunningName string
	var longRunningUID types.UID
	for _, pr := range prList.Items {
		if pr.UID == queued.UID {
			continue
		}
		assert.Equal(t, pr.GenerateName, longRunningGenName)
		longRunningName = pr.Name
		longRunningUID = pr.UID
	}
	assert.Assert(t, longRunningUID != "", "long-running PipelineRun was not found")

	secrets := run.Clients.Kube.CoreV1().Secrets(queued.Namespace)
	var secret *corev1.Secret
	err = kwait.PollUntilContextTimeout(ctx, time.Second, time.Minute, true, func(ctx context.Context) (bool, error) {
		secret, err = secrets.Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if !slices.ContainsFunc(secret.OwnerReferences, func(ref metav1.OwnerReference) bool {
			return ref.Kind == "PipelineRun" && ref.UID == queued.UID
		}) {
			return false, nil
		}
		_, err = time.Parse(time.RFC3339, secret.Annotations[keys.GitHubTokenExpiry])
		return err == nil, nil
	})
	assert.NilError(t, err, "wait for PaC to attach and timestamp the queued git-auth Secret")

	credential, err := url.Parse(string(secret.Data[".git-credentials"]))
	if err != nil {
		t.Fatal("git-auth Secret has an invalid clone credential URL")
	}
	assert.Assert(t, credential.User != nil, "git-auth Secret has no clone credential")
	const invalidToken = "invalid-e2e-token"
	credential.User = url.UserPassword(credential.User.Username(), invalidToken)
	secret.Data[".git-credentials"] = []byte(credential.String())
	secret.Data["git-provider-token"] = []byte(invalidToken)
	secret.Annotations[keys.GitHubTokenExpiry] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	secret, err = secrets.Update(ctx, secret, metav1.UpdateOptions{})
	assert.NilError(t, err)
	secretUID := secret.UID

	// The sibling must still hold the only concurrency slot. Once it finishes,
	// the queue releases the clone run and the admission refresh would race the
	// tampering above.
	sibling, err := runs.Get(ctx, longRunningName, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Assert(t, !sibling.IsDone(), "long-running PipelineRun finished before its queued sibling was expired; increase its sleep")

	waitSucceeded := wait.Opts{
		Namespace:                 g.TargetNamespace,
		TargetSHA:                 []string{g.SHA},
		MinNumberStatus:           2,
		PollTimeout:               10 * time.Minute,
		FailOnOtherTerminalReason: true,
	}
	succeeded, err := wait.UntilPipelineRunHasReason(ctx, run.Clients, tektonv1.PipelineRunReasonSuccessful, waitSucceeded)
	assert.NilError(t, err, "concurrency release should refresh the queued token and keep both runs successful")
	assert.Equal(t, len(succeeded), 2)
	assert.Assert(t, slices.ContainsFunc(succeeded, func(pr tektonv1.PipelineRun) bool {
		return pr.UID == queued.UID
	}), "git-clone queued PipelineRun was not successful")
	assert.Assert(t, slices.ContainsFunc(succeeded, func(pr tektonv1.PipelineRun) bool {
		return pr.UID == longRunningUID
	}), "long-running sibling PipelineRun was not successful")

	refreshed, err := secrets.Get(ctx, secretName, metav1.GetOptions{})
	assert.NilError(t, err)
	assert.Equal(t, refreshed.UID, secretUID)
	assert.Assert(t, string(refreshed.Data["git-provider-token"]) != invalidToken, "expected token refresh before queued release")
	expiry, err := time.Parse(time.RFC3339, refreshed.Annotations[keys.GitHubTokenExpiry])
	assert.NilError(t, err)
	assert.Assert(t, expiry.After(time.Now()), "expected refreshed token expiry to be in the future")
	assertQueuedTokenRefreshLogged(ctx, t, run, secretName, startTime)
}

// assertQueuedTokenRefreshLogged checks that the admission webhook logged the
// refresh of this Secret, proving the token was renewed by PaC and not by a
// retry or a later reconcile.
func assertQueuedTokenRefreshLogged(ctx context.Context, t *testing.T, run *params.Run, secretName string, since time.Time) {
	t.Helper()
	reg := regexp.MustCompile(`refreshed queued GitHub token before release.*` + regexp.QuoteMeta(secretName))
	sinceSeconds := int64(time.Since(since).Seconds()) + 60
	var output string
	err := kwait.PollUntilContextTimeout(ctx, 5*time.Second, time.Minute, true, func(ctx context.Context) (bool, error) {
		var err error
		output, err = tlogs.GetPodLog(ctx, run.Clients.Kube.CoreV1(), info.GetNS(ctx), "app.kubernetes.io/name=webhook", "pac-webhook", nil, &sinceSeconds)
		if err != nil {
			return false, err
		}
		return reg.MatchString(output), nil
	})
	assert.NilError(t, err, fmt.Sprintf("webhook did not log a token refresh for Secret %s", secretName))
}
