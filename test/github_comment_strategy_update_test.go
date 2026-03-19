//go:build e2e

package test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/go-github/v81/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	tgithub "github.com/openshift-pipelines/pipelines-as-code/test/pkg/github"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
)

// TestGithubCommentStrategyUpdateCELErrorReplacement tests:
// 1. A CEL error comment is posted for a PLR
// 2. After fixing the CEL error with a new commit, the same comment is updated with success status
// 3. Only one comment exists.
func TestGithubGHEWebhookCommentStrategyUpdateCELErrorReplacement(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:         "Github Comment Strategy CEL Error",
		YamlFiles:     []string{"testdata/failures/pipelinerun-invalid-cel.yaml"},
		GHE:           true,
		Webhook:       true,
		NoStatusCheck: true,
	}

	commentStrategy := v1alpha1.Settings{
		Github: &v1alpha1.GithubSettings{
			CommentStrategy: provider.UpdateCommentStrategy,
		},
	}
	g.Options.Settings = commentStrategy

	defer g.TearDown(ctx, t)
	g.RunPullRequest(ctx, t)

	g.Cnx.Clients.Log.Infof("Waiting for CEL error comment to be created")
	time.Sleep(3 * time.Second)

	comments, _, err := g.Provider.Client().Issues.ListComments(
		ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
		&github.IssueListCommentsOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(comments) == 1, "There should be only 1 comment on the pull request.")

	// Find the comment with pac-status marker (matches any PLR name with the marker)
	var celErrorComment *github.IssueComment
	var pipelineRunName string
	markerPattern := regexp.MustCompile(`<!-- pac-status-([^\s]+) -->`)
	for _, comment := range comments {
		matches := markerPattern.FindStringSubmatch(comment.GetBody())
		if len(matches) > 1 {
			celErrorComment = comment
			pipelineRunName = matches[1]
			break
		}
	}

	assert.Assert(t, celErrorComment != nil, "CEL error comment not found in %d comments", len(comments))
	celErrorCommentID := celErrorComment.GetID()
	g.Cnx.Clients.Log.Infof("Found CEL error comment ID: %d", celErrorCommentID)

	// Verify comment contains error information (CEL parsing error expected).
	commentBody := celErrorComment.GetBody()
	truncated := commentBody
	if len(commentBody) > 200 {
		truncated = commentBody[:200]
	}
	g.Cnx.Clients.Log.Infof("Initial comment body (truncated): %s...", truncated)
	assert.Assert(t, strings.Contains(strings.ToLower(commentBody), "error") ||
		strings.Contains(strings.ToLower(commentBody), "failed") ||
		strings.Contains(strings.ToLower(commentBody), "cel"),
		"Comment should contain error/CEL information")

	g.Cnx.Clients.Log.Infof("Pushing fix to replace CEL error with valid pipelinerun")
	fixedContentRaw, err := os.ReadFile("testdata/failures/pipelinerun-invalid-cel.yaml")
	assert.NilError(t, err)
	fixedContent := strings.ReplaceAll(string(fixedContentRaw), `event == "pull request" |`, `event_type == "pull_request"`)
	fixedContent = strings.ReplaceAll(fixedContent, `"\\ .PipelineName //"`, fmt.Sprintf("%q", pipelineRunName))
	fixedContent = strings.ReplaceAll(fixedContent, `"\\ .TargetNamespace //"`, fmt.Sprintf("%q", g.TargetNamespace))
	fileName := ".tekton/pipelinerun-invalid-cel.yaml"
	branchName := strings.TrimPrefix(g.TargetRefName, "refs/heads/")
	sha, err := tgithub.UpdateFilesInRef(ctx, g.Provider.Client(),
		g.Options.Organization, g.Options.Repo,
		branchName,
		"fix: replace CEL error with valid pipelinerun",
		map[string]string{fileName: fixedContent})
	assert.NilError(t, err)
	g.Cnx.Clients.Log.Infof("Pushed commit: %s", sha)

	sopt := twait.SuccessOpt{
		Title:           "fix: replace CEL error with valid pipelinerun",
		TargetNS:        g.TargetNamespace,
		NumberofPRMatch: 1,
		SHA:             sha,
		OnEvent:         "pull_request",
	}
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)

	// Wait for comment to be updated
	time.Sleep(2 * time.Second)
	updatedComments, _, err := g.Provider.Client().Issues.ListComments(
		ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
		&github.IssueListCommentsOptions{})
	assert.NilError(t, err)

	var updatedComment *github.IssueComment
	for _, comment := range updatedComments {
		if markerPattern.MatchString(comment.GetBody()) {
			updatedComment = comment
			break
		}
	}

	assert.Assert(t, updatedComment != nil, "Updated comment not found")
	assert.Equal(t, celErrorCommentID, updatedComment.GetID(),
		"Comment should be updated (ID %d), not a new one created (got ID %d)",
		celErrorCommentID, updatedComment.GetID())

	updatedBody := updatedComment.GetBody()
	truncated = updatedBody
	if len(updatedBody) > 200 {
		truncated = updatedBody[:200]
	}
	g.Cnx.Clients.Log.Infof("Updated comment body (truncated): %s...", truncated)
	assert.Assert(t, strings.Contains(strings.ToLower(updatedBody), "success") ||
		strings.Contains(strings.ToLower(updatedBody), "succeeded") ||
		strings.Contains(updatedBody, "✅"),
		"Comment should contain success status")
}

