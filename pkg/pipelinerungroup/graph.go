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
	ProblemCycle                ProblemKind = "cycle"
	ProblemMissingDependency    ProblemKind = "missing-dependency"
	ProblemNoCEL                ProblemKind = "missing-cel-expression"
	ProblemDependencyNotStarted ProblemKind = "dependency-not-started"
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

// DefinitionName returns the definition name of a PipelineRun template or
// created run, i.e. its OriginalPRName annotation falling back to the name or
// generateName.
func DefinitionName(pr *tektonv1.PipelineRun) string {
	if name, ok := pr.GetAnnotations()[keys.OriginalPRName]; ok && name != "" {
		return name
	}
	if name := pr.GetName(); name != "" {
		return name
	}
	return strings.TrimSuffix(pr.GetGenerateName(), "-")
}

// DefinitionError pairs a definition name with the problem found in its
// on-pipelinerun annotation.
type DefinitionError struct {
	Name string
	Err  error
}

// DefinitionsFromPRs builds the graph definitions from every definition of the
// event's .tekton directory. A definition whose on-pipelinerun annotation is
// invalid, or present but listing no dependency, is reported in the errors and
// left out of the definitions.
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
			// An on-pipelinerun that lists nothing would make the definition
			// wait on no run and stay pending forever: reject it up front.
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
	// Problems are the validation findings. Fatal ones must fail the event.
	Problems []Problem
	// Create holds the definition names of the follow-ups to plan, chained
	// follow-ups included.
	Create []string
	// Skip holds the follow-ups that must not run because one of their
	// dependencies is not run for this event, propagated transitively.
	Skip []Problem
}

// PlanFollowUps validates the dependency graph and works out which follow-ups
// to plan. started is the set of definition names matched for this event,
// follow-ups included.
func PlanFollowUps(defs []Definition, started map[string]bool) Plan {
	plan := Plan{}
	all := map[string]bool{}
	isFollowUp := map[string]bool{}
	for _, def := range defs {
		all[def.Name] = true
		if len(def.DependsOn) > 0 {
			isFollowUp[def.Name] = true
		}
	}

	for _, def := range defs {
		if !isFollowUp[def.Name] {
			continue
		}
		for _, dep := range def.DependsOn {
			switch {
			case dep == def.Name:
				plan.Problems = append(plan.Problems, Problem{
					Name: def.Name, Kind: ProblemSelfReference,
					Msg: fmt.Sprintf("follow-up %q waits on itself", def.Name),
				})
			case !all[dep]:
				plan.Problems = append(plan.Problems, Problem{
					Name: def.Name, Kind: ProblemMissingDependency,
					Msg: fmt.Sprintf("follow-up %q waits on %q which is not defined in the .tekton directory", def.Name, dep),
				})
			}
		}
		if def.CEL == "" {
			plan.Problems = append(plan.Problems, Problem{
				Name: def.Name, Kind: ProblemNoCEL,
				Msg: fmt.Sprintf("follow-up %q must have an %s annotation", def.Name, keys.OnCelExpression),
			})
		}
	}
	plan.Problems = append(plan.Problems, detectCycles(defs)...)
	if len(plan.Problems) > 0 {
		// A graph with fatal problems is not scheduled at all: creating some
		// follow-ups of a broken graph would leave chains half-started.
		return plan
	}

	// Fixpoint: a follow-up is planned when every dependency is started for
	// this event or is itself a planned follow-up. Anything else is skipped,
	// and a skip propagates up the chain.
	created := map[string]bool{}
	changed := true
	for changed {
		changed = false
	nextDef:
		for _, def := range defs {
			if !isFollowUp[def.Name] || created[def.Name] {
				continue
			}
			for _, dep := range def.DependsOn {
				if !started[dep] && !created[dep] {
					continue nextDef
				}
			}
			created[def.Name] = true
			changed = true
		}
	}

	for _, def := range defs {
		if !isFollowUp[def.Name] || created[def.Name] {
			continue
		}
		missing := []string{}
		for _, dep := range def.DependsOn {
			if !started[dep] {
				missing = append(missing, dep)
			}
		}
		sort.Strings(missing)
		plan.Skip = append(plan.Skip, Problem{
			Name: def.Name, Kind: ProblemDependencyNotStarted,
			Msg: fmt.Sprintf("dependency %s did not run for this event", strings.Join(missing, ", ")),
		})
	}
	for name := range created {
		plan.Create = append(plan.Create, name)
	}
	sort.Strings(plan.Create)
	return plan
}

// detectCycles rejects definitions on a dependency loop, searched over all
// definitions since a dormant loop is still a configuration error. Only nodes
// actually on a cycle are reported.
func detectCycles(defs []Definition) []Problem {
	byName := map[string]Definition{}
	for _, def := range defs {
		byName[def.Name] = def
	}

	const (
		unvisited = iota
		onStack
		done
	)
	state := map[string]int{}
	var stack []string
	cyclic := map[string]bool{}
	var visit func(name string)
	visit = func(name string) {
		switch state[name] {
		case onStack:
			// Closed a cycle: every node from this one up the stack is on it.
			for i := len(stack) - 1; i >= 0; i-- {
				cyclic[stack[i]] = true
				if stack[i] == name {
					break
				}
			}
			return
		case done:
			return
		}
		state[name] = onStack
		stack = append(stack, name)
		for _, dep := range byName[name].DependsOn {
			if dep == name {
				// a self-loop is reported as a self-reference problem
				continue
			}
			if _, ok := byName[dep]; ok {
				visit(dep)
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = done
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		visit(name)
	}

	var problems []Problem
	for _, name := range names {
		if cyclic[name] {
			problems = append(problems, Problem{
				Name: name, Kind: ProblemCycle,
				Msg: fmt.Sprintf("follow-up %q is part of a dependency loop", name),
			})
		}
	}
	return problems
}
