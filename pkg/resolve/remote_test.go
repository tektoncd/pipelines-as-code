package resolve

import (
	"fmt"
	"os"
	"strings"
	"testing"

	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	httptesthelper "github.com/openshift-pipelines/pipelines-as-code/pkg/test/http"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	ttkn "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	rtesting "knative.dev/pkg/reconciler/testing"
	"sigs.k8s.io/yaml"
)

func TestRemote(t *testing.T) {
	randomPipelineRunName := "pipelinerun-abc"
	remotePipelineName := "remote-pipeline"
	remotePipelineURL := "http://remote/" + remotePipelineName

	taskFromPipelineRunName := "task-from-pipelinerun"
	taskFromPipelineRunURL := "http://remote/" + taskFromPipelineRunName

	remoteTaskName := "remote-task"
	remoteTaskURL := "http://remote/" + remoteTaskName
	taskFromPipelineSpec := tektonv1.TaskSpec{
		Steps: []tektonv1.Step{
			{
				Name:    "step1",
				Image:   "scratch",
				Command: []string{"true"},
			},
		},
	}
	taskFromPipelineRunSpec := tektonv1.TaskSpec{
		Steps: []tektonv1.Step{
			{
				Name:    "frompipelinerun",
				Image:   "scratch",
				Command: []string{"false"},
			},
		},
	}
	pipelineTaskEmbedded := []tektonv1.PipelineTask{
		{
			Name: "embedded",
			TaskSpec: &tektonv1.EmbeddedTask{
				TaskSpec: taskFromPipelineSpec,
			},
		},
	}
	pipelinewithTaskEmbedded := ttkn.MakePipeline(remotePipelineName, pipelineTaskEmbedded, nil)
	pipelinewithTaskEmbeddedB, err := yaml.Marshal(pipelinewithTaskEmbedded)
	assert.NilError(t, err)

	pipelineTaskRef := []tektonv1.PipelineTask{
		{
			Name: remoteTaskName,
			TaskRef: &tektonv1.TaskRef{
				Name: remoteTaskName,
			},
		},
	}
	pipelinewithTaskRef := ttkn.MakePipeline(remotePipelineName, pipelineTaskRef, map[string]string{
		apipac.Task: remoteTaskURL,
	})
	pipelinewithTaskRefYamlB, err := yaml.Marshal(pipelinewithTaskRef)
	assert.NilError(t, err)

	pipelineRelativeTaskRef := []tektonv1.PipelineTask{
		{
			Name: remoteTaskName + "-a",
			TaskRef: &tektonv1.TaskRef{
				Name: remoteTaskName + "-a",
			},
		},
		{
			Name: remoteTaskName + "-b",
			TaskRef: &tektonv1.TaskRef{
				Name: remoteTaskName + "-b",
			},
		},
		{
			Name: remoteTaskName + "-c",
			TaskRef: &tektonv1.TaskRef{
				Name: remoteTaskName + "-c",
			},
		},
		{
			Name: remoteTaskName + "-d",
			TaskRef: &tektonv1.TaskRef{
				Name: remoteTaskName + "-d",
			},
		},
	}
	pipelineWithRelativeTaskRef := ttkn.MakePipeline(remotePipelineName, pipelineRelativeTaskRef[:3], map[string]string{
		apipac.Task:        "./" + remoteTaskName + "-a",
		apipac.Task + "-1": "../" + remoteTaskName + "-b",
		apipac.Task + "-2": "../../../../" + remoteTaskName + "-c",
	})

	pipelineWithRelativeTaskRefYamlB, err := yaml.Marshal(pipelineWithRelativeTaskRef)
	assert.NilError(t, err)

	pipelineWithRelativeTaskRef1 := ttkn.MakePipeline(remotePipelineName, pipelineRelativeTaskRef[1:], map[string]string{
		apipac.Task:        remoteTaskName + "-b",
		apipac.Task + "-1": "utils/" + remoteTaskName + "-c",
		apipac.Task + "-2": " " + remoteTaskName + "-d",
	})

	pipelineWithRelativeTaskRefYamlB1, err := yaml.Marshal(pipelineWithRelativeTaskRef1)
	assert.NilError(t, err)

	singleRelativeTaskBa, err := ttkn.MakeTaskB(remoteTaskName+"-a", taskFromPipelineSpec)
	assert.NilError(t, err)

	singleRelativeTaskBb, err := ttkn.MakeTaskB(remoteTaskName+"-b", taskFromPipelineSpec)
	assert.NilError(t, err)

	singleRelativeTaskBc, err := ttkn.MakeTaskB(remoteTaskName+"-c", taskFromPipelineSpec)
	assert.NilError(t, err)

	singleRelativeTaskBd, err := ttkn.MakeTaskB(remoteTaskName+"-d", taskFromPipelineSpec)
	assert.NilError(t, err)

	singleTask := ttkn.MakeTask(remoteTaskName, taskFromPipelineSpec)
	singleTaskB, err := yaml.Marshal(singleTask)
	assert.NilError(t, err)

	taskFromPipelineRun := ttkn.MakeTask(remoteTaskName, taskFromPipelineRunSpec)
	taskFromPipelineRunB, err := yaml.Marshal(taskFromPipelineRun)
	assert.NilError(t, err)

	tests := []struct {
		name                 string
		pipelineruns         []*tektonv1.PipelineRun
		tasks                []*tektonv1.Task
		pipelines            []*tektonv1.Pipeline
		wantErrSnippet       string
		remoteURLS           map[string]map[string]string
		expectedLogsSnippets []string
		expectedTaskSpec     tektonv1.TaskSpec
		expectedPipelineRun  []string
		noPipelineRun        bool
	}{
		{
			name: "remote pipeline with remote task from pipeline",
			pipelineruns: []*tektonv1.PipelineRun{
				ttkn.MakePR(randomPipelineRunName, map[string]string{
					apipac.Pipeline: remotePipelineURL,
				},
					tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{
							Name: "remote-pipeline",
						},
					},
				),
			},
			remoteURLS: map[string]map[string]string{
				"http://remote/embedpipeline": {
					"body": string(pipelinewithTaskEmbeddedB),
					"code": "200",
				},
				remotePipelineURL: {
					"body": string(pipelinewithTaskRefYamlB),
					"code": "200",
				},
				remoteTaskURL: {
					"body": string(singleTaskB),
					"code": "200",
				},
			},
			expectedTaskSpec: taskFromPipelineSpec,
			expectedLogsSnippets: []string{
				fmt.Sprintf("successfully fetched %s from remote HTTPS URL", remotePipelineURL),
				fmt.Sprintf("successfully fetched %s from remote HTTPS URL", remoteTaskURL),
			},
			expectedPipelineRun: []string{"remote-pipeline-with-remote-task-from-pipeline.yaml"},
		},
		{
			name: "remote pipelines with relative tasks",
			pipelineruns: []*tektonv1.PipelineRun{
				ttkn.MakePR(randomPipelineRunName, map[string]string{
					apipac.Pipeline: remotePipelineURL,
				},
					tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{
							Name: remotePipelineName,
						},
					},
				),
				ttkn.MakePR(randomPipelineRunName, map[string]string{
					apipac.Pipeline: remotePipelineURL + "-1",
				},
					tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{
							Name: remotePipelineName,
						},
					},
				),
			},
			remoteURLS: map[string]map[string]string{
				remotePipelineURL: {
					"body": string(pipelineWithRelativeTaskRefYamlB),
					"code": "200",
				},
				remotePipelineURL + "-1": {
					"body": string(pipelineWithRelativeTaskRefYamlB1),
					"code": "200",
				},
				remoteTaskURL + "-a": {
					"body": string(singleRelativeTaskBa),
					"code": "200",
				},
				remoteTaskURL + "-b": {
					"body": string(singleRelativeTaskBb),
					"code": "200",
				},
				remoteTaskURL + "-c": {
					"body": string(singleRelativeTaskBc),
					"code": "200",
				},
				"http://remote/utils/remote-task-c": {
					"body": string(singleRelativeTaskBc),
					"code": "200",
				},
				remoteTaskURL + "-d": {
					"body": string(singleRelativeTaskBd),
					"code": "200",
				},
			},
			expectedTaskSpec:     taskFromPipelineSpec,
			expectedLogsSnippets: []string{},
			expectedPipelineRun: []string{
				"remote-pipeline-with-relative-tasks.yaml",
				"remote-pipeline-with-relative-tasks-1.yaml",
			},
		},
		{
			name: "multiple pipelineruns sharing same remote pipeline with relative tasks, pipeline and tasks all resolve",
			pipelineruns: []*tektonv1.PipelineRun{
				ttkn.MakePR(randomPipelineRunName, map[string]string{
					apipac.Pipeline: remotePipelineURL,
				},
					tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{
							Name: remotePipelineName,
						},
					},
				),
				ttkn.MakePR(randomPipelineRunName+"-second", map[string]string{
					apipac.Pipeline: remotePipelineURL, // SAME URL as first PipelineRun
				},
					tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{
							Name: remotePipelineName,
						},
					},
				),
			},
			remoteURLS: map[string]map[string]string{
				remotePipelineURL: {
					"body": string(pipelineWithRelativeTaskRefYamlB),
					"code": "200",
				},
				remoteTaskURL + "-a": {
					"body": string(singleRelativeTaskBa),
					"code": "200",
				},
				remoteTaskURL + "-b": {
					"body": string(singleRelativeTaskBb),
					"code": "200",
				},
				remoteTaskURL + "-c": {
					"body": string(singleRelativeTaskBc),
					"code": "200",
				},
			},
			expectedTaskSpec:     taskFromPipelineSpec,
			expectedLogsSnippets: []string{},
			expectedPipelineRun: []string{
				"remote-pipeline-with-relative-tasks.yaml",
				"remote-pipeline-with-relative-tasks-same-pipeline.yaml",
			},
		},
		{
			name: "remote pipeline with remote task in pipeline overridden from pipelinerun",
			pipelineruns: []*tektonv1.PipelineRun{
				ttkn.MakePR(randomPipelineRunName, map[string]string{
					apipac.Pipeline: remotePipelineURL,
					apipac.Task:     taskFromPipelineRunURL,
				},
					tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{
							Name: "remote-pipeline",
						},
					},
				),
			},
			expectedTaskSpec: taskFromPipelineRunSpec,
			remoteURLS: map[string]map[string]string{
				remotePipelineURL: {
					"body": string(pipelinewithTaskRefYamlB),
					"code": "200",
				},
				remoteTaskURL: {
					"body": string(singleTaskB),
					"code": "200",
				},
				taskFromPipelineRunURL: {
					"body": string(taskFromPipelineRunB),
					"code": "200",
				},
			},
			expectedLogsSnippets: []string{
				fmt.Sprintf("successfully fetched %s from remote HTTPS URL", remotePipelineURL),
				fmt.Sprintf("successfully fetched %s from remote HTTPS URL", taskFromPipelineRunURL),
			},
			expectedPipelineRun: []string{"remote-pipeline-with-remote-task-from-pipelinerun.yaml"},
		},
		{
			name: "remote pipelinerun no annotations",
			pipelineruns: []*tektonv1.PipelineRun{
				ttkn.MakePR(randomPipelineRunName, map[string]string{},
					tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{
							Name: "remote-pipeline",
						},
					},
				),
			},
			noPipelineRun: true,
		},
		{
			name:           "error/remote pipelinerun is 404",
			wantErrSnippet: "error getting remote pipeline \"" + remotePipelineURL + "\"",
			pipelineruns: []*tektonv1.PipelineRun{
				ttkn.MakePR(randomPipelineRunName, map[string]string{
					apipac.Pipeline: remotePipelineURL,
				},
					tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{
							Name: "remote-pipeline",
						},
					},
				),
			},
		},
		{
			name: "skip fetching multiple tasks of the same name from pipelinerun annotations and pipeline annotation",
			pipelineruns: []*tektonv1.PipelineRun{
				ttkn.MakePR(randomPipelineRunName, map[string]string{
					apipac.Pipeline:    remotePipelineURL,
					apipac.Task:        remoteTaskURL,
					apipac.Task + "-1": remoteTaskURL,
				},
					tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{
							Name: "remote-pipeline",
						},
					},
				),
			},
			remoteURLS: map[string]map[string]string{
				remotePipelineURL: {
					"body": string(pipelinewithTaskRefYamlB),
					"code": "200",
				},
				remoteTaskURL: {
					"body": string(singleTaskB),
					"code": "200",
				},
			},
			expectedTaskSpec: taskFromPipelineSpec,
			expectedLogsSnippets: []string{
				fmt.Sprintf("successfully fetched %s from remote HTTPS URL", remotePipelineURL),
				fmt.Sprintf("successfully fetched %s from remote HTTPS URL", remoteTaskURL),
			},
			expectedPipelineRun: []string{"skip-fetching-multiple-tasks-of-the-same-name-from-pipelinerun-annotations-and-pipeline-annotation.yaml"},
		},
		{
			name: "skip fetching multiple tasks of the same name from pipelinerun annotations and tektondir",
			pipelineruns: []*tektonv1.PipelineRun{
				ttkn.MakePR(randomPipelineRunName, map[string]string{
					apipac.Pipeline: remotePipelineURL,
					apipac.Task:     remoteTaskURL,
				},
					tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{
							Name: "remote-pipeline",
						},
					},
				),
			},
			tasks: []*tektonv1.Task{
				singleTask,
			},
			remoteURLS: map[string]map[string]string{
				remotePipelineURL: {
					"body": string(pipelinewithTaskRefYamlB),
					"code": "200",
				},
				remoteTaskURL: {
					"body": string(singleTaskB),
					"code": "200",
				},
			},
			expectedTaskSpec: taskFromPipelineSpec,
			expectedLogsSnippets: []string{
				fmt.Sprintf("successfully fetched %s from remote HTTPS URL", remotePipelineURL),
				fmt.Sprintf("successfully fetched %s from remote HTTPS URL", remoteTaskURL),
				fmt.Sprintf("skipping remote task %s as already fetched task %s for pipelinerun %s", remoteTaskURL, remoteTaskName, randomPipelineRunName),
				fmt.Sprintf("overriding task %s coming from .tekton directory by an annotation task for pipelinerun %s", remoteTaskName, randomPipelineRunName),
			},
			expectedPipelineRun: []string{"skip-fetching-multiple-tasks-of-the-same-name-from-pipelinerun-annotations-and-tektondir.yaml"},
		},
		{
			name: "skip fetching multiple pipelines of the same name from pipelinerun annotations and tektondir",
			pipelineruns: []*tektonv1.PipelineRun{
				ttkn.MakePR(randomPipelineRunName, map[string]string{
					apipac.Pipeline: remotePipelineURL,
					apipac.Task:     remoteTaskURL,
				},
					tektonv1.PipelineRunSpec{
						PipelineRef: &tektonv1.PipelineRef{
							Name: "remote-pipeline",
						},
					},
				),
			},
			pipelines: []*tektonv1.Pipeline{
				pipelinewithTaskEmbedded,
			},
			remoteURLS: map[string]map[string]string{
				remotePipelineURL: {
					"body": string(pipelinewithTaskRefYamlB),
					"code": "200",
				},
				remoteTaskURL: {
					"body": string(singleTaskB),
					"code": "200",
				},
			},
			expectedTaskSpec: taskFromPipelineSpec,
			expectedLogsSnippets: []string{
				fmt.Sprintf("successfully fetched %s from remote HTTPS URL", remotePipelineURL),
				fmt.Sprintf("successfully fetched %s from remote HTTPS URL", remoteTaskURL),
				fmt.Sprintf("skipping remote task %s as already fetched task %s for pipelinerun %s", remoteTaskURL, remoteTaskName, randomPipelineRunName),
			},
			expectedPipelineRun: []string{"skip-fetching-multiple-pipelines-of-the-same-name-from-pipelinerun-annotations-and-tektondir.yaml"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, log := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			tktype := TektonTypes{
				Pipelines:    tt.pipelines,
				Tasks:        tt.tasks,
				PipelineRuns: tt.pipelineruns,
			}

			tprovider := &testprovider.TestProviderImp{}
			httpTestClient := httptesthelper.MakeHTTPTestClient(tt.remoteURLS)
			rt := &matcher.RemoteTasks{
				ProviderInterface: tprovider,
				Logger:            logger,
				Run: &params.Run{
					Clients: clients.Clients{
						HTTP: *httpTestClient,
					},
				},
			}
			ret, err := resolveRemoteResources(ctx, rt, tktype, &Opts{RemoteTasks: true, GenerateName: true})
			if tt.wantErrSnippet != "" {
				assert.ErrorContains(t, err, tt.wantErrSnippet)
				return
			}
			assert.NilError(t, err)

			for k, snippet := range tt.expectedLogsSnippets {
				logmsg := log.AllUntimed()[k].Message
				assert.Assert(t, strings.Contains(logmsg, snippet), "\n on index: %d\n we want: %s\n we  got: %s", k, snippet, logmsg)
			}

			if tt.noPipelineRun {
				assert.Assert(t, len(ret) == 0, "not expecting any pipelinerun")
				return
			}
			for i, pr := range ret {
				if len(tt.expectedPipelineRun) < len(ret) {
					assert.NilError(t, fmt.Errorf("insufficient amount of expectedPipelineRuns was provided, got %d but want %d; or set noPipelineRun to true",
						len(tt.expectedPipelineRun), len(ret)))
				}
				expectedData, err := os.ReadFile("testdata/" + tt.expectedPipelineRun[i])
				assert.NilError(t, err)
				pipelineRun := &tektonv1.PipelineRun{}
				err = yaml.Unmarshal(expectedData, pipelineRun)
				assert.NilError(t, err)
				assert.DeepEqual(t, pipelineRun, pr)
			}
		})
	}
}

