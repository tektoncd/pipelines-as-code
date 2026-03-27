package customparams

import (
	"encoding/json"
	"testing"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/incoming"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestApplyIncomingParams(t *testing.T) {
	tests := []struct {
		name               string
		initialParams      map[string]string
		payload            *incoming.Payload
		expectedParams     map[string]string
		expectedLogSnippet string
	}{
		{
			name: "apply params",
			initialParams: map[string]string{
				"key1": "value1",
			},
			payload: &incoming.Payload{
				Params: map[string]any{
					"key2": "value2",
				},
			},
			expectedParams: map[string]string{
				"key1": "value1",
			},
		},
		{
			name: "apply params with same key",
			initialParams: map[string]string{
				"key1": "value1",
			},
			payload: &incoming.Payload{
				Params: map[string]any{
					"key1": "value2",
				},
			},
			expectedParams: map[string]string{
				"key1": "value2",
			},
		},
		{
			name: "no request in event",
			initialParams: map[string]string{
				"key1": "value1",
			},
			expectedParams: map[string]string{
				"key1": "value1",
			},
		},
		{
			name: "cannot convert incoming param",
			initialParams: map[string]string{
				"key1": "value1",
			},
			payload: &incoming.Payload{
				Params: map[string]any{
					"key2": 1,
				},
			},
			expectedLogSnippet: "cannot convert incoming param key: key2 value: 1 as string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, tlog := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			jsonPayload, _ := json.Marshal(tt.payload)
			event := &info.Event{}
			if tt.payload != nil {
				event.Request = &info.Request{
					Payload: jsonPayload,
				}
			}
			p := &CustomParams{
				event: event,
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			p.eventEmitter = events.NewEventEmitter(stdata.Kube, logger)
			gotParams := p.applyIncomingParams(tt.initialParams)

			assert.DeepEqual(t, tt.initialParams, gotParams)
			if tt.expectedLogSnippet != "" {
				logmsg := tlog.FilterMessageSnippet(tt.expectedLogSnippet).TakeAll()
				assert.Assert(t, len(logmsg) > 0, "log message filtered %s expected %s alllogs: %+v", logmsg,
					tt.expectedLogSnippet, tlog.TakeAll())
			}
		})
	}
}

