package pipelinerungroup

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

// Definition is a .tekton/ definition as seen by the dependency graph.
type Definition struct {
	// Name is the definition name, i.e. the OriginalPRName annotation value.
	Name string
	// DependsOn is the parsed on-pipelinerun annotation, empty for plain runs.
	DependsOn []string
	// CEL is the on-cel-expression annotation, empty when absent.
	CEL string
}

// ProblemKind classifies why a follow-up cannot run.
type ProblemKind string

const (
	ProblemSelfReference        ProblemKind = "self-reference"
	ProblemChainedDependency    ProblemKind = "chained-dependency"
	ProblemMissingDependency    ProblemKind = "missing-dependency"
	ProblemNoCEL                ProblemKind = "missing-cel-expression"
	ProblemDependencyNotStarted ProblemKind = "dependency-not-started"
	ProblemDuplicateName        ProblemKind = "duplicate-name"
	ProblemCaseSensitiveName    ProblemKind = "case-sensitive-name"
)

// Problem is a single validation finding for a follow-up definition.
type Problem struct {
	// Name is the follow-up definition name.
	Name string
	Kind ProblemKind
	Msg  string
}

// ParseDependsOn parses the on-pipelinerun annotation: a JSON array of
// definition names. An empty annotation returns nil.
func ParseDependsOn(annotation string) ([]string, error) {
	if strings.TrimSpace(annotation) == "" {
		return nil, nil
	}
	var deps []string
	if err := json.Unmarshal([]byte(annotation), &deps); err != nil {
		return nil, fmt.Errorf("invalid %s annotation, expected a JSON array of definition names: %w", keys.OnPipelineRun, err)
	}
	deduped := make([]string, 0, len(deps))
	seen := map[string]bool{}
	for _, dep := range deps {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			return nil, fmt.Errorf("invalid %s annotation, it contains an empty definition name", keys.OnPipelineRun)
		}
		if !seen[dep] {
			seen[dep] = true
			deduped = append(deduped, dep)
		}
	}
	return deduped, nil
}

// DefinitionName returns the OriginalPRName annotation, falling back to the
// name or generateName, kept as is like the metadata resolution does.
func DefinitionName(pr *tektonv1.PipelineRun) string {
	if name, ok := pr.GetAnnotations()[keys.OriginalPRName]; ok && name != "" {
		return name
	}
	if name := pr.GetName(); name != "" {
		return name
	}
	return pr.GetGenerateName()
}

// DefinitionError pairs a definition name with the problem found in its
// on-pipelinerun annotation.
type DefinitionError struct {
	Name string
	Err  error
}

// DefinitionsFromPRs builds the graph definitions. Definitions with an invalid
// or empty on-pipelinerun annotation are reported as errors and left out.
func DefinitionsFromPRs(prs []*tektonv1.PipelineRun) ([]Definition, []DefinitionError) {
	defs := make([]Definition, 0, len(prs))
	var errs []DefinitionError
	for _, pr := range prs {
		annotation, hasAnnotation := pr.GetAnnotations()[keys.OnPipelineRun]
		deps, err := ParseDependsOn(annotation)
		switch {
		case err != nil:
			errs = append(errs, DefinitionError{Name: DefinitionName(pr), Err: err})
		case hasAnnotation && len(deps) == 0:
			// it would wait on nothing and stay pending forever
			errs = append(errs, DefinitionError{
				Name: DefinitionName(pr),
				Err:  fmt.Errorf("the %s annotation must list at least one definition", keys.OnPipelineRun),
			})
		default:
			defs = append(defs, Definition{
				Name:      DefinitionName(pr),
				DependsOn: deps,
				CEL:       pr.GetAnnotations()[keys.OnCelExpression],
			})
		}
	}
	return defs, errs
}

// Plan is the outcome of planning the follow-ups of one event.
type Plan struct {
	// Problems are the validation findings, all fatal: a graph with problems
	// is not planned at all.
	Problems []Problem
	// Create holds the follow-ups to plan.
	Create []string
	// Skip holds the follow-ups with only some of their dependencies running.
	// A follow-up none of whose dependencies runs is not part of the plan: it
	// does not match the event.
	Skip []Problem
}

// ValidateGraph reports the configuration errors of the definitions. A
// follow-up may only wait on plain definitions, never on another follow-up.
func ValidateGraph(defs []Definition) []Problem {
	var problems []Problem
	add := func(name string, kind ProblemKind, format string, args ...any) {
		problems = append(problems, Problem{Name: name, Kind: kind, Msg: fmt.Sprintf(format, args...)})
	}

	byName := map[string]Definition{}
	for _, def := range defs {
		if _, ok := byName[def.Name]; ok {
			add(def.Name, ProblemDuplicateName, "definition %q is defined more than once in the .tekton directory", def.Name)
		}
		byName[def.Name] = def
	}
	for _, def := range defs {
		if len(def.DependsOn) == 0 {
			continue
		}
		if def.Name != strings.ToLower(def.Name) {
			add(def.Name, ProblemCaseSensitiveName, "follow-up %q must be lowercase: its run name is lowercased, so names differing only by case would collide", def.Name)
		}
		for _, dep := range def.DependsOn {
			depDef, defined := byName[dep]
			switch {
			case dep == def.Name:
				add(def.Name, ProblemSelfReference, "follow-up %q waits on itself", def.Name)
			case !defined:
				add(def.Name, ProblemMissingDependency, "follow-up %q waits on %q which is not defined in the .tekton directory", def.Name, dep)
			case len(depDef.DependsOn) > 0:
				add(def.Name, ProblemChainedDependency, "follow-up %q waits on %q which is itself a follow-up: a follow-up can only wait on plain definitions", def.Name, dep)
			}
		}
		if def.CEL == "" {
			add(def.Name, ProblemNoCEL, "follow-up %q must have an %s annotation", def.Name, keys.OnCelExpression)
		}
	}
	return problems
}

// PlanFollowUps validates the definitions and works out what to do with each
// follow-up. started holds the plain definitions matched for the event.
func PlanFollowUps(defs []Definition, started map[string]bool) Plan {
	plan := Plan{Problems: ValidateGraph(defs)}
	if len(plan.Problems) > 0 {
		return plan
	}

	for _, def := range defs {
		if len(def.DependsOn) == 0 {
			continue
		}
		var notRunning []string
		for _, dep := range def.DependsOn {
			if !started[dep] {
				notRunning = append(notRunning, dep)
			}
		}
		switch {
		case len(notRunning) == 0:
			plan.Create = append(plan.Create, def.Name)
		case len(notRunning) < len(def.DependsOn):
			sort.Strings(notRunning)
			plan.Skip = append(plan.Skip, Problem{
				Name: def.Name, Kind: ProblemDependencyNotStarted,
				Msg: fmt.Sprintf("dependency %s did not run for this event", strings.Join(notRunning, ", ")),
			})
		}
	}
	sort.Strings(plan.Create)
	sort.Slice(plan.Skip, func(i, j int) bool { return plan.Skip[i].Name < plan.Skip[j].Name })
	return plan
}
