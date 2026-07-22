package matcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	httptesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/http"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/env"
	"gotest.tools/v3/fs"
	k8scheme "k8s.io/client-go/kubernetes/scheme"
	rtesting "knative.dev/pkg/reconciler/testing"
)

const (
	testHubURL         = "https://mybelovedhub"
	testCatalogHubName = "tekton"
)

type revisionRecordingProvider struct {
	*provider.TestProviderImp
	fileInsideRepoRevision string
}

func (v *revisionRecordingProvider) GetFileInsideRepo(ctx context.Context, event *info.Event, file, revision string) (string, error) {
	v.fileInsideRepoRevision = revision
	return v.TestProviderImp.GetFileInsideRepo(ctx, event, file, revision)
}

func createArtifactHubResponse(t *testing.T, manifestContent string) string {
	t.Helper()
	response := map[string]interface{}{
		"data": map[string]interface{}{
			"manifestRaw": manifestContent,
		},
	}
	jsonBytes, err := json.Marshal(response)
	assert.NilError(t, err)
	return string(jsonBytes)
}

func TestMain(m *testing.M) {
	s := k8scheme.Scheme
	if err := tektonv1beta1.AddToScheme(s); err != nil {
		log.Fatalf("Unable to add route scheme: (%v)", err)
	}
	ret := m.Run()
	os.Exit(ret)
}

func readTDfile(t *testing.T, testname string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + testname + ".yaml")
	assert.NilError(t, err)
	return string(data)
}

func TestGrabTasksFromAnnotation(t *testing.T) {
	tests := []struct {
		annotations map[string]string
		expected    []string
		name        string
		wantErr     string
	}{
		{
			name: "single task",
			annotations: map[string]string{
				keys.Task: "[http://remote.task]",
			},
			expected: []string{"http://remote.task"},
		},
		{
			name: "wrong key",
			annotations: map[string]string{
				keys.Task:                            "[http://remote.task]",
				pipelinesascode.GroupName + "/taskA": "[http://other.task]", // That's wrong this would be skipped
			},
			expected: []string{"http://remote.task"},
		},
		{
			name: "multiple tasks",
			annotations: map[string]string{
				keys.Task:        "[http://remote.task]",
				keys.Task + "-1": "[http://other.task]",
			},
			expected: []string{"http://other.task", "http://remote.task"},
		},
		{
			name: "multiple tasks with random order",
			annotations: map[string]string{
				keys.Task:        "[http://remote.task]",
				keys.Task + "-5": "[http://other.task]",
			},
			expected: []string{"http://other.task", "http://remote.task"},
		},
		{
			name: "multiple tasks with only orders",
			annotations: map[string]string{
				keys.Task + "-5": "[http://remote.task]",
				keys.Task + "-1": "[http://other.task]",
			},
			expected: []string{"http://other.task", "http://remote.task"},
		},
		{
			name: "multiple tasks with one annotation",
			annotations: map[string]string{
				keys.Task + "-1": "[http://other.task, http://remote.task]",
			},
			expected: []string{"http://other.task", "http://remote.task"},
		},
		{
			name: "test-annotations-remote-http-bad-annotation",
			annotations: map[string]string{
				keys.Task: "[http://remote.task",
			},
			expected: nil,
			wantErr:  "annotations in pipeline are in wrong format: [http://remote.task",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := GrabTasksFromAnnotations(tt.annotations)

			for _, task := range output {
				// check if we have the task inside the expected list
				found := false
				for _, expectedTask := range tt.expected {
					if task == expectedTask {
						found = true
					}
				}
				assert.Assert(t, found, "We should have found the task %v in the expected list", task)
			}
			assert.Equal(t, len(tt.expected), len(output), "We should have the same number of tasks")

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr, "We should have get an error with %v but we didn't", tt.wantErr)
			}
		})
	}
}

