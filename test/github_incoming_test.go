//go:build e2e

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	repository "github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/secret"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	incomingSecretName   = "pac-incoming-secret"
	incomingSecreteValue = "shhhh-secrete"
)

// TestGithubAppIncoming tests that a Pipelinerun with the incoming event
// gets created despite the presence of multiple Pipelineruns in the .tekton directory with
// eventType as incoming.
func TestGithubGHEAppIncoming(t *testing.T) {
	randomedString := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml", ".tekton/pr.yaml": "testdata/pipelinerun.yaml",
	}, randomedString, randomedString, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	verifyIncomingWebhook(t, randomedString, "pipelinerun-incoming", entries, []string{randomedString}, false, 1)
}

func TestGithubGHEIncoming(t *testing.T) {
	randomedString := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml", ".tekton/pr.yaml": "testdata/pipelinerun.yaml",
	}, randomedString, randomedString, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	verifyIncomingWebhook(t, randomedString, "pipelinerun-incoming", entries, []string{randomedString}, false, 1)
}

func TestGithubGHEWebhookIncoming(t *testing.T) {
	randomedString := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	// Create entries with different event types to test that only incoming PipelineRun gets triggered
	incomingEntries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml",
	}, randomedString, randomedString, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	prEntries, err := payload.GetEntries(map[string]string{
		".tekton/pr.yaml": "testdata/pipelinerun.yaml",
	}, randomedString, randomedString, triggertype.PullRequest.String(), map[string]string{})
	assert.NilError(t, err)

	// Merge the entries - incoming PipelineRun triggers on "incoming", PR PipelineRun triggers on "pull_request"
	entries := make(map[string]string)
	for k, v := range incomingEntries {
		entries[k] = v
	}
	for k, v := range prEntries {
		entries[k] = v
	}

	verifyIncomingWebhook(t, randomedString, "pipelinerun-incoming", entries, []string{randomedString}, true, 1)
}

// TestGithubAppIncomingForDifferentEvent tests that a Pipelinerun with the incoming event
// gets created despite the presence of multiple Pipelineruns in the .tekton directory,
// where one has an eventType as incoming and another as pull_request.
func TestGithubGHEAppIncomingForDifferentEvent(t *testing.T) {
	randomedString := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming-generatename.yaml", ".tekton/pr.yaml": "testdata/pipelinerun.yaml",
	}, randomedString, randomedString, triggertype.PullRequest.String(), map[string]string{})
	assert.NilError(t, err)

	verifyIncomingWebhook(t, randomedString, "pipelinerun-incoming-", entries, []string{randomedString}, false, 1)
}

// TestGithubAppIncomingGlobPattern tests incoming webhook with glob pattern matching.
func TestGithubGHEAppIncomingGlobPattern(t *testing.T) {
	randomedString := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml",
	}, randomedString, randomedString, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	// Test with glob pattern that matches the branch name
	// Pattern: pac-e2e-ns* should match branch pac-e2e-ns-xxxxx
	verifyIncomingWebhook(t, randomedString, "pipelinerun-incoming", entries, []string{"pac-e2e-ns*"}, false, 1)
}

// TestGithubAppIncomingGlobPrefixPattern tests incoming webhook with prefix glob pattern.
func TestGithubGHEAppIncomingGlobPrefixPattern(t *testing.T) {
	// Create a branch name with "feature-" prefix (using hyphen instead of slash for Kubernetes compliance)
	randomedString := fmt.Sprintf("feature-%s", names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e"))

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml",
	}, randomedString, randomedString, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	// Test with glob pattern that matches feature branches
	verifyIncomingWebhook(t, randomedString, "pipelinerun-incoming", entries, []string{"feature-*"}, false, 1)
}

// TestGithubAppIncomingGlobFirstMatchWins tests first-match-wins with multiple glob targets.
func TestGithubGHEAppIncomingGlobFirstMatchWins(t *testing.T) {
	randomedString := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml",
	}, randomedString, randomedString, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	// Multiple patterns - first one that matches should win
	// Both pac-e2e-ns* and * will match, but first should win
	verifyIncomingWebhook(t, randomedString, "pipelinerun-incoming", entries, []string{"pac-e2e-ns*", "*"}, false, 1)
}

// TestGithubAppIncomingNoMatch tests that incoming webhook fails when branch doesn't match any target.
func TestGithubGHEAppIncomingNoMatch(t *testing.T) {
	randomedString := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml",
	}, randomedString, randomedString, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	// Test with glob pattern that will NOT match the branch
	// Pattern: production-* should NOT match branch pac-e2e-ns-xxxxx
	verifyIncomingWebhook(t, randomedString, "pipelinerun-incoming", entries, []string{"production-*"}, false, 0)
}