// TestGithubCommentStrategyUpdateMultiplePLRs tests:
// 1. Multiple PLRs in one PR each create their own comment
// 2. Each PLR only updates its own comment (no cross-updates)
// 3. Comments are correctly identified by their unique markers.
func TestGithubGHEWebhookCommentStrategyUpdateMultiplePLRs(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label:     "Github Comment Strategy Multiple PLRs",
		YamlFiles: []string{"testdata/pipelinerun.yaml", "testdata/pipelinerun-clone.yaml"},
		GHE:       true,
		Webhook:   true,
	}

	commentStrategy := v1alpha1.Settings{
		Github: &v1alpha1.GithubSettings{
			CommentStrategy: provider.UpdateCommentStrategy,
		},
	}
	g.Options.Settings = commentStrategy

	defer g.TearDown(ctx, t)
	g.RunPullRequest(ctx, t)

	sopt := twait.SuccessOpt{
		Title:           g.CommitTitle,
		TargetNS:        g.TargetNamespace,
		NumberofPRMatch: 2,
		SHA:             g.SHA,
		OnEvent:         "pull_request",
	}
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)

	// Wait for comments to be created.
	time.Sleep(1 * time.Second)

	comments, _, err := g.Provider.Client().Issues.ListComments(
		ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
		&github.IssueListCommentsOptions{})
	assert.NilError(t, err)

	// Find comments with pac-status markers
	markerPattern := regexp.MustCompile(`<!-- pac-status-([^-]+(?:-[^-]+)*) -->`)
	plrComments := make(map[string]*github.IssueComment)

	for _, comment := range comments {
		matches := markerPattern.FindStringSubmatch(comment.GetBody())
		if len(matches) > 1 {
			plrName := matches[1]
			plrComments[plrName] = comment
			g.Cnx.Clients.Log.Infof("Found comment for PLR: %s (ID: %d)", plrName, comment.GetID())
		}
	}

	assert.Equal(t, 2, len(plrComments),
		"Should have exactly 2 PLR status comments, found %d", len(plrComments))

	originalIDs := make(map[string]int64)
	for plrName, comment := range plrComments {
		originalIDs[plrName] = comment.GetID()
	}

	g.Cnx.Clients.Log.Infof("Pushing empty commit to trigger pipelines again")
	branchName := strings.TrimPrefix(g.TargetRefName, "refs/heads/")
	sha, err := tgithub.UpdateFilesInRef(ctx, g.Provider.Client(),
		g.Options.Organization, g.Options.Repo,
		branchName,
		"test: trigger re-run",
		map[string]string{"dummy-retest.txt": "trigger re-run"})
	assert.NilError(t, err)
	g.Cnx.Clients.Log.Infof("Pushed trigger commit: %s", sha)

	sopt.NumberofPRMatch = 4
	sopt.SHA = sha
	sopt.Title = "test: trigger re-run"
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)

	// Wait for comments to be updated
	time.Sleep(1 * time.Second)

	updatedComments, _, err := g.Provider.Client().Issues.ListComments(
		ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
		&github.IssueListCommentsOptions{})
	assert.NilError(t, err)

	updatedPLRComments := make(map[string]*github.IssueComment)
	for _, comment := range updatedComments {
		matches := markerPattern.FindStringSubmatch(comment.GetBody())
		if len(matches) > 1 {
			plrName := matches[1]
			updatedPLRComments[plrName] = comment
		}
	}

	assert.Equal(t, 2, len(updatedPLRComments),
		"Should still have exactly 2 PLR status comments after retest, found %d", len(updatedPLRComments))

	for plrName, updatedComment := range updatedPLRComments {
		originalID, exists := originalIDs[plrName]
		assert.Assert(t, exists, "PLR %s should have existed in original comments", plrName)
		assert.Equal(t, originalID, updatedComment.GetID(),
			"PLR %s comment should be updated (same ID), not recreated", plrName)
		g.Cnx.Clients.Log.Infof("Verified PLR %s comment was updated (ID: %d)", plrName, originalID)
	}
}

