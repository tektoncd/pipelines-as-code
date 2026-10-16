package acl

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"sigs.k8s.io/yaml"
)

type aliases = map[string][]string

type simpleConfig struct {
	Approvers []string `json:"approvers,omitempty"`
	Reviewers []string `json:"reviewers,omitempty"`
}

type filtersConfig struct {
	Filters map[string]simpleConfig `json:"filters,omitempty"`
}

type aliasesConfig struct {
	Aliases aliases `json:"aliases,omitempty"`
}

// UserInOwnerFile Parse OWNERS and OWNERS_ALIASES files and return true if the sender is in
// there. Support OWNERS simple configs (approvers, reviewers) and filters. When filters are used,
// only match against the ".*" filter.
func UserInOwnerFile(ownersContent, ownersAliasesContent, sender string) (bool, error) {
	sc := simpleConfig{}
	fc := filtersConfig{}
	ac := aliasesConfig{}
	err := yaml.Unmarshal([]byte(ownersContent), &sc)
	if err != nil {
		return false, fmt.Errorf("cannot parse OWNERS file Approvers and Reviewers: %w", err)
	}
	err = yaml.Unmarshal([]byte(ownersContent), &fc)
	if err != nil {
		return false, fmt.Errorf("cannot parse OWNERS file Filters: %w", err)
	}
	err = yaml.Unmarshal([]byte(ownersAliasesContent), &ac)
	if err != nil {
		return false, fmt.Errorf("cannot parse OWNERS_ALIASES: %w", err)
	}

	var approvers, reviewers []string
	if len(sc.Approvers) > 0 || len(sc.Reviewers) > 0 {
		approvers, reviewers = sc.Approvers, sc.Reviewers
		// Simple config (approvers/reviewers) and filters can't exist together.
		// We only check for the ".*" filter (matching all files in the repo).
	} else if filter, ok := fc.Filters[".*"]; ok {
		if len(filter.Approvers) > 0 || len(filter.Reviewers) > 0 {
			approvers, reviewers = filter.Approvers, filter.Reviewers
		}
	}
	owners := expandAliases(append(approvers, reviewers...), ac.Aliases)
	for _, owner := range owners {
		// entries may carry trailing annotations, e.g. "alice # team lead"
		if strings.HasPrefix(strings.TrimSpace(owner), sender) {
			return true, nil
		}
	}
	return false, nil
}

// Expand aliases into the list of owners removing the duplicates.
// Due to the use of map for deduplication, the order is not guaranteed.
func expandAliases(owners []string, aliases aliases) []string {
	dedups := make(map[string]bool)
	for _, owner := range owners {
		if _, ok := dedups[owner]; !ok {
			// check if owner is an alias
			if alias, ok := aliases[owner]; ok {
				for _, name := range alias {
					dedups[name] = true
				}
			} else {
				dedups[owner] = true
			}
		}
	}
	return slices.Collect(maps.Keys(dedups))
}
