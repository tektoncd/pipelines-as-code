package kubeinteraction

import (
	"context"
	"fmt"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAddLabelsAndAnnotations(t *testing.T) {
	event := info.NewEvent()
	event.Organization = "org"
	event.Repository = "repo"
	event.SHA = "sha"
	event.Sender = "sender"
	event.EventType = "pull_request"
	event.BaseBranch = "main"
	event.SHAURL = "https://url/sha"
	event.HeadBranch = "pr_branch"
	event.HeadURL = "https://url/pr"

	type args struct {
		event          *info.Event
		pipelineRun    *tektonv1.PipelineRun
		repo           *apipac.Repository
		controllerInfo *info.ControllerInfo
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "test label and annotation added to pr",
			args: args{
				event: event,
				pipelineRun: &tektonv1.PipelineRun{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{},
						Annotations: map[string]string{
							keys.CancelInProgress: "true",
						},
					},
				},
				repo: &apipac.Repository{
					ObjectMeta: metav1.ObjectMeta{
						Name: "repo",
					},
				},
				controllerInfo: &info.ControllerInfo{
					Name:             "controller",
					Configmap:        "configmap",
					Secret:           "secret",
					GlobalRepository: "repo",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paramsRun := &params.Run{
				Info: info.Info{
					Controller: tt.args.controllerInfo,
				},
			}
			err := AddLabelsAndAnnotations(context.Background(), tt.args.event, tt.args.pipelineRun, tt.args.repo, &info.ProviderConfig{}, paramsRun)
			assert.NilError(t, err)
			assert.Equal(t, tt.args.pipelineRun.Labels[keys.URLOrg], tt.args.event.Organization, "'%s' != %s",
				tt.args.pipelineRun.Labels[keys.URLOrg], tt.args.event.Organization)
			assert.Equal(t, tt.args.pipelineRun.Labels[keys.CancelInProgress], tt.args.pipelineRun.Annotations[keys.CancelInProgress], "'%s' != %s",
				tt.args.pipelineRun.Labels[keys.CancelInProgress], tt.args.pipelineRun.Annotations[keys.CancelInProgress])
			assert.Equal(t, tt.args.pipelineRun.Annotations[keys.URLOrg], tt.args.event.Organization, "'%s' != %s",
				tt.args.pipelineRun.Annotations[keys.URLOrg], tt.args.event.Organization)
			assert.Equal(t, tt.args.pipelineRun.Annotations[keys.ShaURL], tt.args.event.SHAURL)
			assert.Equal(t, tt.args.pipelineRun.Annotations[keys.SourceBranch], tt.args.event.HeadBranch)
			assert.Equal(t, tt.args.pipelineRun.Annotations[keys.SourceRepoURL], tt.args.event.HeadURL)
			assert.Equal(t, tt.args.pipelineRun.Annotations[keys.ControllerInfo],
				fmt.Sprintf(`{"name":"%s","configmap":"%s","secret":"%s", "gRepo": "%s"}`, tt.args.controllerInfo.Name, tt.args.controllerInfo.Configmap, tt.args.controllerInfo.Secret, tt.args.controllerInfo.GlobalRepository))
		})
	}
}
