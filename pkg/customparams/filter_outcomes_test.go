package customparams

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

// TestFilterOutcomes checks the param filters evaluated when the event is
// processed can be recorded and replayed once the payload they read is gone.
func TestFilterOutcomes(t *testing.T) {
	repoParams := []v1alpha1.Params{
		{Name: "stage", Value: "review", Filter: `body.action == "opened"`},
		{Name: "stage", Value: "ship", Filter: `body.action == "closed"`},
		{Name: "token", SecretRef: &v1alpha1.Secret{Name: "creds", Key: "token"}},
	}
	withPayload := &info.Event{Event: map[string]any{"action": "opened"}, SHA: "abc"}
	// the event the watcher rebuilds: no payload
	withoutPayload := &info.Event{SHA: "abc"}

	tests := []struct {
		name      string
		event     *info.Event
		outcomes  map[string]string
		wantStage string
		wantErr   bool
	}{
		{
			name:      "the filters are evaluated against the payload",
			event:     withPayload,
			wantStage: "review",
		},
		{
			name:    "without the payload a filter reading it fails",
			event:   withoutPayload,
			wantErr: true,
		},
		{
			name:      "the recorded outcome selects the param without the payload",
			event:     withoutPayload,
			outcomes:  map[string]string{"stage": `body.action == "opened"`},
			wantStage: "review",
		},
		{
			name:     "a filter changed since the event is not selected",
			event:    withoutPayload,
			outcomes: map[string]string{"stage": `body.action == "reopened"`},
		},
		{
			name:     "no recorded outcome selects no filtered param",
			event:    withoutPayload,
			outcomes: map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			logger := zap.NewNop().Sugar()
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
				Spec:       v1alpha1.RepositorySpec{Params: &repoParams},
			}
			cp := NewCustomParams(tt.event, repo, &params.Run{},
				&kitesthelper.KinterfaceTest{GetSecretResult: map[string]string{"creds": "s3cr3t"}},
				events.NewEventEmitter(nil, logger), &provider.TestProviderImp{})
			if tt.outcomes != nil {
				cp = cp.WithFilterOutcomes(tt.outcomes)
			}

			got, _, err := cp.GetParams(ctx)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantStage, got["stage"])
			// unfiltered params resolve as usual, secrets included
			assert.Equal(t, "s3cr3t", got["token"])
			assert.Equal(t, "abc", got["revision"])
		})
	}
}

func TestMatchedFilters(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	logger := zap.NewNop().Sugar()
	repoParams := []v1alpha1.Params{
		{Name: "stage", Value: "review", Filter: `body.action == "opened"`},
		{Name: "stage", Value: "any", Filter: `true`},
		{Name: "lane", Value: "fast", Filter: `body.action == "closed"`},
		{Name: "token", SecretRef: &v1alpha1.Secret{Name: "creds", Key: "token"}},
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "ns"},
		Spec:       v1alpha1.RepositorySpec{Params: &repoParams},
	}
	cp := NewCustomParams(&info.Event{Event: map[string]any{"action": "opened"}}, repo, &params.Run{},
		&kitesthelper.KinterfaceTest{}, events.NewEventEmitter(nil, logger), &provider.TestProviderImp{})

	matched, err := cp.MatchedFilters(ctx)
	assert.NilError(t, err)
	// the first matching filter of a name wins, like GetParams; no value is
	// recorded, and params without a filter are not part of it
	assert.DeepEqual(t, map[string]string{"stage": `body.action == "opened"`}, matched)
}
