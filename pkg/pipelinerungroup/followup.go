package pipelinerungroup

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/apis/validate"
	"k8s.io/apimachinery/pkg/util/validation"
)

// The states of a follow-up pipelinerun.
const (
	StatePending   = "pending"
	StateCreated   = "created"
	StateSkipped   = "skipped"
	StateCancelled = "cancelled"
	// StateFailed is a follow-up whose run could not be created.
	StateFailed = "failed"
)

// FollowUp is the stored intent of a follow-up between the event that
// planned it and the creation of its run.
type FollowUp struct {
	// ID is the deterministic name of the run created for the follow-up. It
	// doubles as the identity of the follow-up across the runs that carry it.
	ID string `json:"id"`
	// Name is the definition name of the follow-up.
	Name string `json:"name"`
	// Deps are the definition names the follow-up waits on.
	Deps []string `json:"deps,omitempty"`
	// State is pending or one of the terminal follow-up states.
	State string `json:"state,omitempty"`
	// Event is the event as the matcher saw it, for the follow-up's CEL
	// expression to see the same variables. Absent on follow-ups stored by
	// an older version, which then see the event rebuilt from annotations.
	Event *FollowUpEvent `json:"event,omitempty"`
}

// FollowUpEvent holds the event fields the on-cel-expression variables and
// the template variables of a follow-up are computed from, as they were when
// the event was templated: the annotations of a run are lossy (a GitHub push
// of a merge commit carries the pull request number, the pull request title
// and the comment are not stored at all).
type FollowUpEvent struct {
	TriggerTarget    string   `json:"trigger_target,omitempty"`
	EventType        string   `json:"event_type,omitempty"`
	PullRequestTitle string   `json:"pull_request_title,omitempty"`
	SHATitle         string   `json:"sha_title,omitempty"`
	BaseBranch       string   `json:"base_branch,omitempty"`
	HeadBranch       string   `json:"head_branch,omitempty"`
	BaseURL          string   `json:"base_url,omitempty"`
	HeadURL          string   `json:"head_url,omitempty"`
	DefaultBranch    string   `json:"default_branch,omitempty"`
	TriggerComment   string   `json:"trigger_comment,omitempty"`
	PullRequestLabel []string `json:"pull_request_label,omitempty"`
	// ParamFilters records, per Repository custom param name, the filter that
	// selected it when the event was templated, never the param value: the
	// filters may read the webhook payload, gone once the follow-up is
	// templated.
	ParamFilters map[string]string `json:"param_filters,omitempty"`
	// SourceRepoNamespace and SourceRepoName record the Repository the
	// .tekton directory was templated from at event time. At replay the
	// definition is templated from it again: the execution repository of a
	// target namespace may carry other custom params and settings than the
	// source the event matched. Absent on follow-ups stored by an older
	// version, which are templated from their execution repository.
	SourceRepoNamespace string `json:"source_repo_namespace,omitempty"`
	SourceRepoName      string `json:"source_repo_name,omitempty"`
}

// NewFollowUpEvent captures the fields of the event being matched.
func NewFollowUpEvent(event *info.Event) *FollowUpEvent {
	return &FollowUpEvent{
		TriggerTarget:    event.TriggerTarget.String(),
		EventType:        event.EventType,
		PullRequestTitle: event.PullRequestTitle,
		SHATitle:         event.SHATitle,
		BaseBranch:       event.BaseBranch,
		HeadBranch:       event.HeadBranch,
		BaseURL:          event.BaseURL,
		HeadURL:          event.HeadURL,
		DefaultBranch:    event.DefaultBranch,
		TriggerComment:   event.TriggerComment,
		PullRequestLabel: event.PullRequestLabel,
	}
}

// ParamFilterOutcomes returns the recorded param filter outcomes, nil (the
// filters are evaluated) for a follow-up stored by an older version.
func (f *FollowUpEvent) ParamFilterOutcomes() map[string]string {
	if f == nil {
		return nil
	}
	if f.ParamFilters == nil {
		return map[string]string{}
	}
	return f.ParamFilters
}

// Apply returns a copy of event with the captured fields restored. A nil
// FollowUpEvent returns an unchanged copy.
func (f *FollowUpEvent) Apply(event *info.Event) *info.Event {
	restored := *event
	if f == nil {
		return &restored
	}
	restored.TriggerTarget = triggertype.Trigger(f.TriggerTarget)
	restored.EventType = f.EventType
	restored.PullRequestTitle = f.PullRequestTitle
	restored.SHATitle = f.SHATitle
	restored.BaseBranch = f.BaseBranch
	restored.HeadBranch = f.HeadBranch
	restored.BaseURL = f.BaseURL
	restored.HeadURL = f.HeadURL
	restored.DefaultBranch = f.DefaultBranch
	restored.TriggerComment = f.TriggerComment
	restored.PullRequestLabel = f.PullRequestLabel
	return &restored
}

// Pending reports whether the follow-up still has to be created or consumed.
func (e FollowUp) Pending() bool {
	return e.State == "" || e.State == StatePending
}

