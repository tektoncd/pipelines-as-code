package status

import (
	"testing"
	"unicode/utf8"

	"github.com/jonboulle/clockwork"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	paramclients "github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	testclient "github.com/openshift-pipelines/pipelines-as-code/pkg/test/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/test/kubernetestint"
	tektontest "github.com/openshift-pipelines/pipelines-as-code/pkg/test/tekton"
	"github.com/stretchr/testify/assert"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	assertv3 "gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	knativeapi "knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestCollectFailedTasksLogSnippet(t *testing.T) {
	clock := clockwork.NewFakeClock()

	tests := []struct {
		name, displayName string
		message, status   string
		conditionStatus   corev1.ConditionStatus
		wantFailure       int
		podOutput         string
		wantSnippet       string
		wantWarning       string
	}{
		{
			name:            "no failures",
			status:          "Success",
			conditionStatus: corev1.ConditionTrue,
			message:         "never gonna make you fail",
			wantFailure:     0,
		},
		{
			name:            "failure pod output",
			status:          "Failed",
			conditionStatus: corev1.ConditionFalse,
			message:         "i am gonna to make you fail",
			podOutput:       "hahah i am the devil of the pod",
			wantFailure:     1,
			displayName:     "A task",
		},
		{
			name:            "step failed",
			status:          "StepFailed",
			conditionStatus: corev1.ConditionFalse,
			message:         `"step-lint" exited with code 2: Error`,
			podOutput:       "the step went wrong",
			wantFailure:     1,
		},
		{
			name:            "step out of memory",
			status:          "StepOOM",
			conditionStatus: corev1.ConditionFalse,
			message:         `"step-build" exited because of OOMKilled`,
			podOutput:       "out of memory",
			wantFailure:     1,
		},
		{
			name:            "sidecar failed",
			status:          "SidecarFailed",
			conditionStatus: corev1.ConditionFalse,
			message:         "sidecar crashed",
			podOutput:       "sidecar logs",
			wantFailure:     1,
		},
		{
			name:            "sidecar could not be stopped",
			status:          tektonv1.TaskRunReasonStopSidecarFailed.String(),
			conditionStatus: corev1.ConditionFalse,
			message:         "sidecar could not be stopped",
			podOutput:       "stop sidecar logs",
			wantFailure:     1,
		},
		{
			name:            "result larger than the allowed limit",
			status:          tektonv1.TaskRunReasonResultLargerThanAllowedLimit.String(),
			conditionStatus: corev1.ConditionFalse,
			message:         "result is way too large",
			podOutput:       "task result logs",
			wantFailure:     1,
		},
		{
			name:            "pod evicted",
			status:          "PodEvicted",
			conditionStatus: corev1.ConditionFalse,
			message:         "pod was evicted",
			podOutput:       "evicted logs",
			wantFailure:     1,
		},
		{
			name:            "init container failed is reported without pod logs",
			status:          "InitContainerFailed",
			conditionStatus: corev1.ConditionFalse,
			message:         "init container prepare failed",
			wantFailure:     1,
		},
		{
			name:            "ignored failure is skipped",
			status:          tektonv1.TaskRunReasonFailureIgnored.String(),
			conditionStatus: corev1.ConditionFalse,
			message:         "we don't care about this one",
			wantFailure:     0,
		},
		{
			name:            "unknown failure reason is skipped and reported",
			status:          "ANewTektonFailureReason",
			conditionStatus: corev1.ConditionFalse,
			message:         "something new happened",
			wantFailure:     0,
			wantWarning:     "unknown taskrun failure reason",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exitcode := int32(0)
			if tt.wantFailure > 0 {
				exitcode = 1
			}

			pr := tektontest.MakePRCompletion(clock, "pipeline-newest", "ns", tektonv1.PipelineRunReasonSuccessful.String(), nil, make(map[string]string), 10)
			pr.Status.ChildReferences = []tektonv1.ChildStatusReference{
				{
					TypeMeta: runtime.TypeMeta{
						Kind: "TaskRun",
					},
					Name:             "task1",
					PipelineTaskName: "task1",
				},
			}

			taskStatus := tektonv1.TaskRunStatusFields{
				PodName:  "task1",
				TaskSpec: &tektonv1.TaskSpec{DisplayName: tt.displayName},
				Steps: []tektonv1.StepState{
					{
						Name: "step1",
						ContainerState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: exitcode,
							},
						},
					},
				},
			}

			tdata := testclient.Data{
				TaskRuns: []*tektonv1.TaskRun{
					tektontest.MakeTaskRunCompletion(clock, "task1", "ns", "pipeline-newest",
						map[string]string{}, taskStatus, knativeduckv1.Conditions{
							{
								Type:    knativeapi.ConditionSucceeded,
								Status:  tt.conditionStatus,
								Reason:  tt.status,
								Message: tt.message,
							},
						},
						10),
				},
			}
			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			observer, logCatcher := zapobserver.New(zap.WarnLevel)
			cs := &params.Run{Clients: paramclients.Clients{
				Tekton: stdata.Pipeline,
				Log:    zap.New(observer).Sugar(),
			}}
			intf := &kubernetestint.KinterfaceTest{}
			if tt.podOutput != "" {
				intf.GetPodLogsOutput = map[string]string{
					"task1": tt.podOutput,
				}
			}
			got := CollectFailedTasksLogSnippet(ctx, cs, intf, pr, 1)
			assert.Equal(t, tt.wantFailure, len(got))
			if tt.podOutput != "" {
				assert.Equal(t, tt.podOutput, got["task1"].LogSnippet)
			}
			if tt.wantSnippet != "" {
				assert.Equal(t, tt.wantSnippet, got["task1"].LogSnippet)
			}
			if tt.displayName != "" {
				assert.Equal(t, tt.displayName, got["task1"].DisplayName)
			}
			if tt.wantWarning != "" {
				assertv3.Assert(t, logCatcher.FilterMessageSnippet(tt.wantWarning).Len() > 0, "expected a warning matching %q", tt.wantWarning)
			} else {
				assert.Equal(t, 0, logCatcher.Len(), "no warning was expected")
			}
		})
	}
}

