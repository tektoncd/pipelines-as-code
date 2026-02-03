package github

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-github/v81/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	ghprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"github.com/tektoncd/pipeline/pkg/names"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
)

func PushFilesToRef(ctx context.Context, client *github.Client, commitMessage, baseBranch, targetRef, owner, repo string, files map[string]string) (string, *github.Reference, error) {
	maintree, _, err := client.Git.GetTree(ctx, owner, repo, baseBranch, false)
	if err != nil {
		return "", nil, fmt.Errorf("error getting tree: %w", err)
	}
	mainSha := maintree.GetSHA()
	entries := []*github.TreeEntry{}
	defaultMode := "100644"
	for path, fcontent := range files {
		content := base64.StdEncoding.EncodeToString([]byte(fcontent))
		encoding := "base64"
		blob, _, err := client.Git.CreateBlob(ctx, owner, repo, github.Blob{
			Content:  &content,
			Encoding: &encoding,
		})
		if err != nil {
			return "", nil, fmt.Errorf("error creating blobs: %w", err)
		}
		sha := blob.GetSHA()

		_path := path
		entries = append(entries,
			&github.TreeEntry{
				Path: &_path,
				Mode: &defaultMode,
				SHA:  &sha,
			})
	}

	tree, _, err := client.Git.CreateTree(ctx, owner, repo, mainSha, entries)
	if err != nil {
		return "", nil, err
	}

	commitAuthor := "OpenShift Pipelines E2E test"
	commitEmail := "e2e-pipelines@redhat.com"
	commit, _, err := client.Git.CreateCommit(ctx, owner, repo, github.Commit{
		Author: &github.CommitAuthor{
			Name:  &commitAuthor,
			Email: &commitEmail,
		},
		Message: &commitMessage,
		Tree:    tree,
		Parents: []*github.Commit{
			{
				SHA: &mainSha,
			},
		},
	}, &github.CreateCommitOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("error creating commit: %w", err)
	}

	// Only create a new ref if targetRef is provided
	// If empty, caller is responsible for updating an existing ref
	if targetRef != "" {
		createRef := github.CreateRef{
			Ref: targetRef,
			SHA: *commit.SHA,
		}
		vref, _, err := client.Git.CreateRef(ctx, owner, repo, createRef)
		if err != nil {
			return "", nil, fmt.Errorf("error creating ref: %w", err)
		}
		return commit.GetSHA(), vref, nil
	}

	return commit.GetSHA(), nil, nil
}

func PRCreate(ctx context.Context, cs *params.Run, ghcnx *ghprovider.Provider, owner, repo, targetRef, defaultBranch, title string) (int, error) {
	pr, _, err := ghcnx.Client().PullRequests.Create(ctx, owner, repo, &github.NewPullRequest{
		Title: &title,
		Head:  &targetRef,
		Base:  &defaultBranch,
		Body:  github.Ptr("Add a new PR for testing"),
	})
	if err != nil {
		return -1, err
	}
	cs.Clients.Log.Infof("Pull request created: %s", pr.GetHTMLURL())
	return pr.GetNumber(), nil
}

type PRTest struct {
	Label         string
	YamlFiles     []string
	GHE           bool
	Webhook       bool
	NoStatusCheck bool

	Cnx             *params.Run
	Options         options.E2E
	Provider        *ghprovider.Provider
	TargetNamespace string
	TargetRefName   string
	PRNumber        int
	SHA             string
	Logger          *zap.SugaredLogger
	CommitTitle     string
	DynamicRepoName string
}