// FollowUpName composes the deterministic run name of a follow-up: definition
// name and group id, lowercased. Known at event time, it makes the creation
// race-free and gives the run's statuses one identity. It is checked against
// the PipelineRun name rules at event time: the name is fixed, unlike a
// generateName, and the watcher could never create a run of an invalid one.
func FollowUpName(definition, groupID string) (string, error) {
	name := fmt.Sprintf("%s-%s", strings.ToLower(definition), strings.ToLower(groupID))
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return "", fmt.Errorf("the follow-up run name %q is not a valid kubernetes name: %s",
			name, strings.Join(errs, ", "))
	}
	if len(name) > validate.MaxLength {
		return "", fmt.Errorf("the follow-up run name %q is longer than the %d characters of a PipelineRun name: the definition name must be at most %d characters",
			name, validate.MaxLength, validate.MaxLength-len(groupID)-1)
	}
	return name, nil
}

// FollowUpOnRun pairs a follow-up with the run it is stored on, so the watcher
// knows where to patch a follow-up state and where to migrate a follow-up from.
type FollowUpOnRun struct {
	FollowUp FollowUp
	Run      *tektonv1.PipelineRun
}

// FollowUpsOf reads the follow-ups stored on a run. A run without the
// annotation has none.
func FollowUpsOf(pr *tektonv1.PipelineRun) ([]FollowUp, error) {
	annotation := pr.GetAnnotations()[keys.FollowUps]
	if strings.TrimSpace(annotation) == "" {
		return []FollowUp{}, nil
	}
	var followUps []FollowUp
	if err := json.Unmarshal([]byte(annotation), &followUps); err != nil {
		return nil, fmt.Errorf("cannot parse the %s annotation of pipelinerun %s: %w",
			keys.FollowUps, pr.GetName(), err)
	}
	return followUps, nil
}

// AddFollowUps adds follow-ups to a run, replacing the ones already stored
// with the same ID. The caller persists the change with a create or a patch.
func AddFollowUps(pr *tektonv1.PipelineRun, followUps ...FollowUp) error {
	if len(followUps) == 0 {
		return nil
	}
	current, err := FollowUpsOf(pr)
	if err != nil {
		return err
	}
	byID := make(map[string]int, len(current)+len(followUps))
	for i, followUp := range current {
		byID[followUp.ID] = i
	}
	for _, followUp := range followUps {
		if i, ok := byID[followUp.ID]; ok {
			current[i] = followUp
			continue
		}
		current = append(current, followUp)
		byID[followUp.ID] = len(current) - 1
	}
	return stampFollowUps(pr, current)
}

// RemoveFollowUps removes follow-ups by ID from a run. The caller persists the
// change with a patch.
func RemoveFollowUps(pr *tektonv1.PipelineRun, ids ...string) error {
	if len(ids) == 0 {
		return nil
	}
	remove := make(map[string]bool, len(ids))
	for _, id := range ids {
		remove[id] = true
	}
	current, err := FollowUpsOf(pr)
	if err != nil {
		return err
	}
	kept := current[:0]
	for _, followUp := range current {
		if !remove[followUp.ID] {
			kept = append(kept, followUp)
		}
	}
	return stampFollowUps(pr, kept)
}

// ConsumeFollowUp moves a follow-up stored on a run to a terminal state.
func ConsumeFollowUp(pr *tektonv1.PipelineRun, id, state string) error {
	current, err := FollowUpsOf(pr)
	if err != nil {
		return err
	}
	for i := range current {
		if current[i].ID == id {
			current[i].State = state
			return stampFollowUps(pr, current)
		}
	}
	return nil
}

// FollowUpsPatch is the merge patch carrying the current follow-ups of a run.
func FollowUpsPatch(pr *tektonv1.PipelineRun) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]string{
				keys.FollowUps: pr.GetAnnotations()[keys.FollowUps],
			},
		},
	}
}

func stampFollowUps(pr *tektonv1.PipelineRun, followUps []FollowUp) error {
	if pr.GetAnnotations() == nil {
		pr.Annotations = map[string]string{}
	}
	if len(followUps) == 0 {
		delete(pr.Annotations, keys.FollowUps)
		return nil
	}
	data, err := json.Marshal(followUps)
	if err != nil {
		return fmt.Errorf("cannot marshal the follow-ups of pipelinerun %s: %w", pr.GetName(), err)
	}
	pr.Annotations[keys.FollowUps] = string(data)
	return nil
}

// CollectFollowUps gathers the follow-ups stored on every run of a group,
// deduped by ID. A migration can briefly leave the same ID on two runs: a
// terminal copy wins over a pending one, and ties break by run name so
// the choice is stable across reconciles.
func CollectFollowUps(group []*tektonv1.PipelineRun) ([]FollowUpOnRun, error) {
	runs := make([]*tektonv1.PipelineRun, len(group))
	copy(runs, group)
	sort.Slice(runs, func(i, j int) bool { return runs[i].GetName() < runs[j].GetName() })

	byID := map[string]FollowUpOnRun{}
	for _, run := range runs {
		followUps, err := FollowUpsOf(run)
		if err != nil {
			return nil, err
		}
		for _, followUp := range followUps {
			existing, ok := byID[followUp.ID]
			if !ok || (existing.FollowUp.Pending() && !followUp.Pending()) {
				byID[followUp.ID] = FollowUpOnRun{FollowUp: followUp, Run: run}
			}
		}
	}
	collected := make([]FollowUpOnRun, 0, len(byID))
	for _, fu := range byID {
		collected = append(collected, fu)
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].FollowUp.ID < collected[j].FollowUp.ID })
	return collected, nil
}