func TestGrabPipelineFromAnnotation(t *testing.T) {
	tests := []struct {
		annotations map[string]string
		expected    string
		name        string
		wantErr     string
	}{
		{
			name: "single pipeline",
			annotations: map[string]string{
				keys.Pipeline: "[http://remote.task]",
			},
			expected: "http://remote.task",
		},
		{
			name: "sing pipeline and a wrong key",
			annotations: map[string]string{
				keys.Pipeline:                            "[http://remote.task]",
				pipelinesascode.GroupName + "/pipelineA": "[http://other.task]", // That's wrong this would be skipped
			},
			expected: "http://remote.task",
		},
		{
			name: "single pipeline with only wrong key",
			annotations: map[string]string{
				keys.Pipeline + "-1": "[http://other.task]",
			},
			expected: "",
		},
		{
			name: "multiple pipelines with one annotation",
			annotations: map[string]string{
				keys.Pipeline: "[http://other.task, http://remote.task]",
			},
			expected: "",
			wantErr:  "only one pipeline is allowed on remote resolution, we have received multiple of them: [http://other.task http://remote.task]",
		},
		{
			name: "test-annotations-remote-http-bad-annotation",
			annotations: map[string]string{
				keys.Pipeline: "[http://remote.task",
			},
			expected: "",
			wantErr:  "annotations in pipeline are in wrong format: [http://remote.task",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := GrabPipelineFromAnnotations(tt.annotations)
			assert.Equal(t, tt.expected, output)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr, "We should have get an error with %v but we didn't", tt.wantErr)
				return
			}
		})
	}
}

func makeTestHubCatalogs() *sync.Map {
	catalogs := &sync.Map{}
	catalogs.Store("default", settings.HubCatalog{Index: "default", URL: testHubURL, Name: testCatalogHubName})
	catalogs.Store("anotherHub", settings.HubCatalog{Index: "1", URL: testHubURL, Name: testCatalogHubName})
	catalogs.Store("artifactHub", settings.HubCatalog{Index: "2", URL: testHubURL, Name: testCatalogHubName})
	catalogs.Store("artifactHubDefault", settings.HubCatalog{Index: "3", URL: testHubURL, Name: "default"})
	return catalogs
}

