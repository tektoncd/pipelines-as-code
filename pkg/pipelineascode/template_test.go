package pipelineascode

import (
	"fmt"
	"testing"

	"github.com/google/go-github/v84/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	kitesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestProcessTemplates(t *testing.T) {
	ns := "there"
	tests := []struct {
		name               string
		event              *info.Event
		template           string
		expected           string
		repository         *v1alpha1.Repository
		secretData         map[string]string
		expectedLogSnippet string
	}{
		{
			name: "test process templates",
			event: &info.Event{
				SHA:          "abcd",
				URL:          "http://chmouel.com",
				Organization: "owner",
				Repository:   "repository",
				HeadBranch:   "ohyeah",
				BaseBranch:   "ohno",
				Sender:       "apollo",
			},
			template: `{{ revision }} {{ repo_owner }} {{ repo_name }} {{ repo_url }} {{ source_branch }} {{ target_branch }} {{ sender }}`,
			expected: "abcd owner repository http://chmouel.com ohyeah ohno apollo",
		},
		{
			name: "strip refs head from branches",
			event: &info.Event{
				HeadBranch: "refs/heads/ohyeah",
				BaseBranch: "refs/heads/ohno",
				Sender:     "apollo",
			},
			template: `{{ source_branch }} {{ target_branch }}`,
			expected: "ohyeah ohno",
		},
		{
			name: "process pull request number",
			event: &info.Event{
				PullRequestNumber: 666,
			},
			template: `{{ pull_request_number }}`,
			expected: "666",
		},
		{
			name:     "no pull request no nothing",
			event:    &info.Event{},
			template: `{{ pull_request_number }}`,
			expected: `{{ pull_request_number }}`,
		},
		{
			name: "test process templates lowering owner and repository",
			event: &info.Event{
				Organization: "OWNER",
				Repository:   "REPOSITORY",
			},
			template: `{{ repo_owner }} {{ repo_name }}`,
			expected: "owner repository",
		},
		{
			name: "test process use cloneurl",
			event: &info.Event{
				CloneURL: "https://cloneurl",
				URL:      "http://chmouel.com",
			},
			template: `{{ repo_url }}`,
			expected: "https://cloneurl",
		},
		{
			name: "test git_tag variable",
			event: &info.Event{
				BaseBranch: "refs/tags/v1.0",
			},
			template: `{{ git_tag }}`,
			expected: "v1.0",
		},
		{
			name:     "replace target_namespace",
			template: `ns {{ target_namespace }}`,
			expected: fmt.Sprintf("ns %s", ns),
			repository: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: ns,
				},
			},
		},
		{
			name:     "params/basic",
			template: `I am {{ params }}`,
			expected: "I am batman",
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
			name:     "params/from secret",
			template: `ain't no sunshine when she {{ params }}`,
			expected: "ain't no sunshine when she gone",
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
			name:     "params/use last params when two values of the same name",
			template: `I am {{ params }}`,
			expected: "I am robin",
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
			name:     "params/use last params when two values of the same name",
			template: `I am {{ params }}`,
			expected: "I am robin",
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
			name:               "params/skip with no name",
			template:           `I am {{ params }}`,
			expected:           "I am {{ params }}",
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
			name:               "params/pick value when value and secret set",
			template:           `I am {{ params }}`,
			expected:           "I am batman",
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
			template: `I am {{ params }}`,
			expected: "I am batman",
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
			template: `I am {{ params }}`,
			expected: "I am batman",
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
			name:     "params/filter on body with bad filter",
			template: `I am {{ params }}`,
			expected: "I am {{ params }}",
			event:    &info.Event{EventType: "pull_request", Event: github.PullRequestEvent{Number: github.Ptr(42)}},
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
			name:     "params/unknown secret skipped",
			template: `No {{ customparams }}`,
			expected: "No {{ customparams }}",
			event:    &info.Event{EventType: "push"},
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name: "customparams",
							SecretRef: &v1alpha1.Secret{
								Name: "unkownsecret",
							},
						},
					},
				},
			},
		},
		{
			name:     "params/bad filter skipped",
			template: `I am {{ params }}`,
			expected: "I am {{ params }}",
			event:    &info.Event{EventType: "push"},
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
			name:     "params/bad payload skipped",
			template: `I am {{ params }}`,
			expected: "I am {{ params }}",
			event:    &info.Event{EventType: "push", Event: "BADADDADA"},
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
			template:           `I am {{ params }}`,
			expected:           "I am {{ params }}",
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
			name:               "params/two filters same name, match first",
			template:           `I am {{ params }}`,
			expected:           "I am batman",
			event:              &info.Event{EventType: "pull_request"},
			expectedLogSnippet: "skipping params name params, filter has already been matched previously",
			repository: &v1alpha1.Repository{
				Spec: v1alpha1.RepositorySpec{
					Params: &[]v1alpha1.Params{
						{
							Name:   "params",
							Value:  "batman",
							Filter: `pac.event_type == "pull_request"`,
						},
						{
							Name:   "params",
							Value:  "batman",
							Filter: `pac.event_type == "push"`,
						},
					},
				},
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
			p := NewPacs(tt.event, nil, run, &info.PacOpts{}, &kitesthelper.KinterfaceTest{GetSecretResult: tt.secretData}, nil, nil)
			p.logger = logger
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			p.eventEmitter = events.NewEventEmitter(stdata.Kube, logger)
			processed := p.makeTemplate(ctx, repo, tt.template)
			assert.Equal(t, tt.expected, processed)

			if tt.expectedLogSnippet != "" {
				logmsg := log.FilterMessageSnippet(tt.expectedLogSnippet).TakeAll()
				assert.Assert(t, len(logmsg) > 0, "log message filtered %s expected %s alllogs: %+v", logmsg,
					tt.expectedLogSnippet, log.TakeAll())
			}
		})
	}
}
