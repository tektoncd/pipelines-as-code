package opscomments

import (
	"strings"
	"testing"

	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testnewrepo "github.com/openshift-pipelines/pipelines-as-code/pkg/test/repository"
)

const (
	testComment   = "/test"
	retestComment = "/retest"
	cancelComment = "/cancel"
)

func TestLabelsBackwardCompat(t *testing.T) {
	testCases := []struct {
		name     string
		label    string
		expected string
	}{
		{
			name:     "OnCommentEventType",
			label:    OnCommentEventType.String(),
			expected: OnCommentEventType.String(),
		},
		{
			name:     "RetestAllCommentEventType",
			label:    RetestAllCommentEventType.String(),
			expected: triggertype.PullRequest.String(),
		},
		{
			name:     "OtherLabel",
			label:    "OtherLabel",
			expected: "OtherLabel",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			observer, _ := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)

			sampleRepo := testnewrepo.NewRepo(testnewrepo.RepoTestcreationOpts{
				Name:             "test-repo-already-installed",
				InstallNamespace: "namespace",
				URL:              "https://pac.test/already/installed",
			})
			tdata := testclient.Data{Repositories: []*v1alpha1.Repository{sampleRepo}}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			eventEmitter := events.NewEventEmitter(stdata.Kube, logger)
			result := EventTypeBackwardCompat(eventEmitter, sampleRepo, tc.label)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestGetNameFromFunction(t *testing.T) {
	tests := []struct {
		name        string
		comment     string
		commentType string
		expected    string
		wantErr     bool
	}{
		{
			name:        "get name from test comment",
			comment:     "/test prname",
			expected:    "prname",
			commentType: testComment,
		},
		{
			name:        "get name from test comment with args",
			comment:     "/test prname foo=bar hello=moto",
			expected:    "prname",
			commentType: testComment,
		},
		{
			name:        "key value arg is not a pipeline name",
			comment:     "/test custom1=value",
			expected:    "",
			commentType: testComment,
		},
		{
			name:        "key value args without pipeline name",
			comment:     "/test foo=bar hello=moto",
			expected:    "",
			commentType: testComment,
		},
		{
			name:        "get name from cancel comment",
			comment:     "/cancel prname",
			expected:    "prname",
			commentType: cancelComment,
		},
		{
			name:        "get name from retest comment",
			comment:     "/retest prname",
			expected:    "prname",
			commentType: retestComment,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getNameFromComment(tt.commentType, tt.comment)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsAnyOpsEventType(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		want      bool
	}{
		{
			name:      "TestSingleCommentEventType",
			eventType: TestSingleCommentEventType.String(),
			want:      true,
		},
		{
			name:      "TestAllCommentEventType",
			eventType: TestAllCommentEventType.String(),
			want:      true,
		},
		{
			name:      "RetestAllCommentEventType",
			eventType: RetestAllCommentEventType.String(),
			want:      true,
		},
		{
			name:      "RetestSingleCommentEventType",
			eventType: RetestSingleCommentEventType.String(),
			want:      true,
		},
		{
			name:      "CancelCommentSingleEventType",
			eventType: CancelCommentSingleEventType.String(),
			want:      true,
		},
		{
			name:      "CancelCommentAllEventType",
			eventType: CancelCommentAllEventType.String(),
			want:      true,
		},
		{
			name:      "OkToTestCommentEventType",
			eventType: OkToTestCommentEventType.String(),
			want:      true,
		},
		{
			name:      "OnCommentEventType",
			eventType: OnCommentEventType.String(),
			want:      true,
		},
		{
			name:      "NoOpsCommentEventType",
			eventType: NoOpsCommentEventType.String(),
			want:      false,
		},
		{
			name:      "Random string",
			eventType: "random string",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsAnyOpsEventType(tt.eventType)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestCommentEventTypeTest(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		prefix  string
		want    EventType
	}{
		{
			name:    "retest all",
			comment: "/retest",
			want:    RetestAllCommentEventType,
		},
		{
			name:    "retest single",
			comment: "/retest prname",
			want:    RetestSingleCommentEventType,
		},
		{
			name:    "test all",
			comment: "/test",
			want:    TestAllCommentEventType,
		},
		{
			name:    "test single",
			comment: "/test prname",
			want:    TestSingleCommentEventType,
		},
		{
			name:    "ok to test",
			comment: "/ok-to-test",
			want:    OkToTestCommentEventType,
		},
		{
			name:    "no comment event type",
			comment: "random comment",
			want:    NoOpsCommentEventType,
		},
		{
			name:    "cancel all",
			comment: "/cancel",
			want:    CancelCommentAllEventType,
		},
		{
			name:    "cancel single",
			comment: "/cancel prname",
			want:    CancelCommentSingleEventType,
		},
		{
			name:    "test all with prefix",
			comment: "/pac test",
			prefix:  "/pac ",
			want:    TestAllCommentEventType,
		},
		{
			name:    "test single with prefix",
			comment: "/pac test prname",
			prefix:  "/pac ",
			want:    TestSingleCommentEventType,
		},
		{
			name:    "retest all with prefix",
			comment: "/pac retest",
			prefix:  "/pac ",
			want:    RetestAllCommentEventType,
		},
		{
			name:    "retest single with prefix",
			comment: "/pac retest prname",
			prefix:  "/pac ",
			want:    RetestSingleCommentEventType,
		},
		{
			name:    "cancel all with prefix",
			comment: "/pac cancel",
			prefix:  "/pac ",
			want:    CancelCommentAllEventType,
		},
		{
			name:    "cancel single with prefix",
			comment: "/pac cancel prname",
			prefix:  "/pac ",
			want:    CancelCommentSingleEventType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prefix == "" {
				tt.prefix = "/"
			}
			got := CommentEventType(tt.comment, tt.prefix)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSetEventTypeTestPipelineRun(t *testing.T) {
	tests := []struct {
		name         string
		comment      string
		wantType     string
		wantTestPr   string
		wantCancelPr string
		prefix       string
	}{
		{
			name:     "no event type",
			comment:  "random comment",
			wantType: NoOpsCommentEventType.String(),
		},
		{
			name:       "retest single event type",
			comment:    "/retest prname",
			wantType:   RetestSingleCommentEventType.String(),
			wantTestPr: "prname",
		},
		{
			name:       "test single event type",
			comment:    "/test prname",
			wantType:   TestSingleCommentEventType.String(),
			wantTestPr: "prname",
		},
		{
			name:       "test with key value arg treated as test all",
			comment:    "/test custom1=value",
			wantType:   TestSingleCommentEventType.String(),
			wantTestPr: "",
		},
		{
			name:         "cancel single pr",
			comment:      "/cancel prname",
			wantType:     CancelCommentSingleEventType.String(),
			wantCancelPr: "prname",
		},
		{
			name:     "cancel all pr",
			comment:  "/cancel",
			wantType: CancelCommentAllEventType.String(),
		},
		{
			name:       "test single pr with prefix",
			comment:    "/pac test prname",
			wantType:   TestSingleCommentEventType.String(),
			wantTestPr: "prname",
			prefix:     "/pac ",
		},
		{
			name:     "test all with prefix",
			comment:  "/pac test",
			wantType: TestAllCommentEventType.String(),
			prefix:   "/pac ",
		},
		{
			name:       "retest single pr with prefix",
			comment:    "/pac retest prname",
			wantType:   RetestSingleCommentEventType.String(),
			wantTestPr: "prname",
			prefix:     "/pac ",
		},
		{
			name:     "retest all with prefix",
			comment:  "/pac retest",
			wantType: RetestAllCommentEventType.String(),
			prefix:   "/pac ",
		},
		{
			name:         "cancel single pr with prefix",
			comment:      "/pac cancel prname",
			wantType:     CancelCommentSingleEventType.String(),
			wantCancelPr: "prname",
			prefix:       "/pac ",
		},
		{
			name:     "cancel all with prefix",
			comment:  "/pac cancel",
			wantType: CancelCommentAllEventType.String(),
			prefix:   "/pac ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prefix == "" {
				tt.prefix = "/"
			}
			event := &info.Event{}
			SetEventTypeAndTargetPR(event, tt.comment, tt.prefix)
			assert.Equal(t, tt.wantType, event.EventType)
			assert.Equal(t, tt.wantTestPr, event.TargetTestPipelineRun)
		})
	}
}

func TestIsOkToTestComment(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		prefix  string
		want    bool
	}{
		{
			name:    "valid",
			comment: "/ok-to-test",
			want:    true,
		},
		{
			name:    "valid with some string before",
			comment: "/lgtm \n/ok-to-test",
			want:    true,
		},
		{
			name:    "valid with some string before and after",
			comment: "hi, trigger the ci \n/ok-to-test \n then report the status back",
			want:    true,
		},
		{
			name:    "valid comments",
			comment: "/lgtm \n/ok-to-test \n/approve",
			want:    true,
		},
		{
			name:    "invalid",
			comment: "/ok",
			want:    false,
		},
		{
			name:    "valid comment with sha",
			comment: "/ok-to-test 1234567",
			want:    true,
		},
		{
			name:    "valid comment with sha with prefix",
			comment: "/pac ok-to-test 1234567",
			prefix:  "/pac ",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prefix == "" {
				tt.prefix = "/"
			}
			got := IsOkToTestComment(tt.comment, tt.prefix)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsTestRetestComment(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		prefix  string
		want    EventType
	}{
		{
			name:    "valid retest",
			comment: "/retest",
			want:    RetestAllCommentEventType,
		},
		{
			name:    "valid test all",
			comment: "/test",
			want:    TestAllCommentEventType,
		},
		{
			name:    "valid retest with some string before",
			comment: "/lgtm \n/retest",
			want:    RetestAllCommentEventType,
		},
		{
			name:    "valid test with some string before",
			comment: "/lgtm \n/test",
			want:    TestAllCommentEventType,
		},
		{
			name:    "retest with some string before and after",
			comment: "hi, trigger the ci \n/retest \n then report the status back",
			want:    RetestAllCommentEventType,
		},
		{
			name:    "test valid with some string before and after",
			comment: "hi, trigger the ci \n/test \n then report the status back",
			want:    TestAllCommentEventType,
		},
		{
			name:    "test valid with some string before and after",
			comment: "hi, trigger the ci \n/test foobar\n then report the status back",
			want:    TestSingleCommentEventType,
		},
		{
			name:    "valid comments",
			comment: "/lgtm \n/retest \n/approve",
			want:    RetestAllCommentEventType,
		},
		{
			name:    "retest trigger single pr",
			comment: "/retest abc",
			want:    RetestSingleCommentEventType,
		},
		{
			name:    "test trigger single pr",
			comment: "/test abc",
			want:    TestSingleCommentEventType,
		},
		{
			name:    "invalid",
			comment: "test abc",
			want:    NoOpsCommentEventType,
		},
		{
			name:    "invalid",
			comment: "/ok",
			want:    NoOpsCommentEventType,
		},
		{
			name:    "valid retest with prefix",
			comment: "/pac retest",
			prefix:  "/pac ",
			want:    RetestAllCommentEventType,
		},
		{
			name:    "valid test with prefix",
			comment: "/pac test",
			prefix:  "/pac ",
			want:    TestAllCommentEventType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prefix == "" {
				tt.prefix = "/"
			}
			got := CommentEventType(tt.comment, tt.prefix)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetSHAFromOkToTestComment(t *testing.T) {
	tests := []struct {
		name    string
		comment string
		prefix  string
		want    string
	}{
		{
			name:    "no sha",
			comment: "/ok-to-test",
			want:    "",
		},
		{
			name:    "short sha",
			comment: "/ok-to-test 1234567",
			want:    "1234567",
		},
		{
			name:    "full sha",
			comment: "/ok-to-test 1234567890123456789012345678901234567890",
			want:    "1234567890123456789012345678901234567890",
		},
		{
			name:    "sha with surrounding text",
			comment: "lgtm\n/ok-to-test 1234567\napproved",
			want:    "1234567",
		},
		{
			name:    "no sha with prefix",
			comment: "/pac ok-to-test",
			prefix:  "/pac ",
			want:    "",
		},
		{
			name:    "sha with prefix",
			comment: "/pac ok-to-test 1234567",
			prefix:  "/pac ",
			want:    "1234567",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.prefix == "" {
				tt.prefix = "/"
			}
			got := GetSHAFromOkToTestComment(tt.comment, tt.prefix)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAnyOpsKubeLabelInSelector(t *testing.T) {
	assert.Assert(t, strings.Contains(AnyOpsKubeLabelInSelector(), RetestSingleCommentEventType.String()))
}