// TestGithubAppIncomingMultiplePatternsNoMatch tests that incoming webhook fails when none of the patterns match.
func TestGithubGHEAppIncomingMultiplePatternsNoMatch(t *testing.T) {
	randomedString := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml",
	}, randomedString, randomedString, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	// Multiple patterns that all don't match the branch
	verifyIncomingWebhook(t, randomedString, "pipelinerun-incoming", entries, []string{"production-*", "staging-*", "release-*"}, false, 0)
}

// TestGithubAppIncomingNoMatchExactName tests that exact non-matching string doesn't match.
func TestGithubGHEAppIncomingNoMatchExactName(t *testing.T) {
	randomedString := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml",
	}, randomedString, randomedString, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	// Test with exact branch name that doesn't match
	verifyIncomingWebhook(t, randomedString, "pipelinerun-incoming", entries, []string{"main", "develop", "staging"}, false, 0)
}

func verifyIncomingWebhook(t *testing.T, randomedString, pipelinerunName string, entries map[string]string, targets []string, onWebhook bool, numberOfPR int) {
	ctx := context.Background()
	onGHE := true // All incoming webhook tests use GHE
	ctx, runcnx, opts, ghprovider, err := tgithub.Setup(ctx, onGHE, onWebhook)
	assert.NilError(t, err)
	label := "GithubApp Incoming"
	if numberOfPR == 0 {
		label = "GithubApp Incoming No Match"
	}
	logmsg := fmt.Sprintf("Testing %s with Github APPS integration on %s with targets %v", label, randomedString, targets)
	runcnx.Clients.Log.Info(logmsg)

	var repoinfo *github.Repository
	var dynamicRepoName string

	// For GHE + webhook, create a dynamic repo with SMEE webhook
	if onGHE && onWebhook {
		repoName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
		smeeURL := os.Getenv("TEST_GITHUB_SECOND_WEBHOOK_SMEE_URL")
		webhookSecret := os.Getenv("TEST_EL_WEBHOOK_SECRET")

		runcnx.Clients.Log.Infof("Creating dynamic GHE repository %s/%s with webhook to %s", opts.Organization, repoName, smeeURL)
		repoinfo, err = tgithub.CreateGHERepo(ctx, ghprovider.Client(), opts.Organization, repoName, smeeURL, webhookSecret, runcnx.Clients.Log)
		assert.NilError(t, err)

		opts.Repo = repoName
		dynamicRepoName = repoName
	} else {
		// Use existing pre-configured repo
		var resp *github.Response
		repoinfo, resp, err = ghprovider.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
		assert.NilError(t, err)
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			t.Errorf("Repository %s not found in %s", opts.Organization, opts.Repo)
		}
	}

	incoming := &[]v1alpha1.Incoming{
		{
			Type: "webhook-url",
			Secret: v1alpha1.Secret{
				Name: incomingSecretName,
				Key:  "incoming",
			},
			Targets: targets,
			Params: []string{
				"the_best_superhero_is",
			},
		},
	}

	err = tgithub.CreateCRDIncoming(ctx, t, repoinfo, runcnx, incoming, opts, ghprovider, randomedString)
	assert.NilError(t, err)

	err = secret.Create(ctx, runcnx, map[string]string{"incoming": incomingSecreteValue}, randomedString, incomingSecretName)
	assert.NilError(t, err)

	targetRefName := fmt.Sprintf("refs/heads/%s", randomedString)

	title := "TestGithubAppIncoming - " + randomedString
	sha, vref, err := tgithub.PushFilesToRef(ctx, ghprovider.Client(), title,
		repoinfo.GetDefaultBranch(),
		targetRefName,
		opts.Organization,
		opts.Repo,
		entries)
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Commit %s has been created and pushed to branch %s", sha, vref.GetURL())

	var req *http.Request
	var incomingURL string
	client := &http.Client{}

	if onWebhook {
		// Legacy URL query parameters method
		incomingURL = fmt.Sprintf("%s/incoming?repository=%s&branch=%s&pipelinerun=%s&secret=%s",
			opts.ControllerURL, randomedString, randomedString, pipelinerunName, incomingSecreteValue)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, incomingURL, strings.NewReader(`{"params":{"the_best_superhero_is":"Superman"}}`))
		assert.NilError(t, err)
		req.Header.Add("Content-Type", "application/json")
	} else {
		// JSON body method
		incomingURL = fmt.Sprintf("%s/incoming", opts.ControllerURL)
		jsonBody := map[string]interface{}{
			"repository":  randomedString,
			"branch":      randomedString,
			"pipelinerun": pipelinerunName,
			"secret":      incomingSecreteValue,
			"params": map[string]string{
				"the_best_superhero_is": "Superman",
			},
		}
		jsonData, err := json.Marshal(jsonBody)
		assert.NilError(t, err)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, incomingURL, strings.NewReader(string(jsonData)))
		assert.NilError(t, err)
		req.Header.Add("Content-Type", "application/json")
	}
	if onGHE {
		urlParse, _ := url.Parse(*ghprovider.APIURL)
		req.Header.Add("X-GitHub-Enterprise-Host", urlParse.Host)
	}
	assert.NilError(t, err)
	httpResp, err := client.Do(req)
	assert.NilError(t, err)
	defer httpResp.Body.Close()

	g := tgithub.PRTest{
		Cnx:             runcnx,
		Options:         opts,
		Provider:        ghprovider,
		TargetNamespace: randomedString,
		TargetRefName:   targetRefName,
		PRNumber:        -1,
		SHA:             sha,
		Logger:          runcnx.Clients.Log,
		Webhook:         onWebhook,
		GHE:             onGHE,
		DynamicRepoName: dynamicRepoName,
	}
	defer g.TearDown(ctx, t)

	if numberOfPR > 0 {
		runcnx.Clients.Log.Infof("Kicked off on incoming URL: %s with branch: %s, targets: %v", incomingURL, randomedString, targets)
		assert.Assert(t, httpResp.StatusCode >= 200 && httpResp.StatusCode < 300, "HTTP status code mismatch: expected=2xx, actual=%d (branch: %s, targets: %v)", httpResp.StatusCode, randomedString, targets)

		sopt := wait.SuccessOpt{
			Title:           title,
			OnEvent:         triggertype.Incoming.String(),
			TargetNS:        randomedString,
			NumberofPRMatch: numberOfPR,
			SHA:             "",
		}
		wait.Succeeded(ctx, t, runcnx, opts, sopt)
	} else {
		runcnx.Clients.Log.Infof("Attempted incoming URL: %s with branch: %s, targets: %v", incomingURL, randomedString, targets)

		// Wait a bit to ensure no PipelineRun gets created.
		time.Sleep(5 * time.Second)
	}

	prsNew, err := runcnx.Clients.Tekton.TektonV1().PipelineRuns(randomedString).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", keys.SHA, sha),
	})
	assert.NilError(t, err)
	assert.Assert(t, len(prsNew.Items) == numberOfPR, "PipelineRun count mismatch: expected=%d, actual=%d (branch: %s, targets: %v)", numberOfPR, len(prsNew.Items), randomedString, targets)

	if numberOfPR > 0 {
		prName := prsNew.Items[0].GetName()
		index := strings.LastIndex(prsNew.Items[0].GetGenerateName(), "-")
		if index != -1 {
			prName = prsNew.Items[0].GetGenerateName()[:index]
		}
		assert.Assert(t, prName == "pipelinerun-incoming", "Expected PipelineRun name 'pipelinerun-incoming', got '%s'", prName)

		err = wait.RegexpMatchingInPodLog(context.Background(), runcnx, randomedString, "pipelinesascode.tekton.dev/event-type=incoming", "step-task", *regexp.MustCompile(".*It's a Bird... It's a Plane... It's Superman"), "", 2, nil)
		assert.NilError(t, err, "Error while checking the logs of the pods")
	} else {
		runcnx.Clients.Log.Infof("Successfully verified no PipelineRun was created for non-matching branch %s with targets %v", randomedString, targets)
	}
}

