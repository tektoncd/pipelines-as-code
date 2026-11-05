package pipelinerungroup

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
)

// runWith builds a created run holding the given follow-ups.
func runWith(name string, followUps ...FollowUp) (*tektonv1.PipelineRun, error) {
	pr := groupPR(name, "uid-"+name, "started")
	if err := AddFollowUps(pr, followUps...); err != nil {
		return nil, err
	}
	return pr, nil
}

func TestFollowUpsRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		stamp   []FollowUp
		wantIDs []string
	}{
		{
			name:    "a run without the annotation has no follow-ups",
			wantIDs: []string{},
		},
		{
			name: "stamped followUps are read back",
			stamp: []FollowUp{
				pendingFollowUp("deploy", []string{"build"}),
				pendingFollowUp("publish", []string{"deploy"}),
			},
			wantIDs: []string{"deploy-group1", "publish-group1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, err := runWith("build", tt.stamp...)
			assert.NilError(t, err)

			followUps, err := FollowUpsOf(pr)
			assert.NilError(t, err)
			assert.Equal(t, len(tt.wantIDs), len(followUps))
			for i, want := range tt.wantIDs {
				assert.Equal(t, want, followUps[i].ID)
			}
		})
	}
}

func TestAddFollowUpsReplacesSameID(t *testing.T) {
	pr, err := runWith("build", pendingFollowUp("deploy", []string{"build"}))
	assert.NilError(t, err)

	// the same ID added again with a terminal state replaces the pending one
	replacement := pendingFollowUp("deploy", []string{"build"})
	replacement.State = StateCreated
	assert.NilError(t, AddFollowUps(pr, replacement))

	followUps, err := FollowUpsOf(pr)
	assert.NilError(t, err)
	assert.Equal(t, 1, len(followUps))
	assert.Equal(t, StateCreated, followUps[0].State)
}

func TestConsumeFollowUps(t *testing.T) {
	deploy := pendingFollowUp("deploy", []string{"build"})
	publish := pendingFollowUp("publish", []string{"deploy"})

	tests := []struct {
		name         string
		consume      string
		consumeState string
		wantIDs      []string
		stateOf      string
		wantState    string
	}{
		{
			name:         "consuming a follow-up moves it to a terminal state without dropping it",
			consume:      "deploy-group1",
			consumeState: StateSkipped,
			wantIDs:      []string{"deploy-group1", "publish-group1"},
			stateOf:      "deploy-group1",
			wantState:    StateSkipped,
		},
		{
			name:         "consuming an unknown followUp is a no-op",
			consume:      "unknown-group1",
			consumeState: StateSkipped,
			wantIDs:      []string{"deploy-group1", "publish-group1"},
			stateOf:      "deploy-group1",
			wantState:    StatePending,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, err := runWith("build", deploy, publish)
			assert.NilError(t, err)

			if tt.consume != "" {
				assert.NilError(t, ConsumeFollowUp(pr, tt.consume, tt.consumeState))
			}

			followUps, err := FollowUpsOf(pr)
			assert.NilError(t, err)
			assert.Equal(t, len(tt.wantIDs), len(followUps))
			for i, want := range tt.wantIDs {
				assert.Equal(t, want, followUps[i].ID)
			}
			for _, followUp := range followUps {
				if followUp.ID == tt.stateOf {
					assert.Equal(t, tt.wantState, followUp.State)
				}
			}
		})
	}
}

func TestFollowUpsPatchDeletesAbsentAnnotation(t *testing.T) {
	pr, err := runWith("build")
	assert.NilError(t, err)
	_, has := pr.GetAnnotations()[keys.FollowUps]
	assert.Assert(t, !has)

	// a merge patch only deletes a key sent as null
	raw, err := json.Marshal(FollowUpsPatch(pr))
	assert.NilError(t, err)
	assert.Equal(t, `{"metadata":{"annotations":{"`+keys.FollowUps+`":null}}}`, string(raw))
}

func TestCollectFollowUps(t *testing.T) {
	deploy := pendingFollowUp("deploy", []string{"build"})
	publish := pendingFollowUp("publish", []string{"build"})
	build, err := runWith("build", publish, deploy)
	assert.NilError(t, err)
	tests, err := runWith("tests")
	assert.NilError(t, err)

	collected, err := CollectFollowUps([]*tektonv1.PipelineRun{tests, build})
	assert.NilError(t, err)
	assert.Equal(t, 2, len(collected))
	assert.Equal(t, "deploy-group1", collected[0].FollowUp.ID)
	assert.Equal(t, "publish-group1", collected[1].FollowUp.ID)
	assert.Equal(t, build, collected[0].Run)

	collected, err = CollectFollowUps(nil)
	assert.NilError(t, err)
	assert.Equal(t, 0, len(collected))
}

func TestFollowUpsOfOddAnnotations(t *testing.T) {
	tests := []struct {
		name       string
		annotation string
		expected   int
		wantErr    bool
	}{
		{name: "whitespace only", annotation: "  \n"},
		{name: "an empty array", annotation: "[]"},
		{name: "a json null", annotation: "null"},
		{name: "an object instead of an array", annotation: `{"id": "x"}`, wantErr: true},
		{name: "unknown fields are ignored", annotation: `[{"id": "x", "name": "x", "future": 1}]`, expected: 1},
		{name: "an entry without a state is pending", annotation: `[{"id": "x", "name": "x"}]`, expected: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, err := runWith("build")
			assert.NilError(t, err)
			pr.Annotations[keys.FollowUps] = tt.annotation

			followUps, err := FollowUpsOf(pr)
			if tt.wantErr {
				assert.ErrorContains(t, err, "cannot parse")
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.expected, len(followUps))
			for _, followUp := range followUps {
				assert.Assert(t, followUp.Pending())
			}
		})
	}
}

