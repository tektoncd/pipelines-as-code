package matcher

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gobwas/glob"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var ErrRepositoryNameConflict = errors.New("multiple repositories exist with the given name")

func MatchEventURLRepo(ctx context.Context, cs *params.Run, event *info.Event, ns string) (*apipac.Repository, error) {
	repositories, err := cs.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(ns).List(
		ctx, metav1.ListOptions{},
	)
	if err != nil {
		return nil, err
	}
	sort.RepositorySortByCreationOldestTime(repositories.Items)
	for _, repo := range repositories.Items {
		repo.Spec.URL = strings.TrimSuffix(repo.Spec.URL, "/")
		if repo.Spec.URL == event.URL {
			return &repo, nil
		}
	}

	return nil, nil
}

// GetRepoByName get a repo by name anywhere on a cluster.
// Parameter 'ns' may optionally be supplied in case of a naming conflict.
func GetRepoByName(ctx context.Context, cs *params.Run, repoName, ns string) (*apipac.Repository, error) {
	repositories, err := cs.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(ns).List(
		ctx, metav1.ListOptions{
			FieldSelector: "metadata.name==" + repoName,
		},
	)
	if err != nil {
		return nil, err
	}

	switch len(repositories.Items) {
	case 0:
		return nil, nil
	case 1:
		return &repositories.Items[0], nil
	default:
		return nil, ErrRepositoryNameConflict
	}
}

// IncomingWebhookRule will match a rule to an incoming rule, currently a rule is a target branch.
// Supports both exact string matching and glob patterns.
// Uses first-match-wins strategy: returns the first webhook with a matching target.
func IncomingWebhookRule(branch string, incomingWebhooks []apipac.Incoming) *apipac.Incoming {
	// TODO: one day we will match the hook.Type here when we get something else than the dumb one (ie: slack)
	for i := range incomingWebhooks {
		hook := &incomingWebhooks[i]

		// Check each target in this webhook
		for _, target := range hook.Targets {
			matched, err := matchTarget(branch, target)
			if err != nil {
				// Skip invalid glob patterns and continue to next target
				continue
			}

			if matched {
				// First match wins - return immediately
				return hook
			}
		}
	}
	return nil
}

// matchTarget checks if a branch matches a target pattern using glob matching.
// Supports both exact string matching and glob patterns.
func matchTarget(branch, target string) (bool, error) {
	g, err := glob.Compile(target)
	if err != nil {
		return false, fmt.Errorf("invalid glob pattern %q: %w", target, err)
	}

	return g.Match(branch), nil
}