func (g *PRTest) RunPullRequest(ctx context.Context, t *testing.T) {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")

	ctx, runcnx, opts, ghcnx, err := Setup(ctx, g.GHE, g.Webhook)
	assert.NilError(t, err)
	g.Logger = runcnx.Clients.Log
	g.Cnx = runcnx
	preSettings := g.Options.Settings
	g.Options = opts
	if preSettings.Github != nil {
		g.Options.Settings = preSettings
	}
	g.Provider = ghcnx
	g.TargetNamespace = targetNS
	g.CommitTitle = fmt.Sprintf("Testing %s with Github APPS integration on %s", g.Label, targetNS)
	g.Logger.Info(g.CommitTitle)

	var repoinfo *github.Repository

	// For GHE + webhook, create a dynamic repo with SMEE webhook
	if g.GHE && g.Webhook {
		repoName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
		smeeURL := os.Getenv("TEST_GITHUB_SECOND_WEBHOOK_SMEE_URL")
		webhookSecret := os.Getenv("TEST_EL_WEBHOOK_SECRET")

		g.Logger.Infof("Creating dynamic GHE repository %s/%s with webhook to %s", opts.Organization, repoName, smeeURL)
		repoinfo, err = CreateGHERepo(ctx, ghcnx.Client(), opts.Organization, repoName, smeeURL, webhookSecret, g.Logger)
		assert.NilError(t, err)

		opts.Repo = repoName
		g.Options.Repo = repoName
		g.DynamicRepoName = repoName
	} else {
		// Use existing pre-configured repo
		var resp *github.Response
		repoinfo, resp, err = ghcnx.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
		assert.NilError(t, err)
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			t.Errorf("Repository %s not found in %s", opts.Organization, opts.Repo)
		}
	}

	if g.Options.Settings != nil {
		opts.Settings = g.Options.Settings
	}
	err = CreateCRD(ctx, t, repoinfo, runcnx, opts, ghcnx, targetNS)
	assert.NilError(t, err)

	yamlEntries := map[string]string{}
	for _, v := range g.YamlFiles {
		yamlEntries[filepath.Join(".tekton", filepath.Base(v))] = v
	}

	entries, err := payload.GetEntries(yamlEntries, targetNS, options.MainBranch, triggertype.PullRequest.String(),
		map[string]string{"TargetURL": repoinfo.GetHTMLURL(), "SourceURL": repoinfo.GetHTMLURL()})
	assert.NilError(t, err)

	targetRefName := fmt.Sprintf("refs/heads/%s",
		names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test"))
	g.TargetRefName = targetRefName

	sha, vref, err := PushFilesToRef(ctx, ghcnx.Client(), g.CommitTitle, repoinfo.GetDefaultBranch(), targetRefName,
		opts.Organization, opts.Repo, entries)
	assert.NilError(t, err)
	g.SHA = sha

	g.Logger.Infof("Commit %s has been created and pushed to %s", sha, vref.GetURL())
	number, err := PRCreate(ctx, runcnx, ghcnx, opts.Organization,
		opts.Repo, targetRefName, repoinfo.GetDefaultBranch(), g.CommitTitle)
	assert.NilError(t, err)
	g.PRNumber = number

	if !g.NoStatusCheck {
		sopt := wait.SuccessOpt{
			Title:           g.CommitTitle,
			OnEvent:         triggertype.PullRequest.String(),
			TargetNS:        targetNS,
			NumberofPRMatch: len(g.YamlFiles),
			SHA:             sha,
		}
		wait.Succeeded(ctx, t, runcnx, opts, sopt)
	}
}

func (g *PRTest) TearDown(ctx context.Context, t *testing.T) {
	if os.Getenv("TEST_NOCLEANUP") == "true" {
		if g.Logger != nil {
			g.Logger.Infof("Not cleaning up and closing PR since TEST_NOCLEANUP is set")
		}
		return
	}

	// Collect GitHub API call information from controller logs
	g.collectGitHubAPICalls(ctx, t)

	if g.PRNumber != -1 && g.Provider != nil && g.Logger != nil {
		g.Logger.Infof("Closing PR %d", g.PRNumber)
		state := "closed"
		_, _, err := g.Provider.Client().PullRequests.Edit(ctx,
			g.Options.Organization, g.Options.Repo, g.PRNumber,
			&github.PullRequest{State: &state})
		if err != nil {
			t.Fatal(err)
		}
	}
	if g.TargetNamespace != "" && g.Cnx != nil {
		repository.NSTearDown(ctx, t, g.Cnx, g.TargetNamespace)
	}

	// Skip branch deletion for dynamic repos since we're deleting the entire repo
	if g.DynamicRepoName == "" && g.TargetRefName != "" && g.TargetRefName != options.MainBranch && g.Provider != nil && g.Logger != nil {
		branch := fmt.Sprintf("heads/%s", filepath.Base(g.TargetRefName))
		g.Logger.Infof("Deleting Ref %s", branch)
		_, err := g.Provider.Client().Git.DeleteRef(ctx, g.Options.Organization, g.Options.Repo, branch)
		assert.NilError(t, err)
	}

	// Delete dynamic repo if one was created
	if g.DynamicRepoName != "" && g.Provider != nil && g.Logger != nil {
		g.Logger.Infof("Deleting dynamic repository %s/%s", g.Options.Organization, g.DynamicRepoName)
		err := DeleteGHERepo(ctx, g.Provider.Client(), g.Options.Organization, g.DynamicRepoName, g.Logger)
		assert.NilError(t, err)
	}
}

