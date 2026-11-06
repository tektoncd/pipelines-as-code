package pipelineascode

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/events"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	prgroup "github.com/openshift-pipelines/pipelines-as-code/pkg/pipelinerungroup"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

const followUpTektonDir = `apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: build
  annotations:
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
---
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: deploy
  annotations:
    pipelinesascode.tekton.dev/on-pipelinerun: '["build"]'
    pipelinesascode.tekton.dev/on-cel-expression: |
      runs.build.status == "Succeeded"
spec:
  params:
    - name: revision
      value: "{{ revision }}"
    - name: secret
      value: "{{ git_auth_secret }}"
`

// followUpWithBrokenSiblingTektonDir holds a definition whose pipelineRef
// cannot be resolved next to the follow-up.
const followUpWithBrokenSiblingTektonDir = `apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: broken
spec:
  pipelineRef:
    name: not-in-the-directory
---
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: deploy
  annotations:
    pipelinesascode.tekton.dev/on-pipelinerun: '["broken"]'
    pipelinesascode.tekton.dev/on-cel-expression: |
      runs.broken.status == "Succeeded"
spec:
  params:
    - name: revision
      value: "{{ revision }}"
    - name: stage
      value: "{{ stage }}"
  pipelineSpec:
    tasks:
      - name: task
        taskSpec:
          steps:
            - name: task
              image: registry.access.redhat.com/ubi10/ubi-micro
              command: ["/bin/true"]
`

func TestResolveFollowUpDefinition(t *testing.T) {
	stageParams := []v1alpha1.Params{
		// reads the payload, which the watcher does not have
		{Name: "stage", Value: "review", Filter: `body.action == "opened"`},
	}
	tests := []struct {
		name           string
		tektonDir      string
		definitionName string
		repoParams     *[]v1alpha1.Params
		paramFilters   map[string]string
		remoteTasks    bool
		wantStage      string
		wantErr        string
	}{
		{
			name:           "a param selected by a payload filter at event time is templated from the recorded outcome",
			tektonDir:      followUpWithBrokenSiblingTektonDir,
			definitionName: "deploy",
			repoParams:     &stageParams,
			paramFilters:   map[string]string{"stage": `body.action == "opened"`},
			wantStage:      "review",
		},
		{
			name:           "remote task resolution only resolves the follow-up's definition",
			tektonDir:      followUpWithBrokenSiblingTektonDir,
			definitionName: "deploy",
			repoParams:     &stageParams,
			paramFilters:   map[string]string{"stage": `body.action == "opened"`},
			remoteTasks:    true,
			wantStage:      "review",
		},
		{
			name:           "the definition is fetched, templated and stamped with its git auth secret",
			tektonDir:      followUpTektonDir,
			definitionName: "deploy",
		},
		{
			name:           "a definition that is not in the directory is a permanent not-found",
			tektonDir:      followUpTektonDir,
			definitionName: "scan",
			wantErr:        "follow-up definition not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := rtesting.SetupFakeContext(t)
			logger := zap.NewNop().Sugar()
			stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{})
			run := &params.Run{Clients: clients.Clients{
				Kube:   stdata.Kube,
				Tekton: stdata.Pipeline,
				Log:    logger,
			}}
			if tt.remoteTasks {
				run.Info.Pac = &info.PacOpts{Settings: settings.Settings{RemoteTasks: true}}
			}
			event := &info.Event{
				SHA:          "mainsha",
				URL:          "https://gitea.example.com/owner/repo",
				Organization: "owner",
				Repository:   "repo",
				BaseBranch:   "main",
				HeadBranch:   "feature",
				Sender:       "sender",
			}
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "namespace"},
				Spec:       v1alpha1.RepositorySpec{URL: event.URL, Params: tt.repoParams},
			}
			vcx := &testprovider.TestProviderImp{TektonDirTemplate: tt.tektonDir}

			pr, err := ResolveFollowUpDefinition(ctx, run, nil,
				events.NewEventEmitter(stdata.Kube, logger), logger, vcx, event, repo, tt.definitionName, tt.paramFilters)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			assert.NilError(t, err)

			// remote task resolution turns the name into a generateName, like
			// at event time: the watcher sets the deterministic name afterwards
			assert.Equal(t, "deploy", prgroup.DefinitionName(pr))
			secretName := pr.GetAnnotations()[keys.GitAuthSecret]
			assert.Assert(t, secretName != "", "the git auth secret annotation is stamped")
			var revision, secret, stage string
			for _, param := range pr.Spec.Params {
				switch param.Name {
				case "revision":
					revision = param.Value.StringVal
				case "secret":
					secret = param.Value.StringVal
				case "stage":
					stage = param.Value.StringVal
				}
			}
			assert.Equal(t, "mainsha", revision, "the event variables are templated")
			if tt.wantStage != "" {
				assert.Equal(t, tt.wantStage, stage, "the custom param is templated")
				return
			}
			assert.Equal(t, secretName, secret, "the git auth secret placeholder is expanded")
		})
	}
}

func TestPipelineRunByDefinition(t *testing.T) {
	prs := []*tektonv1.PipelineRun{
		{ObjectMeta: metav1.ObjectMeta{Name: "build"}},
		{ObjectMeta: metav1.ObjectMeta{
			GenerateName: "deploy-",
			Annotations:  map[string]string{keys.OriginalPRName: "deploy"},
		}},
	}
	assert.Assert(t, pipelineRunByDefinition(prs, "build") == prs[0])
	assert.Assert(t, pipelineRunByDefinition(prs, "deploy") == prs[1])
	assert.Assert(t, pipelineRunByDefinition(prs, "scan") == nil)
}