// Two PipelineRuns share the same remote pipeline. The first run provides
// a pipelinerun task annotation; the second uses the pipeline task.
// Without DeepCopy the first run's inlining mutates the cached pipeline,
// leaking the pipelinerun task into the second run.
func TestSharedRemotePipelineCacheNotMutated(t *testing.T) {
	taskName := "shared-task"

	pipelineTask := tektonv1.TaskSpec{
		Steps: []tektonv1.Step{
			{Name: "from-pipeline", Image: "alpine", Command: []string{"true"}},
		},
	}
	pipelinerunTask := tektonv1.TaskSpec{
		Steps: []tektonv1.Step{
			{Name: "from-pipelinerun", Image: "busybox", Command: []string{"false"}},
		},
	}

	// remote pipeline with a single TaskRef
	pipeline := ttkn.MakePipeline("shared-pipeline", []tektonv1.PipelineTask{
		{Name: taskName, TaskRef: &tektonv1.TaskRef{Name: taskName}},
	}, map[string]string{
		apipac.Task: "http://remote/shared-task",
	})
	pipelineB, err := yaml.Marshal(pipeline)
	assert.NilError(t, err)

	pipelineTaskB, err := ttkn.MakeTaskB(taskName, pipelineTask)
	assert.NilError(t, err)
	pipelinerunTaskB, err := ttkn.MakeTaskB(taskName, pipelinerunTask)
	assert.NilError(t, err)

	remoteURLS := map[string]map[string]string{
		"http://remote/shared-pipeline": {"body": string(pipelineB), "code": "200"},
		"http://remote/shared-task":     {"body": string(pipelineTaskB), "code": "200"},
		"http://remote/pr-task":         {"body": string(pipelinerunTaskB), "code": "200"},
	}

	// first run: overrides the task via pipelinerun annotation
	// second run: same pipeline, no override — should get the original task
	pipelineruns := []*tektonv1.PipelineRun{
		ttkn.MakePR("first-run", map[string]string{
			apipac.Pipeline: "http://remote/shared-pipeline",
			apipac.Task:     "http://remote/pr-task",
		}, tektonv1.PipelineRunSpec{
			PipelineRef: &tektonv1.PipelineRef{Name: "shared-pipeline"},
		}),
		ttkn.MakePR("second-run", map[string]string{
			apipac.Pipeline: "http://remote/shared-pipeline",
		}, tektonv1.PipelineRunSpec{
			PipelineRef: &tektonv1.PipelineRef{Name: "shared-pipeline"},
		}),
	}

	observer, _ := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	ctx, _ := rtesting.SetupFakeContext(t)
	httpTestClient := httptesthelper.MakeHTTPTestClient(remoteURLS)
	rt := &matcher.RemoteTasks{
		ProviderInterface: &testprovider.TestProviderImp{},
		Logger:            logger,
		Run: &params.Run{
			Clients: clients.Clients{HTTP: *httpTestClient},
		},
	}

	ret, err := resolveRemoteResources(ctx, rt, TektonTypes{PipelineRuns: pipelineruns}, &Opts{RemoteTasks: true, GenerateName: true})
	assert.NilError(t, err)
	assert.Equal(t, len(ret), 2)

	firstStep := ret[0].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0]
	assert.Equal(t, firstStep.Name, "from-pipelinerun")
	assert.Equal(t, firstStep.Image, "busybox")

	secondStep := ret[1].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0]
	assert.Equal(t, secondStep.Name, "from-pipeline")
	assert.Equal(t, secondStep.Image, "alpine")
}

