package gitlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/go-github/v85/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/opscomments"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	thelp "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitlab/test"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/logger"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestParsePayload(t *testing.T) {
	const (
		zeroSHA         = "0000000000000000000000000000000000000000"
		branchCreateSHA = "dc922f5ea0c57ef5fb1cbc0f3ea550dfe3b5786e"
		checkoutSHA     = "1111111111111111111111111111111111111111"
	)
	sample := thelp.TEvent{
		Username:          "foo",
		DefaultBranch:     "main",
		URL:               "https://foo.com",
		SHA:               "sha",
		SHAurl:            "https://url",
		SHAtitle:          "commit it",
		Headbranch:        "branch",
		Basebranch:        "main",
		UserID:            10,
		MRID:              1,
		TargetProjectID:   100,
		SourceProjectID:   200,
		PathWithNameSpace: "hello/this/is/me/ze/project",
	}
	multiCommitPayload := fmt.Sprintf(`{
    "user_username": %q,
    "project_id": %d,
    "user_id": %d,
    "ref": "refs/heads/main",
    "project": {
        "default_branch": %q,
        "web_url": %q,
        "path_with_namespace": %q
    },
    "commits": [
        {
            "id": "1111111111111111111111111111111111111111",
            "url": "https://gitlab.example/commit/11111111",
            "title": "first commit"
        },
        {
            "id": "2222222222222222222222222222222222222222",
            "url": "https://gitlab.example/commit/22222222",
            "title": "second commit"
        }
    ]
}`, sample.Username, sample.TargetProjectID, sample.UserID, sample.DefaultBranch, sample.URL, sample.PathWithNameSpace)
	type fields struct {
		targetProjectID int
		sourceProjectID int
		userID          int
	}
	type args struct {
		event   gitlab.EventType
		payload string
	}
	tests := []struct {
		name           string
		fields         fields
		args           args
		want           *info.Event
		wantKubeClient bool
		wantErrMsg     string
		wantClient     bool
		wantBranch     string
	}{
		{
			name: "bad payload",
			args: args{
				payload: "nono",
				event:   "none",
			},
			wantErrMsg: "unexpected event type: none",
		},
		{
			name: "event not supported",
			args: args{
				event:   gitlab.EventTypePipeline,
				payload: sample.MREventAsJSON("open", ""),
			},
			wantErrMsg: "json: cannot unmarshal object into Go struct field PipelineEventObjectAttributes.object_attributes.source of type string",
		},
		{
			name: "merge event",
			args: args{
				event:   gitlab.EventTypeMergeRequest,
				payload: sample.MREventAsJSON("open", ""),
			},
			want: &info.Event{
				EventType:     "Merge Request",
				TriggerTarget: "pull_request",
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
				SHATitle:      "commit it",
			},
		},
		{
			name: "merge event closed",
			args: args{
				event:   gitlab.EventTypeMergeRequest,
				payload: sample.MREventAsJSON("close", ""),
			},
			want: &info.Event{
				EventType:     "Merge Request",
				TriggerTarget: triggertype.PullRequestClosed,
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
			},
		},
		{
			name: "push event no commits",
			args: args{
				event:   gitlab.EventTypePush,
				payload: sample.PushEventAsJSON(false),
			},
			wantErrMsg: "no commits attached to this push event",
		},
		{
			name: "push event creates branch without commits",
			args: args{
				event: gitlab.EventTypePush,
				payload: sample.PushEventWithoutCommitsAsJSON(
					zeroSHA,
					branchCreateSHA,
					checkoutSHA,
					"refs/heads/release-0.1",
				),
			},
			want: &info.Event{
				EventType:                 "push",
				TriggerTarget:             triggertype.Push,
				Organization:              "hello/this/is/me/ze",
				Repository:                "project",
				SHA:                       branchCreateSHA,
				HeadBranch:                "refs/heads/release-0.1",
				BaseBranch:                "refs/heads/release-0.1",
				CommitMetadataIncomplete:  true,
				PipelineRunSourceRevision: branchCreateSHA,
			},
		},
		{
			name: "push event deletes branch without commits",
			args: args{
				event: gitlab.EventTypePush,
				payload: sample.PushEventWithoutCommitsAsJSON(
					branchCreateSHA,
					zeroSHA,
					"",
					"refs/heads/release-0.1",
				),
			},
			wantErrMsg: "no commits attached to this push event",
		},
		{
			name: "push event without commits is not branch creation when before is nonzero",
			args: args{
				event: gitlab.EventTypePush,
				payload: sample.PushEventWithoutCommitsAsJSON(
					checkoutSHA,
					branchCreateSHA,
					branchCreateSHA,
					"refs/heads/release-0.1",
				),
			},
			wantErrMsg: "no commits attached to this push event",
		},
		{
			name: "push event without commits rejects tag ref",
			args: args{
				event: gitlab.EventTypePush,
				payload: sample.PushEventWithoutCommitsAsJSON(
					zeroSHA,
					branchCreateSHA,
					branchCreateSHA,
					"refs/tags/v0.0.1",
				),
			},
			wantErrMsg: "no commits attached to this push event",
		},
		{
			name: "push event without commits rejects empty branch name",
			args: args{
				event: gitlab.EventTypePush,
				payload: sample.PushEventWithoutCommitsAsJSON(
					zeroSHA,
					branchCreateSHA,
					branchCreateSHA,
					"refs/heads/",
				),
			},
			wantErrMsg: "no commits attached to this push event",
		},
		{
			name: "push event without commits rejects invalid after SHA",
			args: args{
				event: gitlab.EventTypePush,
				payload: sample.PushEventWithoutCommitsAsJSON(
					zeroSHA,
					"not-a-commit",
					"not-a-commit",
					"refs/heads/release-0.1",
				),
			},
			wantErrMsg: "no commits attached to this push event",
		},
		{
			name: "push event",
			args: args{
				event:   gitlab.EventTypePush,
				payload: sample.PushEventAsJSON(true),
			},
			want: &info.Event{
				EventType:     "push",
				TriggerTarget: "push",
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
				SHA:           "sha",
				SHATitle:      "commit it",
				SHAURL:        "https://url",
			},
		},
		{
			name: "push event with multiple commits uses the last commit",
			args: args{
				event:   gitlab.EventTypePush,
				payload: multiCommitPayload,
			},
			want: &info.Event{
				EventType:     "push",
				TriggerTarget: triggertype.Push,
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
				SHA:           "2222222222222222222222222222222222222222",
				SHATitle:      "second commit",
				SHAURL:        "https://gitlab.example/commit/22222222",
			},
		},
		{
			name: "tag event",
			args: args{
				event:   gitlab.EventTypeTagPush,
				payload: sample.PushEventAsJSON(true),
			},
			want: &info.Event{
				EventType:     "Tag Push",
				TriggerTarget: "push",
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
			},
		},
		{
			name: "note event",
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.NoteEventAsJSON(""),
			},
			want: &info.Event{
				EventType:     opscomments.NoOpsCommentEventType.String(),
				TriggerTarget: "pull_request",
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
			},
		},
		{
			name: "note event test",
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.NoteEventAsJSON("/test dummy"),
			},
			want: &info.Event{
				EventType:     opscomments.TestSingleCommentEventType.String(),
				TriggerTarget: "pull_request",
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
				State:         info.State{TargetTestPipelineRun: "dummy"},
			},
		},
		{
			name: "note event cancel all",
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.NoteEventAsJSON("/cancel"),
			},
			want: &info.Event{
				EventType:     opscomments.CancelCommentAllEventType.String(),
				TriggerTarget: "pull_request",
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
			},
		},
		{
			name: "note event cancel a pr",
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.NoteEventAsJSON("/cancel dummy"),
			},
			want: &info.Event{
				EventType:     opscomments.CancelCommentSingleEventType.String(),
				TriggerTarget: "pull_request",
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
				State:         info.State{TargetCancelPipelineRun: "dummy"},
			},
		},
		{
			name: "bad/commit comment repository is nil",
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/test", "create", "null"),
			},
			wantErrMsg: "error parse_payload: the repository in event payload must not be nil",
		},
		{
			name:   "bad/commit comment wrong branch keyword",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/test brrranch:fix", "create", "{}"),
			},
			wantErrMsg:     "does not contain a branch or tag word",
			wantKubeClient: true,
			wantClient:     true,
		},
		{
			name:   "good/commit comment /test all pipelineruns",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/test", "create", "{}"),
			},
			want: &info.Event{
				EventType:     opscomments.TestAllCommentEventType.String(),
				TriggerTarget: triggertype.Push,
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
			},
			wantKubeClient: true,
			wantClient:     true,
		},
		{
			name:   "good/commit comment /test a single pipelinerun",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/test dummy", "create", "{}"),
			},
			want: &info.Event{
				EventType:     opscomments.TestSingleCommentEventType.String(),
				TriggerTarget: triggertype.Push,
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
				State:         info.State{TargetTestPipelineRun: "dummy"},
			},
			wantKubeClient: true,
			wantClient:     true,
		},
		{
			name:   "good/commit comment /retest all pipelineruns",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/retest", "create", "{}"),
			},
			want: &info.Event{
				EventType:     opscomments.RetestAllCommentEventType.String(),
				TriggerTarget: triggertype.Push,
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
			},
			wantKubeClient: true,
			wantClient:     true,
		},
		{
			name:   "good/commit comment /retest a single pipelinerun",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/retest dummy", "create", "{}"),
			},
			want: &info.Event{
				EventType:     opscomments.RetestSingleCommentEventType.String(),
				TriggerTarget: triggertype.Push,
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
				State:         info.State{TargetTestPipelineRun: "dummy"},
			},
			wantKubeClient: true,
			wantClient:     true,
		},
		{
			name:   "good/commit comment /cancel all pipelineruns",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/cancel", "create", "{}"),
			},
			want: &info.Event{
				EventType:     opscomments.CancelCommentAllEventType.String(),
				TriggerTarget: triggertype.Push,
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
			},
			wantKubeClient: true,
			wantClient:     true,
		},
		{
			name:   "good/commit comment /retest a single pipelinerun",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/retest dummy", "create", "{}"),
			},
			want: &info.Event{
				EventType:     opscomments.RetestSingleCommentEventType.String(),
				TriggerTarget: triggertype.Push,
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
				State:         info.State{TargetTestPipelineRun: "dummy"},
			},
			wantKubeClient: true,
			wantClient:     true,
		},
		{
			name:   "good/commit comment /test on a tag",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/test tag:v1.0.0", "create", "{}"),
			},
			want: &info.Event{
				EventType:     opscomments.TestSingleCommentEventType.String(),
				TriggerTarget: triggertype.Push,
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
				HeadBranch:    "refs/tags/v1.0.0",
				BaseBranch:    "refs/tags/v1.0.0",
			},
			wantKubeClient: true,
			wantClient:     true,
		},
		{
			name:   "good/commit comment /retest on a tag",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/retest tag:v1.0.0", "create", "{}"),
			},
			want: &info.Event{
				EventType:     opscomments.RetestSingleCommentEventType.String(),
				TriggerTarget: triggertype.Push,
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
				HeadBranch:    "refs/tags/v1.0.0",
				BaseBranch:    "refs/tags/v1.0.0",
			},
			wantKubeClient: true,
			wantClient:     true,
		},
		{
			name:   "good/commit comment /cancel on a tag",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/cancel tag:v1.0.0", "create", "{}"),
			},
			want: &info.Event{
				EventType:     opscomments.CancelCommentSingleEventType.String(),
				TriggerTarget: triggertype.Push,
				Organization:  "hello/this/is/me/ze",
				Repository:    "project",
				HeadBranch:    "refs/tags/v1.0.0",
				BaseBranch:    "refs/tags/v1.0.0",
			},
			wantKubeClient: true,
			wantClient:     true,
		},
		{
			name:   "bad/commit comment tag does not exist",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/test tag:nonexistent", "create", "{}"),
			},
			wantErrMsg:     "error getting tag nonexistent",
			wantKubeClient: true,
			wantClient:     true,
		},
		{
			name:   "bad/commit comment SHA does not match tag commit",
			fields: fields{sourceProjectID: 200},
			args: args{
				event:   gitlab.EventTypeNote,
				payload: sample.CommitNoteEventAsJSON("/test tag:v1.0.0-mismatch", "create", "{}"),
			},
			wantErrMsg:     "provided SHA sha is not the tagged commit for the tag v1.0.0-mismatch",
			wantKubeClient: true,
			wantClient:     true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			logger, _ := logger.GetLogger()
			run := &params.Run{
				Info: info.NewInfo(),
			}
			if tt.wantKubeClient {
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "fakeNs",
						Name:      "gitlab-webhook-config",
					},
					Data: map[string][]byte{
						"provider.token": []byte("glpat_124ABC"),
						"webhook.secret": []byte("shhhhhhit'ssecret"),
					},
				}

				repo := &v1alpha1.Repository{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "fakeNs",
						Name:      "repo",
					},
					Spec: v1alpha1.RepositorySpec{
						URL: "https://foo.com",
						GitProvider: &v1alpha1.GitProvider{
							Secret:        &v1alpha1.Secret{Name: "gitlab-webhook-config"},
							WebhookSecret: &v1alpha1.Secret{Name: "gitlab-webhook-config"},
						},
					},
				}
				run.Info.Kube.Namespace = "fakeNs"
				data := testclient.Data{
					Repositories: []*v1alpha1.Repository{repo},
					Secret:       []*corev1.Secret{secret},
				}
				stdata, _ := testclient.SeedTestData(t, ctx, data)
				run.Clients = clients.Clients{Kube: stdata.Kube, PipelineAsCode: stdata.PipelineAsCode}
			}
			v := &Provider{
				Token:           github.Ptr("tokeneuneu"),
				targetProjectID: int64(tt.fields.targetProjectID),
				sourceProjectID: int64(tt.fields.sourceProjectID),
				userID:          int64(tt.fields.userID),
				run:             run,
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						ApplicationName: settings.PACApplicationNameDefaultValue,
					},
				},
				eventEmitter: events.NewEventEmitter(run.Clients.Kube, logger),
				Logger:       logger,
			}
			if tt.wantClient {
				client, mux, tearDown := thelp.Setup(t)
				v.SetGitLabClient(client)
				branchName := "main"
				if tt.wantBranch != "" {
					branchName = tt.wantBranch
				}
				mux.HandleFunc(fmt.Sprintf("/projects/200/repository/branches/%s", branchName),
					func(rw http.ResponseWriter, _ *http.Request) {
						branch := &gitlab.Branch{Name: branchName, Commit: &gitlab.Commit{ID: "sha"}}
						bytes, _ := json.Marshal(branch)
						_, _ = rw.Write(bytes)
					})
				// Mock tag API for v1.0.0 (valid tag with matching SHA)
				mux.HandleFunc("/projects/200/repository/tags/v1.0.0",
					func(rw http.ResponseWriter, _ *http.Request) {
						tag := &gitlab.Tag{
							Name:   "v1.0.0",
							Commit: &gitlab.Commit{ID: "sha"},
						}
						bytes, _ := json.Marshal(tag)
						_, _ = rw.Write(bytes)
					})
				// Mock tag API for v1.0.0-mismatch (tag with non-matching SHA)
				mux.HandleFunc("/projects/200/repository/tags/v1.0.0-mismatch",
					func(rw http.ResponseWriter, _ *http.Request) {
						tag := &gitlab.Tag{
							Name:   "v1.0.0-mismatch",
							Commit: &gitlab.Commit{ID: "different-sha"},
						}
						bytes, _ := json.Marshal(tag)
						_, _ = rw.Write(bytes)
					})
				// Mock tag API for nonexistent (return 404)
				mux.HandleFunc("/projects/200/repository/tags/nonexistent",
					func(rw http.ResponseWriter, _ *http.Request) {
						rw.WriteHeader(http.StatusNotFound)
						_, _ = rw.Write([]byte(`{"message":"404 Tag Not Found"}`))
					})
				defer tearDown()
			}

			request := &http.Request{Header: map[string][]string{}}
			request.Header.Set("X-Gitlab-Event", string(tt.args.event))

			got, err := v.ParsePayload(ctx, run, request, tt.args.payload)
			if tt.wantErrMsg != "" {
				assert.ErrorContains(t, err, tt.wantErrMsg)
				return
			}
			assert.NilError(t, err)
			if tt.want != nil {
				assert.Assert(t, got.Event != nil)
				assert.Equal(t, tt.want.TriggerTarget, got.TriggerTarget)
				assert.Equal(t, tt.want.EventType, got.EventType)
				assert.Equal(t, tt.want.Organization, got.Organization)
				assert.Equal(t, tt.want.Repository, got.Repository)
				if tt.want.TargetTestPipelineRun != "" {
					assert.Equal(t, tt.want.TargetTestPipelineRun, got.TargetTestPipelineRun)
				}
				if tt.want.TargetCancelPipelineRun != "" {
					assert.Equal(t, tt.want.TargetCancelPipelineRun, got.TargetCancelPipelineRun)
				}
				if tt.want.HeadBranch != "" {
					assert.Equal(t, tt.want.HeadBranch, got.HeadBranch)
				}
				if tt.want.BaseBranch != "" {
					assert.Equal(t, tt.want.BaseBranch, got.BaseBranch)
				}
				if tt.want.SHA != "" {
					assert.Equal(t, tt.want.SHA, got.SHA)
				}
				if tt.want.EventType == "push" && tt.want.SHATitle != "" {
					assert.Equal(t, tt.want.SHATitle, got.SHATitle)
				}
				if tt.want.EventType == "push" && tt.want.SHAURL != "" {
					assert.Equal(t, tt.want.SHAURL, got.SHAURL)
				}
				assert.Equal(t, tt.want.CommitMetadataIncomplete, got.CommitMetadataIncomplete)
			}
		})
	}
}