func TestGetTaskFromAnnotationName(t *testing.T) {
	hubCatalogs := makeTestHubCatalogs()
	tests := []struct {
		task                   string
		filesInsideRepo        map[string]string
		gotTaskName            string
		name                   string
		remoteURLS             map[string]map[string]string
		repositoryRevision     string
		runevent               info.Event
		wantErr                string
		wantLog                string
		wantProviderRemoteTask bool
	}{
		{
			name: "test-annotations-error-remote-http-not-k8",
			task: "http://remote.task",
			remoteURLS: map[string]map[string]string{
				"http://remote.task": {
					"body": "",
					"code": "200",
				},
			},
			wantErr: "not found",
		},
		{
			name:                   "test-good-coming-from-provider",
			task:                   "http://provider/remote.task",
			wantProviderRemoteTask: true,
			wantErr:                "not found",
		},
		{
			name:                   "test-bad-coming-from-provider",
			task:                   "http://provider/remote.task",
			wantProviderRemoteTask: false,
			wantErr:                "error getting remote task",
		},
		{
			name: "test-annotations-remote-http",
			task: "http://remote.task",
			remoteURLS: map[string]map[string]string{
				"http://remote.task": {
					"body": readTDfile(t, "task-good"),
					"code": "200",
				},
			},
		},
		// TODO: to uncomment in the future when fixing the Valdiate bug issue
		// {
		// 	name: "invalid-remote-task",
		// 	annotations: map[string]string{
		// 		keys.Task: "[http://remote.task]",
		// 	},
		// 	gotTaskName: "task",
		// 	remoteURLS: map[string]map[string]string{
		// 		"http://remote.task": {
		// 			"body": readTDfile(t, "task-invalid"),
		// 			"code": "200",
		// 		},
		// 	},
		// 	wantErr: "cannot be validated properly",
		// },
		{
			name:        "test-annotations-remote-https",
			task:        "https://remote.task",
			gotTaskName: "task",
			remoteURLS: map[string]map[string]string{
				"https://remote.task": {
					"body": readTDfile(t, "task-good"),
					"code": "200",
				},
			},
		},
		{
			name: "bad/not a tasl",
			task: "http://remote.task",
			remoteURLS: map[string]map[string]string{
				"http://remote.task": {
					"body": readTDfile(t, "pipeline-good"),
					"code": "200",
				},
			},
			wantErr: "remote task from URI http://remote.task has not been recognized as a Tekton task",
		},
		{
			name:        "test-annotations-inside-repo",
			task:        "be/healthy",
			gotTaskName: "task",
			filesInsideRepo: map[string]string{
				"be/healthy": readTDfile(t, "task-good"),
			},
			repositoryRevision: "dc922f5ea0c57ef5fb1cbc0f3ea550dfe3b5786e",
			runevent: info.Event{
				SHA: "007",
			},
		},
		{
			name:    "test-annotations-remote-inside-file-not-found",
			task:    "pas/la",
			wantErr: "could not find",
			runevent: info.Event{
				SHA: "007",
			},
		},
		{
			name:    "test-annotations-remote-no-event-not-found-no-error",
			task:    "not/here",
			wantLog: "could not find remote file not/here",
			wantErr: "not found",
		},
		{
			name:    "test-annotations-unknown-hub",
			task:    "foo://bar",
			wantLog: "custom catalog foo is not found",
			wantErr: "remote task \"foo://bar\" not found",
		},
		{
			name:        "test-get-from-custom-hub",
			gotTaskName: "task",
			task:        "anotherHub://chmouzie",
			wantLog:     "successfully fetched task chmouzie from custom catalog Hub anotherHub on URL https://mybelovedhub",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/chmouzie", testHubURL, testCatalogHubName): {
					"body": createArtifactHubResponse(t, readTDfile(t, "task-good")),
					"code": "200",
				},
			},
		},
		{
			name:        "test-get-from-hub-latest",
			gotTaskName: "task",
			task:        "chmouzie",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/chmouzie", testHubURL, testCatalogHubName): {
					"body": createArtifactHubResponse(t, readTDfile(t, "task-good")),
					"code": "200",
				},
			},
		},
		{
			name:        "test-get-from-hub-specific-version",
			gotTaskName: "task",
			task:        "chmouzie:0.2",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/chmouzie/0.2", testHubURL, testCatalogHubName): {
					"body": createArtifactHubResponse(t, readTDfile(t, "task-good")),
					"code": "200",
				},
			},
		},
		{
			name:        "test-get-from-artifacthub-custom-hub",
			gotTaskName: "task",
			task:        "artifactHub://chmouzie",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/%s/chmouzie", testHubURL, testCatalogHubName): {
					"body": createArtifactHubResponse(t, readTDfile(t, "task-good")),
					"code": "200",
				},
			},
		},
		{
			name:        "test-get-from-artifacthub-latest",
			gotTaskName: "task",
			task:        "artifactHubDefault://chmouzie",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/tekton-catalog-tasks/chmouzie", testHubURL): {
					"body": createArtifactHubResponse(t, readTDfile(t, "task-good")),
					"code": "200",
				},
			},
		},
		{
			name:        "test-get-from-artifacthub-specific-version",
			gotTaskName: "task",
			task:        "artifactHubDefault://chmouzie:0.2",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-task/tekton-catalog-tasks/chmouzie/0.2", testHubURL): {
					"body": createArtifactHubResponse(t, readTDfile(t, "task-good")),
					"code": "200",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpTestClient := httptesthelper.MakeHTTPTestClient(tt.remoteURLS)
			observer, fakelog := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			cs := &params.Run{
				Clients: clients.Clients{
					HTTP: *httpTestClient,
					Log:  logger,
				},
				Info: info.Info{
					Pac: &info.PacOpts{
						Settings: settings.Settings{
							HubCatalogs: hubCatalogs,
						},
					},
				},
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			providerImp := &revisionRecordingProvider{
				TestProviderImp: &provider.TestProviderImp{
					FilesInsideRepo:        tt.filesInsideRepo,
					WantProviderRemoteTask: tt.wantProviderRemoteTask,
				},
			}
			rt := RemoteTasks{
				Run:                cs,
				Logger:             logger,
				ProviderInterface:  providerImp,
				Event:              &tt.runevent,
				RepositoryRevision: tt.repositoryRevision,
			}

			got, err := rt.GetTaskFromAnnotationName(ctx, tt.task)
			if tt.wantLog != "" {
				assert.Assert(t, len(fakelog.FilterMessageSnippet(tt.wantLog).TakeAll()) > 0, "could not find log message: got ", fakelog)
			}
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr, "We should have get an error with %v but we didn't", tt.wantErr)
				return
			}
			assert.NilError(t, err, "GetTaskFromAnnotations() error = %v, wantErr %v", err, tt.wantErr)
			assert.Assert(t, got != nil, "GetTaskFromAnnotations() error no tasks has been processed")

			if tt.gotTaskName != "" {
				assert.Equal(t, tt.gotTaskName, got.GetName())
			}
			if tt.repositoryRevision != "" {
				assert.Equal(t, tt.repositoryRevision, providerImp.fileInsideRepoRevision)
			}
		})
	}
}

