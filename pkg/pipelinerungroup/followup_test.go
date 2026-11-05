package pipelinerungroup

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
)

// runWith builds a created run with the given name, optionally with the
// given follow-ups stored on it.
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

	// the same ID lands again with a terminal state: the migration does this
	// with an add-before-remove, and the copy must replace the pending one
	replacement := pendingFollowUp("deploy", []string{"build"})
	replacement.State = StateCreated
	assert.NilError(t, AddFollowUps(pr, replacement))

	followUps, err := FollowUpsOf(pr)
	assert.NilError(t, err)
	assert.Equal(t, 1, len(followUps))
	assert.Equal(t, StateCreated, followUps[0].State)
}

func TestRemoveAndConsumeFollowUps(t *testing.T) {
	deploy := pendingFollowUp("deploy", []string{"build"})
	publish := pendingFollowUp("publish", []string{"deploy"})

	tests := []struct {
		name         string
		consume      string
		consumeState string
		remove       string
		wantIDs      []string
		wantState    string
	}{
		{
			name:         "consuming a follow-up moves it to a terminal state without dropping it",
			consume:      "deploy-group1",
			consumeState: StateSkipped,
			wantIDs:      []string{"deploy-group1", "publish-group1"},
			wantState:    StateSkipped,
		},
		{
			name:    "removing a follow-up drops it and keeps the others",
			remove:  "deploy-group1",
			wantIDs: []string{"publish-group1"},
		},
		{
			name:         "consuming an unknown followUp is a no-op",
			consume:      "unknown-group1",
			consumeState: StateSkipped,
			wantIDs:      []string{"deploy-group1", "publish-group1"},
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
			if tt.remove != "" {
				assert.NilError(t, RemoveFollowUps(pr, tt.remove))
			}

			followUps, err := FollowUpsOf(pr)
			assert.NilError(t, err)
			assert.Equal(t, len(tt.wantIDs), len(followUps))
			for i, want := range tt.wantIDs {
				assert.Equal(t, want, followUps[i].ID)
			}
			if tt.wantState != "" && tt.consume != "" && tt.consume != "unknown-group1" {
				for _, followUp := range followUps {
					if followUp.ID == tt.consume {
						assert.Equal(t, tt.wantState, followUp.State)
					}
				}
			}
		})
	}
}

func TestRemoveLastFollowUpDeletesAnnotation(t *testing.T) {
	pr, err := runWith("build", pendingFollowUp("deploy", []string{"build"}))
	assert.NilError(t, err)

	assert.NilError(t, RemoveFollowUps(pr, "deploy-group1"))
	_, has := pr.GetAnnotations()[keys.FollowUps]
	assert.Assert(t, !has, "removing the last follow-up must delete the annotation")
}

func TestCollectFollowUpsDeduplicatesByID(t *testing.T) {
	// the migration copies a follow-up to its new run before removing it from
	// the old one: the same ID is briefly stored twice
	deploy := pendingFollowUp("deploy", []string{"build"})

	root, err := runWith("build", deploy)
	assert.NilError(t, err)
	depRun, err := runWith("tests", deploy)
	assert.NilError(t, err)

	// terminal copy wins over pending
	terminal := deploy
	terminal.State = StateCreated
	terminalRun, err := runWith("build2", terminal)
	assert.NilError(t, err)

	tests := []struct {
		name      string
		group     []*tektonv1.PipelineRun
		wantCount int
		wantState string
		wantOn    string
	}{
		{
			name:      "a pending follow-up stored twice is collected once",
			group:     []*tektonv1.PipelineRun{root, depRun},
			wantCount: 1,
			wantState: StatePending,
			wantOn:    "build-generated",
		},
		{
			name:      "a terminal copy wins over a pending one",
			group:     []*tektonv1.PipelineRun{root, terminalRun},
			wantCount: 1,
			wantState: StateCreated,
			wantOn:    "build2-generated",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collected, err := CollectFollowUps(tt.group)
			assert.NilError(t, err)
			assert.Equal(t, tt.wantCount, len(collected))
			assert.Equal(t, tt.wantState, collected[0].FollowUp.State)
			assert.Equal(t, tt.wantOn, collected[0].Run.GetName())
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

	// the event the watcher rebuilds from the annotations of a GitHub push of
	// a merge commit: the pull request number makes it a pull_request
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
	matched.TriggerComment = "/retest stage=ship"
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
			assert.Equal(t, "/retest stage=ship", got.TriggerComment)
			assert.DeepEqual(t, []string{"deploy"}, got.PullRequestLabel)
		})
	}
}
