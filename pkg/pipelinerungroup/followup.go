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
// maxFollowUpsSize bounds the follow-ups annotation, well under the 256KB
// shared by all the annotations of an object.
const maxFollowUpsSize = 64 * 1024

const (
	StatePending   = "pending"
	StateCreated   = "created"
	StateSkipped   = "skipped"
	StateCancelled = "cancelled"
	// StateFailed is a follow-up whose run could not be created.
	StateFailed = "failed"
)

// FollowUp is the stored intent of a follow-up until its run is created.
type FollowUp struct {
	// ID is the deterministic run name, also the follow-up identity.
	ID string `json:"id"`
	// Name is the definition name of the follow-up.
	Name string `json:"name"`
	// Deps are the definition names the follow-up waits on.
	Deps []string `json:"deps,omitempty"`
	// State is pending or one of the terminal follow-up states.
	State string `json:"state,omitempty"`
	// Event is the event as the matcher saw it. Absent on older follow-ups.
	Event *FollowUpEvent `json:"event,omitempty"`
}

// FollowUpEvent holds the event fields the CEL and template variables of a
// follow-up are computed from, since run annotations are lossy. The trigger
// comment is deliberately not stored: it is user controlled and unbounded, so
// trigger_comment is empty for a follow-up.
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
	PullRequestLabel []string `json:"pull_request_label,omitempty"`
	// ParamFilters records the filter that selected each custom param, never
	// its value.
	ParamFilters map[string]string `json:"param_filters,omitempty"`
	// SourceRepoNamespace and SourceRepoName identify the Repository the
	// definition is templated from at replay.
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
		PullRequestLabel: event.PullRequestLabel,
	}
}

// ParamFilterOutcomes returns the recorded filters, nil for an older follow-up.
func (f *FollowUpEvent) ParamFilterOutcomes() map[string]string {
	if f == nil {
		return nil
	}
	if f.ParamFilters == nil {
		return map[string]string{}
	}
	return f.ParamFilters
}

// Apply returns a copy of event with the captured fields restored.
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
	restored.PullRequestLabel = f.PullRequestLabel
	return &restored
}

// Pending reports whether the follow-up still has to be created or consumed.
func (e FollowUp) Pending() bool {
	return e.State == "" || e.State == StatePending
}

// FollowUpName composes the deterministic run name of a follow-up from its
// definition name and group id, validated as a PipelineRun name.
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

// FollowUpOnRun pairs a follow-up with the run it is stored on.
type FollowUpOnRun struct {
	FollowUp FollowUp
	Run      *tektonv1.PipelineRun
}

// FollowUpsOf reads the follow-ups stored on a run.
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

// AddFollowUps adds follow-ups to a run, replacing ones with the same ID.
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
	return stampFollowUps(pr, current, true)
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
			// no size check: an accepted follow-up must always be consumable,
			// and a terminal state only adds a few bytes
			return stampFollowUps(pr, current, false)
		}
	}
	return nil
}

// FollowUpsPatch is the merge patch for the follow-ups of a run; an absent
// annotation is sent as null to delete it.
func FollowUpsPatch(pr *tektonv1.PipelineRun) map[string]any {
	var value any
	if v, ok := pr.GetAnnotations()[keys.FollowUps]; ok {
		value = v
	}
	return map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				keys.FollowUps: value,
			},
		},
	}
}

func stampFollowUps(pr *tektonv1.PipelineRun, followUps []FollowUp, enforceLimit bool) error {
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
	if enforceLimit && len(data) > maxFollowUpsSize {
		return fmt.Errorf("the follow-ups of pipelinerun %s take %d bytes, more than the %d allowed in an annotation",
			pr.GetName(), len(data), maxFollowUpsSize)
	}
	pr.Annotations[keys.FollowUps] = string(data)
	return nil
}

// CollectFollowUps gathers the follow-ups stored on the runs of a group, sorted
// by ID.
func CollectFollowUps(group []*tektonv1.PipelineRun) ([]FollowUpOnRun, error) {
	var collected []FollowUpOnRun
	for _, run := range group {
		followUps, err := FollowUpsOf(run)
		if err != nil {
			return nil, err
		}
		for _, followUp := range followUps {
			collected = append(collected, FollowUpOnRun{FollowUp: followUp, Run: run})
		}
	}
	sort.Slice(collected, func(i, j int) bool { return collected[i].FollowUp.ID < collected[j].FollowUp.ID })
	return collected, nil
}
