package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/templates"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	defaultNsTemplate   = "%v-pipelines"
	defaultRepoTemplate = "%v-repo-cr"
)

func ConfigureRepository(ctx context.Context, run *params.Run, req *http.Request, payload string, pacInfo *info.PacOpts, logger *zap.SugaredLogger) (bool, bool, error) {
	// check if repo auto configuration is enabled
	if !pacInfo.AutoConfigureNewGitHubRepo {
		return false, false, nil
	}
	// gitea set x-github-event too, so skip it for the gitea driver
	if h := req.Header.Get("X-Gitea-Event-Type"); h != "" {
		return false, false, nil
	}
	event := req.Header.Get("X-Github-Event")
	if event != "repository" {
		return false, false, nil
	}

	eventInt, err := github.ParseWebHook(event, []byte(payload))
	if err != nil {
		return true, false, err
	}
	_ = json.Unmarshal([]byte(payload), &eventInt)
	repoEvent, _ := eventInt.(*github.RepositoryEvent)

	if repoEvent.GetAction() != "created" {
		logger.Infof("github: repository event \"%v\" is not supported", repoEvent.GetAction())
		return true, false, nil
	}

	logger.Infof("github: configuring repository cr for repo: %v", repoEvent.Repo.GetHTMLURL())
	nsTemplate := pacInfo.AutoConfigureRepoNamespaceTemplate
	repoTemplate := pacInfo.AutoConfigureRepoRepositoryTemplate
	if err := createRepository(ctx, nsTemplate, repoTemplate, run.Clients, repoEvent, logger); err != nil {
		logger.Errorf("failed repository creation: %v", err)
		return true, true, err
	}

	return true, true, nil
}

func createRepository(ctx context.Context, nsTemplate, repoTemplate string, clients clients.Clients, gitEvent *github.RepositoryEvent, logger *zap.SugaredLogger) error {
	repoNsName, repoCRName, err := generateNamespaceAndRepositoryName(nsTemplate, repoTemplate, gitEvent)
	if err != nil {
		return fmt.Errorf("failed to generate namespace for repo: %w", err)
	}

	logger.Info("github: generated namespace name: ", repoNsName)

	// create namespace
	repoNs := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: repoNsName,
		},
	}
	repoNs, err = clients.Kube.CoreV1().Namespaces().Create(ctx, repoNs, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create namespace %v: %w", repoNs.Name, err)
	}

	if errors.IsAlreadyExists(err) {
		logger.Infof("github: namespace %v already exists, creating repository", repoNsName)
	} else {
		logger.Info("github: created repository namespace: ", repoNs.Name)
	}

	// create repository
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      repoCRName,
			Namespace: repoNsName,
		},
		Spec: v1alpha1.RepositorySpec{
			URL: gitEvent.Repo.GetHTMLURL(),
		},
	}
	repo, err = clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(repoNsName).Create(ctx, repo, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create repository for repo: %v: %w", gitEvent.Repo.GetHTMLURL(), err)
	}
	logger = logger.With("namespace", repo.Namespace)
	logger.Infof("github: repository created: %s/%s ", repo.Namespace, repo.Name)
	return nil
}

func generateNamespaceAndRepositoryName(nsTemplate, repoTemplate string, gitEvent *github.RepositoryEvent) (string, string, error) {
	repoOwner, repoName, err := formatting.GetRepoOwnerSplitted(gitEvent.Repo.GetHTMLURL())
	if err != nil {
		return "", "", fmt.Errorf("failed to parse git repo url: %w", err)
	}

	nsName := ""
	repoCRName := ""
	placeholders := map[string]string{
		"repo_owner": repoOwner,
		"repo_name":  repoName,
	}

	if nsTemplate == "" {
		nsName = fmt.Sprintf(defaultNsTemplate, repoName)
	} else {
		nsName = templates.ReplacePlaceHoldersVariables(nsTemplate, placeholders, nil, http.Header{}, map[string]any{})
	}

	if repoTemplate == "" {
		repoCRName = fmt.Sprintf(defaultRepoTemplate, repoName)
	} else {
		repoCRName = templates.ReplacePlaceHoldersVariables(repoTemplate, placeholders, nil, http.Header{}, map[string]any{})
	}
	return nsName, repoCRName, nil
}