func (g *PRTest) RunPushRequest(ctx context.Context, t *testing.T) {
	targetNS := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-push")

	targetBranch := g.TargetRefName
	if targetBranch == "" {
		targetBranch = targetNS
	}
	targetEvent := "push"
	ctx, runcnx, opts, ghcnx, err := Setup(ctx, g.GHE, g.Webhook)
	assert.NilError(t, err)
	g.Logger = runcnx.Clients.Log
	g.Cnx = runcnx
	preSettings := g.Options.Settings
	g.Options = opts
	if preSettings.Github != nil {
		g.Options.Settings = preSettings
	}
	g.Provider = ghcnx
	g.TargetNamespace = targetNS
	g.PRNumber = -1

	var logmsg string
	if g.Webhook {
		logmsg = fmt.Sprintf("Testing %s with Direct Webhook integration on %s", g.Label, targetNS)
		g.Logger.Info(logmsg)
	} else {
		logmsg = fmt.Sprintf("Testing %s with Github APPS integration on %s", g.Label, targetNS)
		g.Logger.Info(logmsg)
	}

	var repoinfo *github.Repository

	// For GHE + webhook, create a dynamic repo with SMEE webhook
	if g.GHE && g.Webhook {
		repoName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
		smeeURL := os.Getenv("TEST_GITHUB_SECOND_WEBHOOK_SMEE_URL")
		webhookSecret := os.Getenv("TEST_EL_WEBHOOK_SECRET")

		g.Logger.Infof("Creating dynamic GHE repository %s/%s with webhook to %s", opts.Organization, repoName, smeeURL)
		repoinfo, err = CreateGHERepo(ctx, ghcnx.Client(), opts.Organization, repoName, smeeURL, webhookSecret, g.Logger)
		assert.NilError(t, err)

		opts.Repo = repoName
		g.Options.Repo = repoName
		g.DynamicRepoName = repoName
	} else {
		// Use existing pre-configured repo
		var resp *github.Response
		repoinfo, resp, err = ghcnx.Client().Repositories.Get(ctx, opts.Organization, opts.Repo)
		assert.NilError(t, err)
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			t.Errorf("Repository %s not found in %s", opts.Organization, opts.Repo)
		}
	}

	if g.Options.Settings.Github != nil {
		opts.Settings = g.Options.Settings
	}
	err = CreateCRD(ctx, t, repoinfo, runcnx, opts, ghcnx, targetNS)
	assert.NilError(t, err)

	yamlEntries := map[string]string{}
	for _, v := range g.YamlFiles {
		yamlEntries[filepath.Join(".tekton", filepath.Base(v))] = v
	}

	entries, err := payload.GetEntries(yamlEntries,
		targetNS, targetBranch, targetEvent, map[string]string{})
	assert.NilError(t, err)

	targetRefName := targetBranch
	g.TargetRefName = targetRefName
	cloneURL, err := scm.MakeGitCloneURL(repoinfo.GetCloneURL(), "git", *ghcnx.Token)
	assert.NilError(t, err)
	scmOpts := scm.Opts{
		GitURL:        cloneURL,
		TargetRefName: targetRefName,
		BaseRefName:   repoinfo.GetDefaultBranch(),
		WebURL:        repoinfo.GetHTMLURL(),
		Log:           runcnx.Clients.Log,
		CommitTitle:   logmsg,
	}
	scm.PushFilesToRefGit(t, &scmOpts, entries)
	branch, _, err := ghcnx.Client().Repositories.GetBranch(ctx, opts.Organization, opts.Repo, targetBranch, 1)
	assert.NilError(t, err)
	sha := branch.GetCommit().GetSHA()
	g.SHA = sha
	g.Logger.Infof("Commit %s has been created and pushed to %s in branch %s", sha, branch.GetCommit().GetHTMLURL(), branch.GetName())
	assert.NilError(t, err)

	if !g.NoStatusCheck {
		sopt := wait.SuccessOpt{
			Title:           logmsg,
			OnEvent:         triggertype.Push.String(),
			TargetNS:        targetNS,
			NumberofPRMatch: len(g.YamlFiles),
			SHA:             sha,
		}
		wait.Succeeded(ctx, t, runcnx, opts, sopt)
	}
}

func UpdateFilesInRef(ctx context.Context, client *github.Client, owner, repo, branch, commitMessage string, files map[string]string) (string, error) {
	refName := "heads/" + branch
	ref, _, err := client.Git.GetRef(ctx, owner, repo, refName)
	if err != nil {
		return "", fmt.Errorf("error getting ref: %w", err)
	}
	baseSHA := ref.Object.GetSHA()

	// Pass empty targetRef and update the ref manually instead of creating it
	commitSHA, _, err := PushFilesToRef(ctx, client, commitMessage, baseSHA, "", owner, repo, files)
	if err != nil {
		return "", fmt.Errorf("error creating commit: %w", err)
	}

	updateRef := github.UpdateRef{SHA: commitSHA}
	_, _, err = client.Git.UpdateRef(ctx, owner, repo, refName, updateRef)
	if err != nil {
		return "", fmt.Errorf("error updating ref: %w", err)
	}

	return commitSHA, nil
}
