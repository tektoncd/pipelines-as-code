package customparams

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rectesting "knative.dev/pkg/reconciler/testing"
)

func TestMakeStandardParamsFromEvent(t *testing.T) {
	tests := []struct {
		name    string
		event   *info.Event
		repo    *v1alpha1.Repository
		want    map[string]string
		wantVCX *testprovider.TestProviderImp
	}{
		{
			name: "basic event test",
			event: &info.Event{
				SHA:              "1234567890",
				Organization:     "Org",
				Repository:       "Repo",
				BaseBranch:       "main",
				HeadBranch:       "foo",
				EventType:        "pull_request",
				Sender:           "SENDER",
				URL:              "https://paris.com",
				HeadURL:          "https://india.com",
				TriggerComment:   "\n/test me\nHelp me obiwan kenobi\r\n\r\n\r\nTo test or not to test, is the question?\n\n\n",
				PullRequestLabel: []string{"bugs", "enhancements"},
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myname",
					Namespace: "myns",
				},
			},
			want: map[string]string{
				"event_type":          "pull_request",
				"repo_name":           "repo",
				"repo_owner":          "org",
				"repo_url":            "https://paris.com",
				"source_url":          "https://india.com",
				"revision":            "1234567890",
				"sender":              "sender",
				"source_branch":       "foo",
				"git_tag":             "",
				"target_branch":       "main",
				"target_namespace":    "myns",
				"trigger_comment":     `\n/test me\nHelp me obiwan kenobi\n\n\nTo test or not to test, is the question?\n\n\n`,
				"pull_request_labels": "bugs\\nenhancements",
				"release_body":        "",
			},
			wantVCX: &testprovider.TestProviderImp{
				WantAllChangedFiles: []string{"added.go", "deleted.go", "modified.go", "renamed.go"},
				WantAddedFiles:      []string{"added.go"},
				WantDeletedFiles:    []string{"deleted.go"},
				WantModifiedFiles:   []string{"modified.go"},
				WantRenamedFiles:    []string{"renamed.go"},
			},
		},
		{
			name: "event with different clone URL",
			event: &info.Event{
				SHA:              "1234567890",
				Organization:     "Org",
				Repository:       "Repo",
				BaseBranch:       "main",
				HeadBranch:       "foo",
				EventType:        "pull_request",
				Sender:           "SENDER",
				URL:              "https://paris.com",
				HeadURL:          "https://india.com",
				TriggerComment:   "/test me\nHelp me obiwan kenobi",
				PullRequestLabel: []string{"bugs", "enhancements"},
				CloneURL:         "https://blahblah",
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myname",
					Namespace: "myns",
				},
			},
			want: map[string]string{
				"event_type":          "pull_request",
				"repo_name":           "repo",
				"repo_owner":          "org",
				"repo_url":            "https://blahblah",
				"source_url":          "https://india.com",
				"revision":            "1234567890",
				"sender":              "sender",
				"source_branch":       "foo",
				"git_tag":             "",
				"target_branch":       "main",
				"target_namespace":    "myns",
				"trigger_comment":     "/test me\\nHelp me obiwan kenobi",
				"pull_request_labels": "bugs\\nenhancements",
				"release_body":        "",
			},
			wantVCX: &testprovider.TestProviderImp{
				WantAllChangedFiles: []string{"added.go", "deleted.go", "modified.go", "renamed.go"},
				WantAddedFiles:      []string{"added.go"},
				WantDeletedFiles:    []string{"deleted.go"},
				WantModifiedFiles:   []string{"modified.go"},
				WantRenamedFiles:    []string{"renamed.go"},
			},
		},
		{
			name: "git tag push test event",
			event: &info.Event{
				SHA:            "1234567890",
				Organization:   "Org",
				Repository:     "Repo",
				BaseBranch:     "refs/tags/v1.0",
				HeadBranch:     "refs/tags/v1.0",
				EventType:      "push",
				Sender:         "SENDER",
				URL:            "https://paris.com",
				HeadURL:        "https://india.com",
				TriggerComment: "/test me\nHelp me obiwan kenobi",
				CloneURL:       "https://blahblah",
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myname",
					Namespace: "myns",
				},
			},
			want: map[string]string{
				"event_type":          "push",
				"repo_name":           "repo",
				"repo_owner":          "org",
				"repo_url":            "https://blahblah",
				"source_url":          "https://india.com",
				"revision":            "1234567890",
				"sender":              "sender",
				"source_branch":       "refs/tags/v1.0",
				"git_tag":             "v1.0",
				"target_branch":       "refs/tags/v1.0",
				"target_namespace":    "myns",
				"trigger_comment":     "/test me\\nHelp me obiwan kenobi",
				"pull_request_labels": "",
				"release_body":        "",
			},
			wantVCX: &testprovider.TestProviderImp{
				WantAllChangedFiles: []string{"added.go", "deleted.go", "modified.go", "renamed.go"},
				WantAddedFiles:      []string{"added.go"},
				WantDeletedFiles:    []string{"deleted.go"},
				WantModifiedFiles:   []string{"modified.go"},
				WantRenamedFiles:    []string{"renamed.go"},
			},
		},
		{
			name: "release event with body",
			event: &info.Event{
				SHA:          "abc123",
				Organization: "Org",
				Repository:   "Repo",
				BaseBranch:   "refs/tags/v1.0.0",
				HeadBranch:   "refs/tags/v1.0.0",
				EventType:    "release",
				Sender:       "RELEASER",
				URL:          "https://github.com/org/repo",
				HeadURL:      "https://github.com/org/repo",
				ReleaseBody:  "## Changelog\n- Fixed bug #123\n- Added feature X",
			},
			repo: &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myname",
					Namespace: "myns",
				},
			},
			want: map[string]string{
				"event_type":          "release",
				"repo_name":           "repo",
				"repo_owner":          "org",
				"repo_url":            "https://github.com/org/repo",
				"source_url":          "https://github.com/org/repo",
				"revision":            "abc123",
				"sender":              "releaser",
				"source_branch":       "refs/tags/v1.0.0",
				"git_tag":             "v1.0.0",
				"target_branch":       "refs/tags/v1.0.0",
				"target_namespace":    "myns",
				"trigger_comment":     "",
				"pull_request_labels": "",
				"release_body":        "## Changelog\n- Fixed bug #123\n- Added feature X",
			},
			wantVCX: &testprovider.TestProviderImp{
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
			ctx, _ := rectesting.SetupFakeContext(t)
			p := NewCustomParams(tt.event, tt.repo, nil, nil, nil, tt.wantVCX)
			params, changedFiles := p.makeStandardParamsFromEvent(ctx)

			assert.DeepEqual(t, params, tt.want)
			assert.DeepEqual(t, changedFiles["all"], tt.wantVCX.WantAllChangedFiles)
			assert.DeepEqual(t, changedFiles["added"], tt.wantVCX.WantAddedFiles)
			assert.DeepEqual(t, changedFiles["deleted"], tt.wantVCX.WantDeletedFiles)
			assert.DeepEqual(t, changedFiles["modified"], tt.wantVCX.WantModifiedFiles)
			assert.DeepEqual(t, changedFiles["renamed"], tt.wantVCX.WantRenamedFiles)
		})
	}
}
