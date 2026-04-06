package gitlab

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	clientGitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/zap"
)

func WaitForGitLabCommitStatusCount(ctx context.Context, client *clientGitlab.Client, logger *zap.SugaredLogger, projectID int, sha, targetStatus string, minCount int) (int, error) {
	//nolint:misspell
	if targetStatus != "" && targetStatus != "success" && targetStatus != "failed" && targetStatus != "pending" && targetStatus != "running" && targetStatus != "canceled" {
		return 0, fmt.Errorf("invalid target status: %s", targetStatus)
	}

	ctx, cancel := context.WithTimeout(ctx, twait.DefaultTimeout)
	defer cancel()

	var count int
	err := kubeinteraction.PollImmediateWithContext(ctx, twait.DefaultTimeout, func() (bool, error) {
		statuses, _, err := client.Commits.GetCommitStatuses(projectID, sha, &clientGitlab.GetCommitStatusesOptions{All: clientGitlab.Ptr(true)})
		if err != nil {
			return false, err
		}
		logger.Infof("Current GitLab commit status count: %d (waiting for at least %d)", len(statuses), minCount)
		currentCount := 0
		for _, status := range statuses {
			if targetStatus == "" || status.Status == targetStatus {
				currentCount++
			}

			if currentCount >= minCount {
				count = currentCount
				return true, nil
			}
		}
		return false, nil
	})
	return count, err
}