// TestGithubGHEAppIncomingDefaultBranchProvenance tests that incoming webhooks
// work correctly with pipelinerun_provenance set to "default_branch".
// This verifies the fix for https://github.com/tektoncd/pipelines-as-code/issues/2646
// where DefaultBranch was not populated for incoming events.
func TestGithubGHEAppIncomingDefaultBranchProvenance(t *testing.T) {
	ctx := context.Background()
	onGHE := true
	ctx, runcnx, opts, ghprovider, err := tgithub.Setup(ctx, onGHE, false)
	assert.NilError(t, err)

	randomedString := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	targetBranch := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("incoming-branch")

	runcnx.Clients.Log.Infof("Testing incoming webhook with default_branch provenance on %s", randomedString)

	repoinfo, resp, err := ghprovider.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
	assert.NilError(t, err)
	if resp != nil && resp.StatusCode == http.StatusNotFound {
		t.Errorf("Repository %s not found in %s", opts.Organization, opts.Repo)
	}

	incoming := &[]v1alpha1.Incoming{
		{
			Type: "webhook-url",
			Secret: v1alpha1.Secret{
				Name: incomingSecretName,
				Key:  "incoming",
			},
			Targets: []string{targetBranch},
			Params: []string{
				"the_best_superhero_is",
			},
		},
	}

	// Create Repository CR with pipelinerun_provenance: default_branch
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name: randomedString,
		},
		Spec: v1alpha1.RepositorySpec{
			URL:       repoinfo.GetHTMLURL(),
			Incomings: incoming,
			Settings: &v1alpha1.Settings{
				PipelineRunProvenance: "default_branch",
			},
		},
	}

	err = repository.CreateNS(ctx, randomedString, runcnx)
	assert.NilError(t, err)
	err = repository.CreateRepo(ctx, randomedString, runcnx, repo)
	assert.NilError(t, err)

	err = secret.Create(ctx, runcnx, map[string]string{"incoming": incomingSecreteValue}, randomedString, incomingSecretName)
	assert.NilError(t, err)

	// Push .tekton/ files to the DEFAULT branch (not the incoming target branch)
	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml",
	}, randomedString, targetBranch, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	defaultBranch := repoinfo.GetDefaultBranch()
	title := "TestGithubAppIncomingDefaultBranchProvenance - " + randomedString
	// Push .tekton/ to default branch by creating a commit and updating the ref
	// (PushFilesToRef with empty targetRef creates the commit without creating a new ref)
	sha, _, err := tgithub.PushFilesToRef(ctx, ghprovider.Client(), title,
		defaultBranch,
		"",
		opts.Organization,
		opts.Repo,
		entries)
	assert.NilError(t, err)
	// Update the existing default branch ref to point to the new commit
	_, _, err = ghprovider.Client().Git.UpdateRef(ctx, opts.Organization, opts.Repo,
		"refs/heads/"+defaultBranch, github.UpdateRef{SHA: sha})
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Commit %s has been created and pushed to default branch %s", sha, defaultBranch)

	// Create the target branch (the incoming webhook targets this branch)
	targetSHA, _, err := tgithub.PushFilesToRef(ctx, ghprovider.Client(),
		title,
		defaultBranch,
		fmt.Sprintf("refs/heads/%s", targetBranch),
		opts.Organization,
		opts.Repo,
		map[string]string{"README.md": "# target branch"})
	assert.NilError(t, err)
	runcnx.Clients.Log.Infof("Target branch %s created with SHA %s", targetBranch, targetSHA)

	// Trigger incoming webhook targeting the non-default branch
	incomingURL := fmt.Sprintf("%s/incoming", opts.ControllerURL)
	jsonBody := map[string]interface{}{
		"repository":  randomedString,
		"branch":      targetBranch,
		"pipelinerun": "pipelinerun-incoming",
		"secret":      incomingSecreteValue,
		"params": map[string]string{
			"the_best_superhero_is": "Superman",
		},
	}
	jsonData, err := json.Marshal(jsonBody)
	assert.NilError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, incomingURL, strings.NewReader(string(jsonData)))
	assert.NilError(t, err)
	req.Header.Add("Content-Type", "application/json")
	if onGHE {
		urlParse, _ := url.Parse(*ghprovider.APIURL)
		req.Header.Add("X-GitHub-Enterprise-Host", urlParse.Host)
	}

	client := &http.Client{}
	httpResp, err := client.Do(req)
	assert.NilError(t, err)
	defer httpResp.Body.Close()
	assert.Assert(t, httpResp.StatusCode >= 200 && httpResp.StatusCode < 300,
		"HTTP status code mismatch: expected=2xx, actual=%d", httpResp.StatusCode)

	runcnx.Clients.Log.Infof("Incoming webhook triggered on URL: %s for branch: %s", incomingURL, targetBranch)

	g := tgithub.PRTest{
		Cnx:             runcnx,
		Options:         opts,
		Provider:        ghprovider,
		TargetNamespace: randomedString,
		TargetRefName:   fmt.Sprintf("refs/heads/%s", targetBranch),
		PRNumber:        -1,
		SHA:             targetSHA,
		Logger:          runcnx.Clients.Log,
		GHE:             onGHE,
	}
	defer g.TearDown(ctx, t)

	sopt := wait.SuccessOpt{
		Title:           title,
		OnEvent:         triggertype.Incoming.String(),
		TargetNS:        randomedString,
		NumberofPRMatch: 1,
		SHA:             "",
	}
	wait.Succeeded(ctx, t, runcnx, opts, sopt)

	// Verify the PipelineRun was created
	prs, err := runcnx.Clients.Tekton.TektonV1().PipelineRuns(randomedString).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prs.Items) == 1, "Expected 1 PipelineRun, got %d", len(prs.Items))

	err = wait.RegexpMatchingInPodLog(context.Background(), runcnx, randomedString,
		"pipelinesascode.tekton.dev/event-type=incoming", "step-task",
		*regexp.MustCompile(".*It's a Bird... It's a Plane... It's Superman"), "", 2, nil)
	assert.NilError(t, err, "Error while checking the logs of the pods")
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -run TestGithubAppIncoming ."
// End:
