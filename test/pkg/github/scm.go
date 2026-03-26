package github

import (
	"context"
	"fmt"

	"github.com/google/go-github/v84/github"
	"go.uber.org/zap"
)

// CreateGHERepo creates a new GitHub repository inside an organization and adds
// a webhook pointing to the given hookURL (for example, a smee channel URL
// used to forward events to the controller). The repository is initialized with
// a README on the "main" branch.
func CreateGHERepo(ctx context.Context, client *github.Client, org, name, hookURL, webhookSecret string, logger *zap.SugaredLogger) (*github.Repository, error) {
	logger.Infof("Creating GitHub repository %s/%s", org, name)

	autoInit := true
	repo, _, err := client.Repositories.Create(ctx, org, &github.Repository{
		Name:     github.Ptr(name),
		AutoInit: &autoInit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create repository %s/%s: %w", org, name, err)
	}
	logger.Infof("Created GitHub repository %s", repo.GetFullName())

	logger.Infof("Adding webhook to repository %s pointing to %s", repo.GetFullName(), hookURL)

	active := true
	_, _, err = client.Repositories.CreateHook(ctx, org, name, &github.Hook{
		Events: []string{"push", "pull_request", "issue_comment", "check_run", "check_suite"},
		Config: &github.HookConfig{
			URL:         github.Ptr(hookURL),
			ContentType: github.Ptr("json"),
			Secret:      github.Ptr(webhookSecret),
			InsecureSSL: github.Ptr("1"),
		},
		Active: &active,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add webhook to repository %s: %w", repo.GetFullName(), err)
	}

	return repo, nil
}

func DeleteGHERepo(ctx context.Context, client *github.Client, org, name string, logger *zap.SugaredLogger) error {
	logger.Infof("Deleting GitHub repository %s/%s", org, name)
	_, err := client.Repositories.Delete(ctx, org, name)
	if err != nil {
		return fmt.Errorf("failed to delete repository %s/%s: %w", org, name, err)
	}
	logger.Infof("Deleted GitHub repository %s/%s", org, name)
	return nil
}
