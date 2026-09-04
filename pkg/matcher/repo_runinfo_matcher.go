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
	repoItems, err := cs.ListRepositories(ctx, ns)
	if err != nil {
		return nil, err
	}

	sort.RepositorySortByCreationOldestTime(repoItems)
	eventURL := strings.TrimSuffix(event.URL, "/")

	for _, repo := range repoItems {
		repoURL := strings.TrimSuffix(repo.Spec.URL, "/")
		if repoURL == eventURL {
			return &repo, nil
		}
	}

	return nil, nil
}

// GetRepoByName get a repo by name anywhere on a cluster.
// Parameter 'ns' may optionally be supplied in case of a naming conflict.
func GetRepoByName(ctx context.Context, cs *params.Run, repoName, ns string) (*apipac.Repository, error) {
	// No namespace: the direct API path filters by name server-side, which the
	// lister cannot do, so keep it as a dedicated branch.
	if cs.RepositoryLister == nil {
		repositories, err := cs.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(ns).List(
			ctx, metav1.ListOptions{
				FieldSelector: "metadata.name==" + repoName,
			},
		)
		if err != nil {
			return nil, err
		}
		return repoByUniqueName(repositories.Items)
	}

	// Use the lister with the provided namespace (empty means all namespaces).
	allRepos, err := cs.ListRepositories(ctx, ns)
	if err != nil {
		return nil, err
	}
	var matching []apipac.Repository
	for _, repo := range allRepos {
		if repo.Name == repoName {
			matching = append(matching, repo)
		}
	}
	return repoByUniqueName(matching)
}

// repoByUniqueName returns the sole repository in repos, nil when there are
// none, or ErrRepositoryNameConflict when more than one share the name.
func repoByUniqueName(repos []apipac.Repository) (*apipac.Repository, error) {
	switch len(repos) {
	case 0:
		return nil, nil
	case 1:
		return &repos[0], nil
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
