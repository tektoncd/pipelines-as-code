package policy

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/events"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	"github.com/tektoncd/pipelines-as-code/pkg/params/triggertype"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	testprovider "github.com/tektoncd/pipelines-as-code/pkg/test/provider"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func newRepoWithPolicy(policy *v1alpha1.Policy) *v1alpha1.Repository {
	return &v1alpha1.Repository{
		Spec: v1alpha1.RepositorySpec{
			Settings: &v1alpha1.Settings{
				Policy: policy,
			},
		},
	}
}

func TestPolicyIsAllowed(t *testing.T) {
	senderName := "sender"
	eventWithSender := info.NewEvent()
	eventWithSender.Sender = senderName

	type fields struct {
		repository *v1alpha1.Repository
		event      *info.Event
	}
	type args struct {
		tType triggertype.Trigger
	}
	tests := []struct {
		name                 string
		fields               fields
		args                 args
		vcsReplyAllowed      bool
		want                 Result
		wantErr              bool
		wantReason           string
		allowedInOwnersFile  bool
		expectedLogsSnippets []string
	}{
		{
			name: "notset/not set",
			fields: fields{
				repository: nil,
				event:      nil,
			},
			args: args{
				tType: triggertype.Push,
			},
			want: ResultNotSet,
		},
		{
			name: "notset/unknown event type",
			fields: fields{
				repository: newRepoWithPolicy(&v1alpha1.Policy{PullRequest: []string{"pull_request"}}),
				event:      nil,
			},
			args: args{
				tType: "unknown",
			},
			want: ResultNotSet,
		},
		{
			name: "notset/push",
			fields: fields{
				repository: newRepoWithPolicy(&v1alpha1.Policy{PullRequest: []string{"pull_request"}}),
				event:      nil,
			},
			args: args{
				tType: triggertype.Push,
			},
			want: ResultNotSet,
		},
		{
			name: "allowed/allowing member for pull request",
			fields: fields{
				repository: newRepoWithPolicy(&v1alpha1.Policy{PullRequest: []string{"pull_request"}}),
				event:      info.NewEvent(),
			},
			args: args{
				tType: triggertype.PullRequest,
			},
			vcsReplyAllowed: true,
			want:            ResultAllowed,
		},
		{
			name: "allowed/from owners file",
			fields: fields{
				repository: newRepoWithPolicy(&v1alpha1.Policy{PullRequest: []string{"pull_request"}}),
				event:      eventWithSender,
			},
			args: args{
				tType: triggertype.PullRequest,
			},
			vcsReplyAllowed:     false,
			allowedInOwnersFile: true,
			want:                ResultAllowed,
			expectedLogsSnippets: []string{
				fmt.Sprintf("policy check: policy is set, sender %s not in the allowed policy but allowed via OWNERS file", senderName),
			},
		},
		{
			name: "allowed/member in team for ok-to-test",
			fields: fields{
				repository: newRepoWithPolicy(&v1alpha1.Policy{OkToTest: []string{"ok-to-test"}}),
				event:      info.NewEvent(),
			},
			args: args{
				tType: triggertype.OkToTest,
			},
			vcsReplyAllowed: true,
			want:            ResultAllowed,
		},
		{
			name: "allowed/retest same as ok-to-test",
			fields: fields{
				repository: newRepoWithPolicy(&v1alpha1.Policy{OkToTest: []string{"ok-to-test"}}),
				event:      info.NewEvent(),
			},
			args: args{
				tType: triggertype.Retest,
			},
			vcsReplyAllowed: true,
			want:            ResultAllowed,
		},
		{
			name:                 "disallowed/policy set with empty list",
			expectedLogsSnippets: []string{"policy set and empty with no groups"},
			fields: fields{
				repository: newRepoWithPolicy(&v1alpha1.Policy{PullRequest: []string{""}}),
				event:      eventWithSender,
			},
			args: args{
				tType: triggertype.PullRequest,
			},
			want: ResultDisallowed,
		},
		{
			name: "disallowed/member not in team for pull request",
			fields: fields{
				repository: newRepoWithPolicy(&v1alpha1.Policy{PullRequest: []string{"pull_request"}}),
				event:      info.NewEvent(),
			},
			args: args{
				tType: triggertype.PullRequest,
			},
			want:                 ResultDisallowed,
			wantErr:              true,
			expectedLogsSnippets: []string{"policy check: pull_request, policy disallowing"},
		},
		{
			name: "disallowed/member not in team for ok-to-test",
			fields: fields{
				repository: newRepoWithPolicy(&v1alpha1.Policy{OkToTest: []string{"nono"}}),
				event:      info.NewEvent(),
			},
			args: args{
				tType: triggertype.OkToTest,
			},
			want:                 ResultDisallowed,
			wantErr:              true,
			expectedLogsSnippets: []string{"policy check: ok-to-test, policy disallowing"},
		},
		{
			name: "disallowed/member not in team",
			fields: fields{
				repository: newRepoWithPolicy(&v1alpha1.Policy{OkToTest: []string{"ok-to-test"}}),
				event:      info.NewEvent(),
			},
			args: args{
				tType: triggertype.Retest,
			},
			want:                 ResultDisallowed,
			wantErr:              true,
			vcsReplyAllowed:      false,
			expectedLogsSnippets: []string{"policy check: retest, policy disallowing"},
		},
		{
			name: "disallowed/from owners file",
			fields: fields{
				repository: newRepoWithPolicy(&v1alpha1.Policy{PullRequest: []string{"pull_request"}}),
				event:      info.NewEvent(),
			},
			args: args{
				tType: triggertype.PullRequest,
			},
			vcsReplyAllowed:      false,
			allowedInOwnersFile:  false,
			want:                 ResultDisallowed,
			expectedLogsSnippets: []string{"policy check: pull_request, policy disallowing"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, log := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})

			vcx := &testprovider.TestProviderImp{
				PolicyDisallowing:   !tt.vcsReplyAllowed,
				AllowedInOwnersFile: tt.allowedInOwnersFile,
			}
			if tt.fields.event == nil {
				tt.fields.event = info.NewEvent()
			}
			p := &Policy{
				Repository:   tt.fields.repository,
				Event:        tt.fields.event,
				VCX:          vcx,
				Logger:       logger,
				EventEmitter: events.NewEventEmitter(stdata.Kube, logger),
			}
			got, reason := p.IsAllowed(ctx, tt.args.tType)
			switch got {
			case ResultAllowed:
				assert.Equal(t, tt.want, ResultAllowed)
			case ResultDisallowed:
				assert.Equal(t, tt.want, ResultDisallowed)
			case ResultNotSet:
				assert.Equal(t, tt.want, ResultNotSet)
			}
			assert.Equal(t, tt.wantReason, reason)

			for k, snippet := range tt.expectedLogsSnippets {
				logmsg := log.AllUntimed()[k].Message
				assert.Assert(t, strings.Contains(logmsg, snippet), "\n on index: %d\n we want: %s\n we  got: %s", k, snippet, logmsg)
			}
		})
	}
}