func TestInitGitLabClientSkipsTokenAutoRotation(t *testing.T) {
	ctx, _ := rtesting.SetupFakeContext(t)
	log, _ := logger.GetLogger()
	client, mux, tearDown := thelp.Setup(t)
	defer tearDown()

	introspectionCalled := false
	mux.HandleFunc("/personal_access_tokens/self", func(rw http.ResponseWriter, _ *http.Request) {
		introspectionCalled = true
		fmt.Fprint(rw, `{"id": 1, "active": true, "expires_at": "2000-01-01"}`)
	})

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "fakeNs",
			Name:      "gitlab-webhook-config",
		},
		Data: map[string][]byte{
			"provider.token": []byte("glpat_124ABC"),
			"webhook.secret": []byte("shhhhhhit'ssecret"),
		},
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "fakeNs",
			Name:      "repo",
		},
		Spec: v1alpha1.RepositorySpec{
			URL: "https://foo.com",
			GitProvider: &v1alpha1.GitProvider{
				URL:           client.BaseURL().String(),
				Secret:        &v1alpha1.Secret{Name: "gitlab-webhook-config"},
				WebhookSecret: &v1alpha1.Secret{Name: "gitlab-webhook-config"},
			},
		},
	}
	run := &params.Run{
		Info: info.NewInfo(),
	}
	run.Info.Kube.Namespace = "fakeNs"
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Repositories: []*v1alpha1.Repository{repo},
		Secret:       []*corev1.Secret{secret},
	})
	run.Clients = clients.Clients{
		Kube:           stdata.Kube,
		PipelineAsCode: stdata.PipelineAsCode,
		Log:            log,
	}

	v := &Provider{
		run: run,
		pacInfo: &info.PacOpts{
			Settings: settings.Settings{
				ApplicationName: settings.PACApplicationNameDefaultValue,
			},
		},
		eventEmitter: events.NewEventEmitter(run.Clients.Kube, log),
		Logger:       log,
	}
	event := info.NewEvent()
	event.URL = "https://foo.com"
	event.Organization = "hello"
	event.Repository = "project"
	event.SourceProjectID = 200
	event.TargetProjectID = 200

	_, err := v.initGitLabClient(ctx, event)
	assert.NilError(t, err)
	assert.Assert(t, v.gitlabClient != nil, "gitlab client should be initialized")
	assert.Assert(t, !introspectionCalled, "token rotation must not run before webhook validation")
}