func TestAssembleTaskFQDNs(t *testing.T) {
	tests := []struct {
		name        string
		pipelineURL string
		tasks       []string
		expected    []string
	}{
		{
			name:        "hub catalog URL returns tasks unchanged",
			pipelineURL: "suva://python-build-test-tag-suva:1.2",
			tasks:       []string{"task-a", "task-b"},
			expected:    []string{"task-a", "task-b"},
		},
		{
			name:        "custom hub without version returns tasks unchanged",
			pipelineURL: "anotherHub://chmouzie",
			tasks:       []string{"task-a"},
			expected:    []string{"task-a"},
		},
		{
			name:        "empty pipeline URL returns tasks unchanged",
			pipelineURL: "",
			tasks:       []string{"task-a", "task-b"},
			expected:    []string{"task-a", "task-b"},
		},
		{
			name:        "http URL resolves relative tasks",
			pipelineURL: "http://example.com/pipelines/my-pipeline.yaml",
			tasks:       []string{"../tasks/my-task.yaml"},
			expected:    []string{"http://example.com/tasks/my-task.yaml"},
		},
		{
			name:        "https URL resolves relative tasks",
			pipelineURL: "https://example.com/repo/pipelines/pipeline.yaml",
			tasks:       []string{"./task.yaml", "https://other.com/task2.yaml"},
			expected:    []string{"https://example.com/repo/pipelines/task.yaml", "https://other.com/task2.yaml"},
		},
		{
			name:        "uppercase HTTP URL resolves relative tasks",
			pipelineURL: "HTTP://example.com/pipelines/pipeline.yaml",
			tasks:       []string{"../tasks/my-task.yaml"},
			expected:    []string{"http://example.com/tasks/my-task.yaml"},
		},
		{
			name:        "mixed-case HTTPS URL resolves relative tasks",
			pipelineURL: "HtTpS://example.com/repo/pipeline.yaml",
			tasks:       []string{"./task.yaml"},
			expected:    []string{"https://example.com/repo/task.yaml"},
		},
		{
			name:        "repository file path URL resolves relative tasks",
			pipelineURL: "share/pipelines/build.yaml",
			tasks:       []string{"../tasks/t.yaml", "tasks/other-task.yaml"},
			expected:    []string{"share/tasks/t.yaml", "share/pipelines/tasks/other-task.yaml"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := assembleTaskFQDNs(tt.pipelineURL, tt.tasks)
			assert.NilError(t, err)
			assert.DeepEqual(t, tt.expected, result)
		})
	}
}