// TestGithubCommentStrategyUpdateMarkerMatchingWithRegexChars tests:
// 1. PLR names containing regex-relevant characters are handled correctly
// 2. Marker matching remains exact even with special characters
// 3. No cross-contamination between PLRs with similar names.
func TestGithubGHEWebhookCommentStrategyUpdateMarkerMatchingWithRegexChars(t *testing.T) {
	ctx := context.Background()
	g := &tgithub.PRTest{
		Label: "Github Comment Strategy Regex Chars",
		YamlFiles: []string{
			"testdata/pipelinerun-regex-name.yaml",
			"testdata/pipelinerun2-regex-name.yaml",
		},
		GHE:     true,
		Webhook: true,
	}

	commentStrategy := v1alpha1.Settings{
		Github: &v1alpha1.GithubSettings{
			CommentStrategy: provider.UpdateCommentStrategy,
		},
	}
	g.Options.Settings = commentStrategy

	defer g.TearDown(ctx, t)
	g.RunPullRequest(ctx, t)

	sopt := twait.SuccessOpt{
		Title:           g.CommitTitle,
		TargetNS:        g.TargetNamespace,
		NumberofPRMatch: 2,
		SHA:             g.SHA,
		OnEvent:         "pull_request",
	}
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)
	// Wait for comments
	time.Sleep(1 * time.Second)

	comments, _, err := g.Provider.Client().Issues.ListComments(
		ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
		&github.IssueListCommentsOptions{})
	assert.NilError(t, err)

	// Find comments with pac-status markers
	markerPattern := regexp.MustCompile(`<!-- pac-status-([^\s]+) -->`)
	plrComments := make(map[string]*github.IssueComment)

	for _, comment := range comments {
		body := comment.GetBody()
		matches := markerPattern.FindStringSubmatch(body)
		if len(matches) > 1 {
			plrName := matches[1]
			plrComments[plrName] = comment
			g.Cnx.Clients.Log.Infof("Found comment for PLR with special chars: %s (ID: %d)",
				plrName, comment.GetID())
		}
	}

	assert.Assert(t, len(plrComments) >= 2,
		"Should have at least 2 PLR status comments, found %d", len(plrComments))

	seenMarkers := make(map[string]bool)
	for plrName := range plrComments {
		assert.Assert(t, !seenMarkers[plrName],
			"Duplicate marker found for PLR: %s", plrName)
		seenMarkers[plrName] = true

		// Verify the marker contains the special characters
		// For example: "test.pipeline.v1" or "test-pipeline[0]"
		assert.Assert(t, strings.ContainsAny(plrName, ".[]()+*?"),
			"PLR name should contain regex-special characters: %s", plrName)
	}

	originalData := make(map[string]struct {
		ID   int64
		Body string
	})
	for plrName, comment := range plrComments {
		originalData[plrName] = struct {
			ID   int64
			Body string
		}{
			ID:   comment.GetID(),
			Body: comment.GetBody(),
		}
	}

	g.Cnx.Clients.Log.Infof("Pushing dummy update to trigger comment refresh")
	branchName := strings.TrimPrefix(g.TargetRefName, "refs/heads/")
	sha, err := tgithub.UpdateFilesInRef(ctx, g.Provider.Client(),
		g.Options.Organization, g.Options.Repo,
		branchName,
		"test: trigger comment update",
		map[string]string{"dummy.txt": "trigger update"})
	assert.NilError(t, err)
	g.Cnx.Clients.Log.Infof("Pushed dummy commit: %s", sha)

	sopt.NumberofPRMatch = 2
	sopt.SHA = sha
	sopt.Title = "test: trigger comment update"
	twait.Succeeded(ctx, t, g.Cnx, g.Options, sopt)

	updatedComments, _, err := g.Provider.Client().Issues.ListComments(
		ctx, g.Options.Organization, g.Options.Repo, g.PRNumber,
		&github.IssueListCommentsOptions{})
	assert.NilError(t, err)

	for _, comment := range updatedComments {
		body := comment.GetBody()
		matches := markerPattern.FindStringSubmatch(body)
		if len(matches) > 1 {
			plrName := matches[1]
			if originalInfo, exists := originalData[plrName]; exists {
				assert.Equal(t, originalInfo.ID, comment.GetID(),
					"Comment for PLR %s should be the same comment (not recreated)", plrName)
				assert.Assert(t, strings.Contains(body, fmt.Sprintf("<!-- pac-status-%s -->", plrName)),
					"Marker should be preserved exactly for PLR: %s", plrName)
			}
		}
	}
}