func TestGetPipelineFromAnnotationName(t *testing.T) {
	hubCatalogs := makeTestHubCatalogs()
	tests := []struct {
		pipeline        string
		filesInsideRepo map[string]string
		gotPipelineName string
		name            string
		remoteURLS      map[string]map[string]string
		runevent        info.Event
		wantErr         string
		wantLog         string
	}{
		{
			name:            "good/fetching from remote http",
			gotPipelineName: "pipeline",
			pipeline:        "http://remote.pipeline",
			remoteURLS: map[string]map[string]string{
				"http://remote.pipeline": {
					"body": readTDfile(t, "pipeline-good"),
					"code": "200",
				},
			},
		},
		{
			name:            "good/fetching with bundle",
			gotPipelineName: "pipeline",
			pipeline:        "http://remote.pipeline",
			remoteURLS: map[string]map[string]string{
				"http://remote.pipeline": {
					"body": readTDfile(t, "pipeline-good-bundle"),
					"code": "200",
				},
			},
		},
		// TODO: to uncomment in the future when fixing the Valdiate bug issue
		// {
		// 	name: "invalid-pipeline-validaton-failed",
		// 	annotations: map[string]string{
		// 		keys.Pipeline: "[http://remote.pipeline]",
		// 	},
		// 	remoteURLS: map[string]map[string]string{
		// 		"http://remote.pipeline": {
		// 			"body": readTDfile(t, "pipeline-invalid-bundle"),
		// 			"code": "200",
		// 		},
		// 	},
		// 	wantErr: "remote pipeline from URI http://remote.pipeline with name pipeline-test1 cannot be validated:",
		// },
		// {
		// 	name: "invalid-remote-pipeline",
		// 	annotations: map[string]string{
		// 		keys.Pipeline: "[http://remote.pipeline]",
		// 	},
		// 	remoteURLS: map[string]map[string]string{
		// 		"http://remote.pipeline": {
		// 			"body": readTDfile(t, "pipeline-invalid"),
		// 			"code": "200",
		// 		},
		// 	},
		// 	wantErr: "emote pipeline from uri: http://remote.pipeline with name pipeline cannot be validated: expected at least one, got none:",
		// },
		{
			name:     "bad/error getting pipeline",
			pipeline: "http://remote.pipeline",
			remoteURLS: map[string]map[string]string{
				"http://remote.pipeline": {
					"code": "501",
				},
			},
			wantErr: "error getting remote pipeline",
		},
		{
			name:     "bad/not a pipeline",
			pipeline: "http://remote.pipeline",
			remoteURLS: map[string]map[string]string{
				"http://remote.pipeline": {
					"body": readTDfile(t, "task-good"),
					"code": "200",
				},
			},
			wantErr: "remote pipeline from URI http://remote.pipeline has not been recognized as a Tekton pipeline",
		},
		{
			name:     "bad/could not get remote",
			pipeline: "http://nowhere.pipeline",
			wantErr:  "error getting remote pipeline",
		},
		{
			name:     "bad/not found",
			pipeline: "http://remote.pipeline",
			remoteURLS: map[string]map[string]string{
				"http://remote.pipeline": {
					"body": "",
					"code": "200",
				},
			},
			wantErr: "not found",
		},
		{
			name:     "test-annotations-unknown-hub",
			pipeline: "foo://bar",
			wantLog:  "custom catalog foo is not found",
			wantErr:  "remote pipeline \"foo://bar\" not found",
		},
		{
			name:            "test-get-from-custom-hub",
			gotPipelineName: "pipeline",
			pipeline:        "anotherHub://chmouzie",
			wantLog:         "successfully fetched pipeline chmouzie from custom catalog Hub anotherHub on URL https://mybelovedhub",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/%s/chmouzie", testHubURL, testCatalogHubName): {
					"body": createArtifactHubResponse(t, readTDfile(t, "pipeline-good")),
					"code": "200",
				},
			},
		},
		{
			name:            "test-get-from-hub-latest",
			gotPipelineName: "pipeline",
			pipeline:        "chmouzie",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/%s/chmouzie", testHubURL, testCatalogHubName): {
					"body": createArtifactHubResponse(t, readTDfile(t, "pipeline-good")),
					"code": "200",
				},
			},
		},
		{
			name:            "test-get-from-hub-specific-version",
			gotPipelineName: "pipeline",
			pipeline:        "chmouzie:0.2",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/%s/chmouzie/0.2", testHubURL, testCatalogHubName): {
					"body": createArtifactHubResponse(t, readTDfile(t, "pipeline-good")),
					"code": "200",
				},
			},
		},
		{
			name:            "test-get-from-artifacthub-custom-hub",
			gotPipelineName: "pipeline",
			pipeline:        "artifactHub://chmouzie",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/%s/chmouzie", testHubURL, testCatalogHubName): {
					"body": createArtifactHubResponse(t, readTDfile(t, "pipeline-good")),
					"code": "200",
				},
			},
		},
		{
			name:            "test-get-from-artifacthub-latest",
			gotPipelineName: "pipeline",
			pipeline:        "artifactHubDefault://chmouzie",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/tekton-catalog-pipelines/chmouzie", testHubURL): {
					"body": createArtifactHubResponse(t, readTDfile(t, "pipeline-good")),
					"code": "200",
				},
			},
		},
		{
			name:            "test-get-from-artifacthub-specific-version",
			gotPipelineName: "pipeline",
			pipeline:        "artifactHubDefault://chmouzie:0.2",
			remoteURLS: map[string]map[string]string{
				fmt.Sprintf("%s/api/v1/packages/tekton-pipeline/tekton-catalog-pipelines/chmouzie/0.2", testHubURL): {
					"body": createArtifactHubResponse(t, readTDfile(t, "pipeline-good")),
					"code": "200",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			httpTestClient := httptesthelper.MakeHTTPTestClient(tt.remoteURLS)
			observer, fakelog := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()

			cs := &params.Run{
				Clients: clients.Clients{
					HTTP: *httpTestClient,
					Log:  logger,
				},
				Info: info.Info{
					Pac: &info.PacOpts{
						Settings: settings.Settings{
							HubCatalogs: hubCatalogs,
						},
					},
				},
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			rt := RemoteTasks{
				Run:    cs,
				Logger: logger,
				ProviderInterface: &provider.TestProviderImp{
					FilesInsideRepo: tt.filesInsideRepo,
				},
				Event: &tt.runevent,
			}

			got, err := rt.GetPipelineFromAnnotationName(ctx, tt.pipeline)

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr, "We should have get an error with %v but we didn't", tt.wantErr)
				return
			}
			if tt.wantLog != "" {
				assert.Assert(t, len(fakelog.FilterMessageSnippet(tt.wantLog).TakeAll()) > 0, "could not find log message: got ", fakelog)
			}
			assert.NilError(t, err)
			assert.Assert(t, got != nil, "GetPipelineFromAnnotations() error no pipelines has been processed")

			if tt.gotPipelineName != "" {
				assert.Equal(t, tt.gotPipelineName, got.GetName())
			}
		})
	}
}

func TestGetTaskFromLocalFS(t *testing.T) {
	content := "hellomoto"
	defer env.ChangeWorkingDir(t, fs.NewDir(t, "TestGetTaskFromLocalFS", fs.WithFile("task1", content)).Path())()
	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	taskContent, err := getFileFromLocalFS("task1", logger)
	assert.NilError(t, err)
	assert.Equal(t, content, taskContent)
}