func TestCollectFailedTasksLogSnippetUTF8SafeTruncation(t *testing.T) {
	clock := clockwork.NewFakeClock()

	tests := []struct {
		name                string
		podOutput           string
		expectedTruncation  bool
		expectedLengthRunes int  // Expected rune count for non-truncated strings
		expectValidUTF8     bool // Should result in valid UTF-8
	}{
		{
			name:                "short ascii text",
			podOutput:           "Error: simple failure message",
			expectedTruncation:  false,
			expectedLengthRunes: 29,
			expectValidUTF8:     true,
		},
		{
			name:               "long ascii text over limit",
			podOutput:          string(make([]byte, maxErrorSnippetCharacterLimit+100)), // Fill with null bytes which are 1 byte each
			expectedTruncation: true,
			expectValidUTF8:    true,
		},
		{
			name:                "utf8 text under limit",
			podOutput:           "🚀 Error: deployment failed with émojis and spécial chars",
			expectedTruncation:  false,
			expectedLengthRunes: len([]rune("🚀 Error: deployment failed with émojis and spécial chars")),
			expectValidUTF8:     true,
		},
		{
			name:               "utf8 text over limit",
			podOutput:          "🚀 " + string(make([]rune, maxErrorSnippetCharacterLimit)), // Create string with unicode chars (will be >65535 bytes)
			expectedTruncation: true,
			expectValidUTF8:    true,
		},
		{
			name:               "mixed utf8 at boundary",
			podOutput:          string(make([]rune, maxErrorSnippetCharacterLimit+1)) + "🚀🔥💥",
			expectedTruncation: true,
			expectValidUTF8:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := tektontest.MakePRCompletion(clock, "pipeline-newest", "ns", tektonv1.PipelineRunReasonSuccessful.String(), nil, make(map[string]string), 10)
			pr.Status.ChildReferences = []tektonv1.ChildStatusReference{
				{
					TypeMeta: runtime.TypeMeta{
						Kind: "TaskRun",
					},
					Name:             "task1",
					PipelineTaskName: "task1",
				},
			}

			taskStatus := tektonv1.TaskRunStatusFields{
				PodName: "task1",
				Steps: []tektonv1.StepState{
					{
						Name: "step1",
						ContainerState: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								ExitCode: 1,
							},
						},
					},
				},
			}

			tdata := testclient.Data{
				TaskRuns: []*tektonv1.TaskRun{
					tektontest.MakeTaskRunCompletion(clock, "task1", "ns", "pipeline-newest",
						map[string]string{}, taskStatus, knativeduckv1.Conditions{
							{
								Type:    knativeapi.ConditionSucceeded,
								Status:  corev1.ConditionFalse,
								Reason:  "Failed",
								Message: "task failed",
							},
						},
						10),
				},
			}

			ctx, _ := rtesting.SetupFakeContext(t)
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			cs := &params.Run{Clients: paramclients.Clients{
				Tekton: stdata.Pipeline,
			}}

			intf := &kubernetestint.KinterfaceTest{
				GetPodLogsOutput: map[string]string{
					"task1": tt.podOutput,
				},
			}

			got := CollectFailedTasksLogSnippet(ctx, cs, intf, pr, 1)
			assert.Equal(t, 1, len(got))

			snippet := got["task1"].LogSnippet
			byteCount := len(snippet)
			runeCount := len([]rune(snippet))

			if tt.expectedTruncation {
				// Should be truncated to at most maxErrorSnippetCharacterLimit bytes
				if byteCount > maxErrorSnippetCharacterLimit {
					t.Errorf("Expected truncated string to be at most %d bytes, got %d",
						maxErrorSnippetCharacterLimit, byteCount)
				}

				// Verify the string is valid UTF-8 after truncation
				assert.True(t, utf8.ValidString(snippet), "Truncated string should be valid UTF-8")

				// Should be shorter than original (in bytes)
				assert.Less(t, byteCount, len(tt.podOutput),
					"Truncated string should be shorter than original")
			} else {
				// Should match expected length exactly (in runes for non-truncated)
				assert.Equal(t, tt.expectedLengthRunes, runeCount,
					"Expected string length %d runes, got %d", tt.expectedLengthRunes, runeCount)

				// Should match original (no truncation)
				assert.Equal(t, tt.podOutput, snippet, "String should not be truncated")
			}

			// Always verify valid UTF-8
			if tt.expectValidUTF8 {
				assert.True(t, utf8.ValidString(snippet), "String should be valid UTF-8")
			}
		})
	}
}

