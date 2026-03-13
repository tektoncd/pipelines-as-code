package reconciler

import (
	"context"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildEventFromPipelineRun(t *testing.T) {
	event := &info.Event{
		EventType:         "push",
		BaseBranch:        "branch",
		SHA:               "sha",
		SHAURL:            "sha-url",
		SHATitle:          "sha-title",
		PullRequestNumber: 1234,
		Organization:      "url-org",
		Repository:        "repo",
		InstallationID:    12345678,
		GHEURL:            "http://ghe",
		SourceProjectID:   1234,
		TargetProjectID:   2345,
		CloneURL:          "https://url/clone",
	}
	tests := []struct {
		name        string
		pipelineRun *tektonv1.PipelineRun
		event       *info.Event
	}{
		{
			name:  "build event from pr",
			event: event,
			pipelineRun: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						keys.URLOrg:        "url-org",
						keys.URLRepository: "repo",
						keys.SHA:           "sha",
						keys.EventType:     "push",
						keys.State:         kubeinteraction.StateStarted,
						keys.PullRequest:   "1234",
					},
					Annotations: map[string]string{
						keys.ShaTitle: "sha-title",
						keys.ShaURL:   "sha-url",
						keys.CloneURL: "https://url/clone",

						// github
						keys.InstallationID: "12345678",
						keys.GHEURL:         "http://ghe",

						// gitlab
						keys.SourceProjectID: "1234",
						keys.TargetProjectID: "2345",
						keys.URLOrg:          "url-org",
						keys.URLRepository:   "repo",
						keys.SHA:             "sha",
						keys.EventType:       "push",
						keys.Branch:          "branch",
						keys.State:           kubeinteraction.StateStarted,
						keys.PullRequest:     "1234",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := buildEventFromPipelineRun(tt.pipelineRun)
			assert.Equal(t, event.InstallationID, tt.event.InstallationID)
			assert.Equal(t, event.GHEURL, tt.event.GHEURL)
			assert.Equal(t, event.SHA, tt.event.SHA)
			assert.Equal(t, event.SHATitle, tt.event.SHATitle)
			assert.Equal(t, event.SourceProjectID, tt.event.SourceProjectID)
			assert.Equal(t, event.TargetProjectID, tt.event.TargetProjectID)
			assert.Equal(t, event.PullRequestNumber, tt.event.PullRequestNumber)
			assert.Equal(t, event.CloneURL, tt.event.CloneURL)
		})
	}
}

func TestDetectProvider(t *testing.T) {
	observer, _ := zapobserver.New(zap.InfoLevel)
	fakelogger := zap.New(observer).Sugar()
	tests := []struct {
		name         string
		missTheLabel bool
		annotation   string
		errStr       string
	}{
		{
			name:       "known provider",
			annotation: "gitlab",
			errStr:     "",
		},
		{
			name:       "forgejo provider resolves to gitea",
			annotation: "forgejo",
			errStr:     "",
		},
		{
			name:       "unknown provider",
			annotation: "batman",
			errStr:     "failed to detect provider for pipelinerun: test : unknown provider",
		},
		{
			name:         "no label",
			missTheLabel: true,
			errStr:       "failed to detect git provider for pipleinerun test : git-provider label not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Labels: map[string]string{}},
			}

			if !tt.missTheLabel {
				pr = &tektonv1.PipelineRun{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test",
						Annotations: map[string]string{
							keys.GitProvider: tt.annotation,
						},
					},
				}
			}
			r := Reconciler{}
			_, _, err := r.detectProvider(context.Background(), fakelogger, pr)
			if tt.errStr == "" {
				assert.NilError(t, err)
				return
			}
			assert.Error(t, err, tt.errStr)
		})
	}
}