func TestFollowUpsOfBrokenAnnotation(t *testing.T) {
	pr, err := runWith("build")
	assert.NilError(t, err)
	pr.Annotations[keys.FollowUps] = "{not json"

	_, err = FollowUpsOf(pr)
	assert.ErrorContains(t, err, "cannot parse")
}

func TestFollowUpEvent(t *testing.T) {
	matched := info.NewEvent()
	matched.TriggerTarget = triggertype.Push
	matched.EventType = "push"
	matched.SHATitle = "merge the feature"
	matched.BaseBranch = "refs/heads/main"
	matched.HeadBranch = "refs/heads/main"
	matched.BaseURL = "https://forge/org/repo"

	// the event rebuilt from the annotations of a GitHub merge commit push
	rebuilt := info.NewEvent()
	rebuilt.TriggerTarget = triggertype.PullRequest
	rebuilt.EventType = "push"
	rebuilt.PullRequestNumber = 42
	rebuilt.SHA = "abc123"

	tests := []struct {
		name        string
		captured    *FollowUpEvent
		wantTrigger triggertype.Trigger
		wantTitle   string
	}{
		{
			name:        "the captured event fields are restored",
			captured:    NewFollowUpEvent(matched),
			wantTrigger: triggertype.Push,
			wantTitle:   "merge the feature",
		},
		{
			name:        "a follow-up stored without an event keeps the rebuilt one",
			wantTrigger: triggertype.PullRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// the capture survives the annotation round trip
			pr := &tektonv1.PipelineRun{}
			assert.NilError(t, AddFollowUps(pr, FollowUp{ID: "deploy-group1", Name: "deploy", Deps: []string{"build"}, Event: tt.captured}))
			stored, err := FollowUpsOf(pr)
			assert.NilError(t, err)

			got := stored[0].Event.Apply(rebuilt)
			assert.Equal(t, tt.wantTrigger, got.TriggerTarget)
			assert.Equal(t, tt.wantTitle, got.SHATitle)
			// what the capture does not hold comes from the rebuilt event
			assert.Equal(t, "abc123", got.SHA)
			assert.Equal(t, triggertype.PullRequest, rebuilt.TriggerTarget, "the rebuilt event must not be modified")
		})
	}
}

func TestFollowUpEventTemplatingFields(t *testing.T) {
	matched := info.NewEvent()
	matched.DefaultBranch = "trunk"
	matched.PullRequestLabel = []string{"deploy"}
	withFilters := NewFollowUpEvent(matched)
	withFilters.ParamFilters = map[string]string{"stage": `body.action == "opened"`}

	tests := []struct {
		name         string
		captured     *FollowUpEvent
		wantOutcomes map[string]string
		wantRestored bool
	}{
		{
			name:         "the templating fields and the filter outcomes are restored",
			captured:     withFilters,
			wantOutcomes: map[string]string{"stage": `body.action == "opened"`},
			wantRestored: true,
		},
		{
			name:         "recorded with no filter matched, the replay selects none",
			captured:     NewFollowUpEvent(matched),
			wantOutcomes: map[string]string{},
			wantRestored: true,
		},
		{
			name: "stored by an older version, the filters are evaluated",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.DeepEqual(t, tt.wantOutcomes, tt.captured.ParamFilterOutcomes())
			got := tt.captured.Apply(info.NewEvent())
			if !tt.wantRestored {
				assert.Equal(t, "", got.DefaultBranch)
				return
			}
			assert.Equal(t, "trunk", got.DefaultBranch)
			assert.DeepEqual(t, []string{"deploy"}, got.PullRequestLabel)
		})
	}
}

func TestAddFollowUpsRejectsAnOversizedAnnotation(t *testing.T) {
	pr, err := runWith("build")
	assert.NilError(t, err)

	big := pendingFollowUp("deploy", []string{"build"})
	big.Event = &FollowUpEvent{PullRequestTitle: strings.Repeat("a", maxFollowUpsSize)}

	err = AddFollowUps(pr, big)
	assert.ErrorContains(t, err, "more than the")
	_, stamped := pr.Annotations[keys.FollowUps]
	assert.Assert(t, !stamped, "a rejected follow-up must not be stamped")
}

func TestFollowUpEventDropsTheTriggerComment(t *testing.T) {
	matched := info.NewEvent()
	matched.TriggerComment = "/retest stage=ship"

	restored := NewFollowUpEvent(matched).Apply(info.NewEvent())
	assert.Equal(t, "", restored.TriggerComment)
}

func TestConsumeFollowUpAtTheSizeLimit(t *testing.T) {
	deploy := pendingFollowUp("deploy", []string{"build"})
	deploy.Event = &FollowUpEvent{PullRequestTitle: "a"}
	base, err := json.Marshal([]FollowUp{deploy})
	assert.NilError(t, err)
	deploy.Event.PullRequestTitle = strings.Repeat("a", maxFollowUpsSize-len(base)+1)

	pr, err := runWith("build", deploy)
	assert.NilError(t, err)
	assert.Equal(t, maxFollowUpsSize, len(pr.Annotations[keys.FollowUps]))

	assert.NilError(t, ConsumeFollowUp(pr, deploy.ID, StateCancelled))
	followUps, err := FollowUpsOf(pr)
	assert.NilError(t, err)
	assert.Equal(t, StateCancelled, followUps[0].State)
}