func TestProcessTemplates(t *testing.T) {
	ns := "there"
	// event_type is a standard params and should override it from the command line
	// foo is never set anywhere so will be skipped
	// hello is a custom params and should be overridden as well with value
	triggerCommentArgs := `/test foobar event_type=push foo="bar" hello="\"yolo\""`
	tests := []struct {
		name               string
		event              *info.Event
		expected           map[string]string
		repository         *v1alpha1.Repository
		secretData         map[string]string
		expectedLogSnippet string
		expectedError      bool
		incomingPayload    string
		vcx                *provider.TestProviderImp
	}{
		{
			name:     "params/basic",
			expected: map[string]string{"params": "batman"},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:  "params",
							Value: "batman",
						},
					},
				},
			},
		},
		{
			name: "params/added_from_incoming",
			expected: map[string]string{
				"the_best_superhero_is": "superman",
				"event_type":            "",
				"repo_name":             "",
				"repo_owner":            "",
				"repo_url":              "",
				"revision":              "",
				"sender":                "",
				"source_branch":         "",
				"source_url":            "",
				"git_tag":               "",
				"target_branch":         "",
				"target_namespace":      "",
				"trigger_comment":       "",
				"pull_request_labels":   "",
			},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{},
			},
			incomingPayload: `{"params":{"the_best_superhero_is":"superman"}}`,
		},
		{
			name:     "params/added_from_incoming_webhook_override",
			expected: map[string]string{"the_best_superhero_is": "you"},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:  "the_best_superhero_is",
							Value: "batman",
						},
					},
				},
			},
			incomingPayload: `{"params":{"the_best_superhero_is":"you"}}`,
		},
		{
			name:     "params/from secret",
			expected: map[string]string{"params": "gone", "target_namespace": ns},
			secretData: map[string]string{
				"name": "gone",
			},
			repository: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns,
				},
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name: "params",
							SecretRef: &v1alpha1.Secret{
								Name: "name",
								Key:  "key",
							},
						},
					},
				},
			},
		},
		{
			name:          "params/from unknown secret",
			expectedError: true,
			repository: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns,
				},
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name: "params",
							SecretRef: &v1alpha1.Secret{
								Name: "unknown",
							},
						},
					},
				},
			},
		},
		{
			name:     "params/use last params when two values of the same name",
			expected: map[string]string{"params": "robin"},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:  "params",
							Value: "batman",
						},
						{
							Name:  "params",
							Value: "robin",
						},
					},
				},
			},
		},
		{
			name:     "params/fallback to stdparams",
			expected: map[string]string{"event_type": "pull_request"},
			event:    &info.Event{EventType: "pull_request"},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{},
				},
			},
		},
		{
			name: "params/override params via gitops arguments",
			expected: map[string]string{
				"event_type":      "push",
				"hello":           `"yolo"`,
				"trigger_comment": triggerCommentArgs,
			},
			event: &info.Event{EventType: "pull_request", TriggerComment: triggerCommentArgs},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{Name: "hello", Value: "welcome"},
					},
				},
			},
		},
		{
			name: "params/override params with no value via gitops arguments",
			expected: map[string]string{
				"event_type":      "push",
				"hello":           `"yolo"`,
				"trigger_comment": triggerCommentArgs,
			},
			event: &info.Event{EventType: "pull_request", TriggerComment: triggerCommentArgs},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{Name: "hello"},
					},
				},
			},
		},
		{
			name:               "params/skip with no name",
			expectedLogSnippet: "no name has been set in params[0] of repo",
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Value: "batman",
						},
					},
				},
			},
		},
		{
			name:     "params/skip with no value",
			expected: map[string]string{},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name: "empty-param",
						},
					},
				},
			},
		},
		{
			name:               "params/pick value when value and secret set",
			expected:           map[string]string{"params": "batman"},
			expectedLogSnippet: "repo repo, param name params has a value and secretref, picking value",
			repository: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name: "repo",
				},
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:  "params",
							Value: "batman",
							SecretRef: &v1alpha1.Secret{
								Name: "name",
								Key:  "key",
							},
						},
					},
				},
			},
		},
		{
			name:     "params/filter",
			expected: map[string]string{"event_type": "pull_request", "params": "batman"},
			event:    &info.Event{EventType: "pull_request"},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:   "params",
							Value:  "batman",
							Filter: `pac.event_type == "pull_request"`,
						},
					},
				},
			},
		},
		{
			name:     "params/filter on body",
			expected: map[string]string{"params": "batman", "event_type": "pull_request"},
			event:    &info.Event{EventType: "pull_request", Event: github.PullRequestEvent{Number: github.Ptr(42)}},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:   "params",
							Value:  "batman",
							Filter: `body.number == 42`,
						},
					},
				},
			},
		},
		{
			name:          "params/filter on body with bad filter",
			event:         &info.Event{EventType: "pull_request", Event: github.PullRequestEvent{Number: github.Ptr(42)}},
			expectedError: true,
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:   "params",
							Value:  "batman",
							Filter: `body.BADDDADA == 42`,
						},
					},
				},
			},
		},
		{
			name:          "params/bad filter skipped",
			event:         &info.Event{EventType: "push"},
			expectedError: true,
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:   "params",
							Value:  "batman",
							Filter: `BADDADADDADA`,
						},
					},
				},
			},
		},
		{
			name:          "params/bad payload skipped",
			event:         &info.Event{EventType: "push", Event: "BADADDADA"},
			expectedError: true,
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:   "params",
							Value:  "batman",
							Filter: `body.number == 42`,
						},
					},
				},
			},
		},
		{
			name:               "params/no filter match",
			expectedLogSnippet: "skipping params name params, filter condition is false",
			event:              &info.Event{EventType: "push"},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:   "params",
							Value:  "batman",
							Filter: `pac.event_type == "pull_request"`,
						},
					},
				},
			},
		},
		{
			name:               "params/not a condition",
			expectedLogSnippet: "skipping params name params, filter condition is not a boolean",
			event:              &info.Event{EventType: "push"},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:   "params",
							Value:  "batman",
							Filter: `pac.event_type`,
						},
					},
				},
			},
		},
		{
			name:               "params/two filters same name, match first",
			expected:           map[string]string{"params": "batman1", "event_type": "pull_request"},
			event:              &info.Event{EventType: "pull_request"},
			expectedLogSnippet: "skipping params name params, filter has already been matched previously",
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:   "params",
							Value:  "batman1",
							Filter: `pac.event_type == "pull_request"`,
						},
						{
							Name:   "params",
							Value:  "batman2",
							Filter: `pac.event_type == "pull_request"`,
						},
					},
				},
			},
		},
		{
			name: "params/changed files",
			expected: map[string]string{
				"all": "all matched",
			},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:   "all",
							Value:  "all matched",
							Filter: `files.all.exists(x, x.matches('renamed.go'))`,
						},
						{
							Name:   "missing",
							Value:  "did not match",
							Filter: `files.all.exists(x, x.matches('missing.go'))`,
						},
					},
				},
			},
			vcx: &provider.TestProviderImp{
				WantAllChangedFiles: []string{"added.go", "deleted.go", "modified.go", "renamed.go"},
				WantAddedFiles:      []string{"added.go"},
				WantDeletedFiles:    []string{"deleted.go"},
				WantModifiedFiles:   []string{"modified.go"},
				WantRenamedFiles:    []string{"renamed.go"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, log := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			repo := tt.repository
			if repo == nil {
				repo = &v1alpha1.Repository{}
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			run := &params.Run{Clients: clients.Clients{}}
			if tt.event == nil {
				tt.event = &info.Event{}
			}
			tt.event.Request = &info.Request{Payload: []byte(tt.incomingPayload)}

			p := NewCustomParams(tt.event, repo, run, &kitesthelper.KinterfaceTest{GetSecretResult: tt.secretData}, nil, tt.vcx)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			p.eventEmitter = events.NewEventEmitter(stdata.Kube, logger)
			ret, _, err := p.GetParams(ctx)
			if tt.expectedError {
				assert.Assert(t, err != nil, "expected error but got nil")
			} else {
				assert.NilError(t, err)
			}
			if len(tt.expected) > 0 {
				assert.DeepEqual(t, tt.expected, ret)
			}
			if tt.expectedLogSnippet != "" {
				logmsg := log.FilterMessageSnippet(tt.expectedLogSnippet).TakeAll()
				assert.Assert(t, len(logmsg) > 0, "log message filtered %s expected %s all logs: %+v", logmsg,
					tt.expectedLogSnippet, log.TakeAll())
			}
		})
	}
}
