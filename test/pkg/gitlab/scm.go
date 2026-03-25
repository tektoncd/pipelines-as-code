package gitlab

import (
	"fmt"
	"net/http"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	"go.uber.org/zap"
)

// CreateGitLabProject creates a new GitLab project inside a group and adds
// a webhook pointing to the given hookURL (for example, a smee channel URL
// used to forward events to the controller). The project is initialised with
// a README on the "main" branch.
func CreateGitLabProject(client *gitlab.Client, groupPath, projectName, hookURL, webhookSecret string, logger *zap.SugaredLogger) (*gitlab.Project, error) {
	logger.Infof("Looking up GitLab group %s", groupPath)
	group, _, err := client.Groups.GetGroup(groupPath, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to look up group %s: %w", groupPath, err)
	}

	logger.Infof("Creating GitLab project %s in group %s (ID %d)", projectName, groupPath, group.ID)
	project, _, err := client.Projects.CreateProject(&gitlab.CreateProjectOptions{
		Name:                 gitlab.Ptr(projectName),
		NamespaceID:          gitlab.Ptr(group.ID),
		InitializeWithReadme: gitlab.Ptr(true),
		DefaultBranch:        gitlab.Ptr("main"),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create project %s: %w", projectName, err)
	}
	logger.Infof("Created GitLab project %s (ID %d)", project.PathWithNamespace, project.ID)

	logger.Infof("Adding webhook to project %s pointing to %s", project.PathWithNamespace, hookURL)
	_, _, err = client.Projects.AddProjectHook(project.ID, &gitlab.AddProjectHookOptions{
		URL:                   gitlab.Ptr(hookURL),
		Token:                 gitlab.Ptr(webhookSecret),
		MergeRequestsEvents:   gitlab.Ptr(true),
		NoteEvents:            gitlab.Ptr(true),
		PushEvents:            gitlab.Ptr(true),
		TagPushEvents:         gitlab.Ptr(true),
		EnableSSLVerification: gitlab.Ptr(false),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add webhook to project %s: %w", project.PathWithNamespace, err)
	}

	return project, nil
}

func ForkGitLabProject(client *gitlab.Client, projectID int, namespacePath string, logger *zap.SugaredLogger) (*gitlab.Project, error) {
	options := &gitlab.ForkProjectOptions{}
	if namespacePath != "" {
		options.NamespacePath = gitlab.Ptr(namespacePath)
	}

	currentUser, _, err := client.Users.CurrentUser()
	if err != nil {
		logger.Warnf("could not fetch current GitLab user: %v", err)
		logger.Infof("Forking GitLab project %d into namespace %q", projectID, namespacePath)
	} else {
		logger.Infof("Forking GitLab project %d as user %s (ID %d) into namespace %q",
			projectID, currentUser.Username, currentUser.ID, namespacePath)
	}
	project, resp, err := client.Projects.ForkProject(projectID, options)
	if err != nil && namespacePath != "" && resp != nil && resp.StatusCode == http.StatusNotFound {
		logger.Warnf("Fork into namespace %q failed (404) — second user may not be a member of that group; retrying into personal namespace", namespacePath)
		project, _, err = client.Projects.ForkProject(projectID, &gitlab.ForkProjectOptions{})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to fork project %d: %w", projectID, err)
	}

	logger.Infof("Forked GitLab project into %s (ID %d)", project.PathWithNamespace, project.ID)

	return project, nil
}

func AddGitLabProjectMember(client *gitlab.Client, projectID int, userID int64, accessLevel gitlab.AccessLevelValue, logger *zap.SugaredLogger) error {
	logger.Infof("Granting user %d access level %d on GitLab project %d", userID, accessLevel, projectID)
	_, _, err := client.ProjectMembers.AddProjectMember(projectID, &gitlab.AddProjectMemberOptions{
		UserID:      userID,
		AccessLevel: gitlab.Ptr(accessLevel),
	})
	if err != nil {
		return fmt.Errorf("failed to add user %d to project %d: %w", userID, projectID, err)
	}

	return nil
}
