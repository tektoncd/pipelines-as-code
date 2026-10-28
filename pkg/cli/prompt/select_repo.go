package prompt

import (
	"context"
	"fmt"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/formatting"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func SelectRepo(ctx context.Context, cs *params.Run, namespace string) (*v1alpha1.Repository, error) {
	repositories, err := cs.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	if len(repositories.Items) == 0 {
		return nil, fmt.Errorf("no repo found")
	}
	if len(repositories.Items) == 1 {
		return &repositories.Items[0], nil
	}

	allRepositories := []string{}
	for _, repository := range repositories.Items {
		repoOwner, err := formatting.GetRepoOwnerFromURL(repository.Spec.URL)
		if err != nil {
			return nil, err
		}
		allRepositories = append(allRepositories,
			fmt.Sprintf("%s - %s",
				repository.GetName(),
				repoOwner))
	}

	var replyString string
	if err := SurveyAskOne(&survey.Select{
		Message: "Select a repository",
		Options: allRepositories,
	}, &replyString); err != nil {
		return nil, err
	}

	if replyString == "" {
		return nil, fmt.Errorf("you need to choose a repository")
	}
	replyName := strings.Fields(replyString)[0]

	for _, repository := range repositories.Items {
		if repository.GetName() == replyName {
			return &repository, nil
		}
	}

	return nil, fmt.Errorf("cannot match repository")
}