func TestGetStatusFromTaskStatusOrFromAsking(t *testing.T) {
	testNS := "test"
	tests := []struct {
		name               string
		pr                 *tektonv1.PipelineRun
		numStatus          int
		expectedLogSnippet string
		taskRuns           []*tektonv1.TaskRun
		displayNames       []string
	}{
		{
			name:         "get status with displayName",
			numStatus:    2,
			displayNames: []string{"Hello Moto", ""},
			pr: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNS,
				},
				Spec: tektonv1.PipelineRunSpec{
					PipelineSpec: &tektonv1.PipelineSpec{
						Tasks: []tektonv1.PipelineTask{
							{
								Name:        "hello",
								DisplayName: "Hello Moto",
							},
						},
					},
				},
				Status: tektonv1.PipelineRunStatus{
					PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
						ChildReferences: []tektonv1.ChildStatusReference{
							{
								TypeMeta: runtime.TypeMeta{
									Kind: "TaskRun",
								},
								Name:             "hello",
								PipelineTaskName: "hello",
							},
							{
								TypeMeta: runtime.TypeMeta{
									Kind: "TaskRun",
								},
								Name:             "yolo",
								PipelineTaskName: "yolo",
							},
						},
					},
				},
			},
			taskRuns: []*tektonv1.TaskRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hello",
						Namespace: testNS,
					},
					Status: tektonv1.TaskRunStatus{
						TaskRunStatusFields: tektonv1.TaskRunStatusFields{
							TaskSpec: &tektonv1.TaskSpec{},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "yolo",
						Namespace: testNS,
					},
					Status: tektonv1.TaskRunStatus{
						TaskRunStatusFields: tektonv1.TaskRunStatusFields{
							TaskSpec: &tektonv1.TaskSpec{},
						},
					},
				},
			},
		},
		{
			name:      "get status from child references post tektoncd/pipelines 0.44",
			numStatus: 2,
			pr: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNS,
				},
				Status: tektonv1.PipelineRunStatus{
					PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
						ChildReferences: []tektonv1.ChildStatusReference{
							{
								TypeMeta: runtime.TypeMeta{
									Kind: "TaskRun",
								},
								Name: "hello",
							},
							{
								TypeMeta: runtime.TypeMeta{
									Kind: "TaskRun",
								},
								Name: "yolo",
							},
						},
					},
				},
			},
			taskRuns: []*tektonv1.TaskRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hello",
						Namespace: testNS,
					},
					Status: tektonv1.TaskRunStatus{
						TaskRunStatusFields: tektonv1.TaskRunStatusFields{
							TaskSpec: &tektonv1.TaskSpec{},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "yolo",
						Namespace: testNS,
					},
					Status: tektonv1.TaskRunStatus{
						TaskRunStatusFields: tektonv1.TaskRunStatusFields{
							TaskSpec: &tektonv1.TaskSpec{},
						},
					},
				},
			},
		},
		{
			name: "error get status from child references post tektoncd/pipelines 0.44",
			pr: &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: testNS,
				},
				Status: tektonv1.PipelineRunStatus{
					PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
						ChildReferences: []tektonv1.ChildStatusReference{
							{
								Name: "hello",
							},
							{
								Name: "yolo",
							},
						},
					},
				},
			},
			expectedLogSnippet: "cannot get taskrun status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, obslog := zapobserver.New(zap.InfoLevel)
			logger := zap.New(observer).Sugar()
			ctx, _ := rtesting.SetupFakeContext(t)
			run := params.New()

			tdata := testclient.Data{
				Namespaces: []*corev1.Namespace{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: testNS,
						},
					},
				},
				TaskRuns: tt.taskRuns,
			}
			stdata, _ := testclient.SeedTestData(t, ctx, tdata)
			run.Clients = paramclients.Clients{
				Kube:   stdata.Kube,
				Tekton: stdata.Pipeline,
				Log:    logger,
			}
			statuses := GetStatusFromTaskStatusOrFromAsking(ctx, tt.pr, run)
			assert.Equal(t, len(statuses), tt.numStatus)
			displayNames := []string{}
			if tt.displayNames != nil {
				for _, prtrs := range statuses {
					displayNames = append(displayNames, prtrs.Status.TaskSpec.DisplayName)
				}
				assert.ElementsMatch(t, tt.displayNames, displayNames)
			}
			if tt.expectedLogSnippet != "" {
				logmsg := obslog.FilterMessageSnippet(tt.expectedLogSnippet).TakeAll()
				assertv3.Assert(t, len(logmsg) > 0, "log messages", logmsg, tt.expectedLogSnippet)
			}
		})
	}
}
