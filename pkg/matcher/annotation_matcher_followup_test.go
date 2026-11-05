package matcher

import (
	"net/http"
	"slices"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	ghprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

// TestMatchPipelinerunByAnnotationFollowUps checks a follow-up matches on its
// deferred on-cel-expression only: the other matching annotations have no
// effect on it, and it only matches alongside a plain definition.
func TestMatchPipelinerunByAnnotationFollowUps(t *testing.T) {
	definition := func(name string, annotations map[string]string) *tektonv1.PipelineRun {
		return &tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations}}
	}
	build := definition("build", map[string]string{
		keys.OnEvent:        "[pull_request]",
		keys.OnTargetBranch: "[main]",
	})
	followUp := func(name string, extra map[string]string) *tektonv1.PipelineRun {
		annotations := map[string]string{
			keys.OnPipelineRun:   `["build"]`,
			keys.OnCelExpression: `runs.build.status == "Succeeded"`,
		}
		for k, v := range extra {
			annotations[k] = v
		}
		return definition(name, annotations)
	}
	newEvent := func(trigger triggertype.Trigger, eventType string) info.Event {
		return info.Event{
			TriggerTarget: trigger,
			EventType:     eventType,
			BaseBranch:    "main",
			HeadBranch:    "feature",
			Request:       &info.Request{Header: http.Header{}},
		}
	}

	tests := []struct {
		name          string
		pruns         []*tektonv1.PipelineRun
		event         info.Event
		wantMatched   []string
		wantFollowUps []string
		wantUnmatched []string
		wantErr       bool
	}{
		{
			name: "a follow-up's on-event is ignored",
			pruns: []*tektonv1.PipelineRun{
				build,
				followUp("deploy", map[string]string{keys.OnEvent: "[push]", keys.OnTargetBranch: "[release]"}),
			},
			event:         newEvent(triggertype.PullRequest, "pull_request"),
			wantMatched:   []string{"build", "deploy"},
			wantFollowUps: []string{"deploy"},
		},
		{
			name: "a follow-up's on-comment is ignored",
			pruns: []*tektonv1.PipelineRun{
				build,
				followUp("deploy", map[string]string{keys.OnComment: "^/deploy"}),
			},
			event:         newEvent(triggertype.PullRequest, "pull_request"),
			wantMatched:   []string{"build", "deploy"},
			wantFollowUps: []string{"deploy"},
		},
		{
			name:          "with no plain definition matched the follow-ups are unmatched",
			pruns:         []*tektonv1.PipelineRun{build, followUp("deploy", nil)},
			event:         newEvent(triggertype.Push, "push"),
			wantUnmatched: []string{"build", "deploy"},
			wantErr:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			logger := zap.NewNop().Sugar()
			cs := &params.Run{}
			repo := &v1alpha1.Repository{ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"}}
			event := tt.event

			matches, unmatched, err := MatchPipelinerunByAnnotation(ctx, logger, tt.pruns, cs, &event,
				&ghprovider.Provider{}, events.NewEventEmitter(nil, logger), repo, true)
			if tt.wantErr {
				assert.Assert(t, err != nil)
			} else {
				assert.NilError(t, err)
			}

			matched := []string{}
			followUps := []string{}
			for _, m := range matches {
				matched = append(matched, getName(m.PipelineRun))
				if m.Config[ConfigFollowUp] == "true" {
					followUps = append(followUps, getName(m.PipelineRun))
				}
			}
			assert.DeepEqual(t, append([]string{}, tt.wantMatched...), matched)
			assert.DeepEqual(t, append([]string{}, tt.wantFollowUps...), followUps)
			unmatchedNames := []string{}
			for name := range unmatched {
				unmatchedNames = append(unmatchedNames, name)
			}
			slices.Sort(unmatchedNames)
			assert.DeepEqual(t, append([]string{}, tt.wantUnmatched...), unmatchedNames)
		})
	}
}
