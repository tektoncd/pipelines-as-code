package github

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/gobwas/glob"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ScopeTokenToListOfRepos(ctx context.Context, vcx provider.Interface, pacInfo *info.PacOpts, repo *v1alpha1.Repository, run *params.Run,
	event *info.Event, eventEmitter *events.EventEmitter, logger *zap.SugaredLogger,
) (string, error) {
	var (
		listRepos bool
		token     string
	)
	listURLs := []string{}
	repoListToScopeToken := []string{}

	// This is a Global config to provide list of repos to scope token
	if pacInfo.SecretGhAppTokenScopedExtraRepos != "" {
		for _, configValue := range strings.Split(pacInfo.SecretGhAppTokenScopedExtraRepos, ",") {
			configValueS := strings.TrimSpace(configValue)
			if configValueS == "" {
				continue
			}
			// May or may not be a glob
			if _, err := glob.Compile(configValueS); err != nil {
				msg := fmt.Sprintf("invalid repo glob specified %s", configValueS)
				eventEmitter.EmitMessage(nil, zap.ErrorLevel, "InvalidRepoGlobSpecified", msg)

				return "", errors.New(msg)
			}

			repoListToScopeToken = append(repoListToScopeToken, configValueS)
		}
		listRepos = true
		logger.Infof("configured Global configuration to %v to scope Github token ", repoListToScopeToken)
	}
	if repo.Spec.Settings != nil && len(repo.Spec.Settings.GithubAppTokenScopeRepos) != 0 {
		ns := repo.Namespace
		repoListInPerticularNamespace, err := run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", err
		}
		for i := range repoListInPerticularNamespace.Items {
			splitData, err := getURLPathData(repoListInPerticularNamespace.Items[i].Spec.URL)
			if err != nil {
				return "", err
			}
			listURLs = append(listURLs, splitData[1]+"/"+splitData[2])
		}
		for i := range repo.Spec.Settings.GithubAppTokenScopeRepos {
			// May or may not be a glob
			repoToScope, err := glob.Compile(repo.Spec.Settings.GithubAppTokenScopeRepos[i])
			if err != nil {
				msg := fmt.Sprintf("invalid repo glob specified %s", repo.Spec.Settings.GithubAppTokenScopeRepos[i])
				eventEmitter.EmitMessage(nil, zap.ErrorLevel, "InvalidRepoGlobSpecified", msg)

				return "", errors.New(msg)
			}
			globMatchFound := false
			// Match glob to repos list.
			for _, repoKey := range listURLs {
				if repoToScope.Match(repoKey) {
					repoListToScopeToken = append(repoListToScopeToken, repoKey)
					globMatchFound = true
				}
			}
			if !globMatchFound {
				msg := fmt.Sprintf("failed to scope GitHub token as repo with pattern %s does not exist in namespace %s", repo.Spec.Settings.GithubAppTokenScopeRepos[i], ns)
				eventEmitter.EmitMessage(nil, zap.ErrorLevel, "RepoDoesNotExistInNamespace", msg)
				return "", errors.New(msg)
			}
		}
		// When the global configuration is not set then check for secret-github-app-token-scoped key for the repo level configuration
		if pacInfo.SecretGHAppRepoScoped && pacInfo.SecretGhAppTokenScopedExtraRepos == "" {
			msg := fmt.Sprintf(`failed to scope GitHub token as repo scoped key %s is enabled. Hint: update key %s from pipelines-as-code configmap to false`,
				settings.SecretGhAppTokenRepoScopedKey, settings.SecretGhAppTokenRepoScopedKey)
			eventEmitter.EmitMessage(nil, zap.ErrorLevel, "SecretGHAppTokenRepoScopeIsEnabled", msg)
			return "", errors.New(msg)
		}
		listRepos = true
		logger.Infof("configured repo level configuration to %v to scope Github token ", repo.Spec.Settings.GithubAppTokenScopeRepos)
	}
	if listRepos {
		repoInfoFromWhichEventCame, err := getURLPathData(repo.Spec.URL)
		if err != nil {
			return "", err
		}
		// adding the repo info from which event came so that repositoryID will be added while scoping the token
		repoListToScopeToken = append(repoListToScopeToken, repoInfoFromWhichEventCame[1]+"/"+repoInfoFromWhichEventCame[2])
		token, err = vcx.CreateToken(ctx, repoListToScopeToken, event)
		if err != nil {
			return "", fmt.Errorf("failed to scope token to repositories with error : %w", err)
		}
		logger.Infof("Github token scope extended to %v ", repoListToScopeToken)
	}
	return token, nil
}

func getURLPathData(urlInfo string) ([]string, error) {
	urlData, err := url.ParseRequestURI(urlInfo)
	if err != nil {
		return []string{}, err
	}

	split := strings.Split(urlData.Path, "/")
	if len(split) != 3 {
		return []string{}, fmt.Errorf("github repository URL must follow https://github.com/org/repo format without subgroups (found %d path segments, expected 2): %s", len(split)-1, urlInfo)
	}

	return split, nil
}
