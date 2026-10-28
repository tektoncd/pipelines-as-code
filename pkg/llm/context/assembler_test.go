package context

import (
	stdcontext "context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/kubeinteraction"
	"github.com/tektoncd/pipelines-as-code/pkg/params"
	paramclients "github.com/tektoncd/pipelines-as-code/pkg/params/clients"
	"github.com/tektoncd/pipelines-as-code/pkg/params/info"
	"github.com/tektoncd/pipelines-as-code/pkg/params/triggertype"
	testclient "github.com/tektoncd/pipelines-as-code/pkg/test/clients"
	kitesthelper "github.com/tektoncd/pipelines-as-code/pkg/test/kubernetestint"
	"github.com/tektoncd/pipelines-as-code/pkg/test/logger"
	tprovider "github.com/tektoncd/pipelines-as-code/pkg/test/provider"
	tektontest "github.com/tektoncd/pipelines-as-code/pkg/test/tekton"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"knative.dev/pkg/apis"
	knativeduckv1 "knative.dev/pkg/apis/duck/v1"
	rtesting "knative.dev/pkg/reconciler/testing"
)

type recordingPodLogsInterface struct {
	kitesthelper.KinterfaceTest
	tailLines int64
}

func (r *recordingPodLogsInterface) GetPodLogs(_ stdcontext.Context, _, pod, _ string, tailLines int64) (string, error) {
	r.tailLines = tailLines
	return r.GetPodLogsOutput[pod], nil
}

type errorPodLogsInterface struct {
	kitesthelper.KinterfaceTest
	err error
}

func (e *errorPodLogsInterface) GetPodLogs(_ stdcontext.Context, _, _, _ string, _ int64) (string, error) {
	return "", e.err
}

type buildErrorContentFailedTaskCase struct {
	name           string
	podOutput      string
	kinteract      kubeinteraction.Interface
	wantLogSnippet string
	setup          func(t *testing.T, tt *buildErrorContentFailedTaskCase)
}

type buildContainerLogsFailedTaskCase struct {
	name          string
	podOutput     string
	maxLines      int
	kinteract     kubeinteraction.Interface
	wantLogLines  []string
	wantMaxLength int
	setup         func(t *testing.T, tt *buildContainerLogsFailedTaskCase)
}

type buildContextBranchCase struct {
	name         string
	config       *v1alpha1.ContextConfig
	event        *info.Event
	wantCommit   bool
	wantErrors   bool
	wantLogs     bool
	wantLogLines []string
	wantMaxLines int
	setup        func(t *testing.T, tt *buildContextBranchCase) (*Assembler, stdcontext.Context, *tektonv1.PipelineRun)
}

func setupFailedPipelineContext(t *testing.T, kinteract kubeinteraction.Interface) (*Assembler, stdcontext.Context, *tektonv1.PipelineRun) {
	t.Helper()

	testLogger, _ := logger.GetLogger()
	clock := clockwork.NewFakeClock()
	namespace := "test-ns"
	pipelineRunName := "failed-pipeline"
	pipelineTaskName := "build"
	taskRunName := "build-run"
	podName := "build-pod"

	pr := tektontest.MakePRCompletion(clock, pipelineRunName, namespace,
		tektonv1.PipelineRunReasonFailed.String(), nil, map[string]string{}, 10)
	pr.Status.Conditions[0].Message = "pipeline failed"
	pr.Status.ChildReferences = []tektonv1.ChildStatusReference{
		{
			TypeMeta: runtime.TypeMeta{
				Kind: "TaskRun",
			},
			Name:             taskRunName,
			PipelineTaskName: pipelineTaskName,
		},
	}
	pr.Spec.PipelineSpec = &tektonv1.PipelineSpec{
		Tasks: []tektonv1.PipelineTask{
			{
				Name:        pipelineTaskName,
				DisplayName: "Build task",
			},
		},
	}

	taskStatus := tektonv1.TaskRunStatusFields{
		PodName:  podName,
		TaskSpec: &tektonv1.TaskSpec{},
		Steps: []tektonv1.StepState{
			{
				Name:      "step1",
				Container: "step1",
				ContainerState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						ExitCode: 1,
					},
				},
			},
		},
	}

	ctx, _ := rtesting.SetupFakeContext(t)
	stdata, _ := testclient.SeedTestData(t, ctx, testclient.Data{
		Namespaces: []*corev1.Namespace{
			{
				ObjectMeta: metav1.ObjectMeta{Name: namespace},
			},
		},
		TaskRuns: []*tektonv1.TaskRun{
			tektontest.MakeTaskRunCompletion(clock, taskRunName, namespace, "Failed",
				map[string]string{}, taskStatus, knativeduckv1.Conditions{
					{
						Type:    apis.ConditionSucceeded,
						Status:  corev1.ConditionFalse,
						Reason:  tektonv1.TaskRunReasonFailed.String(),
						Message: "task failed",
					},
				},
				10),
		},
	})
	run := &params.Run{Clients: paramclients.Clients{
		Kube:   stdata.Kube,
		Tekton: stdata.Pipeline,
		Log:    testLogger,
	}}

	return NewAssembler(run, kinteract, testLogger), ctx, pr
}

func TestBuildCELContext(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	kinteract := &kubeinteraction.Interaction{}
	assembler := NewAssembler(run, kinteract, logger)

	tests := []struct {
		name           string
		event          *info.Event
		expectedFields map[string]any
		checkFields    []string
	}{
		{
			name: "all basic event fields",
			event: &info.Event{
				EventType:     "pull_request",
				TriggerTarget: triggertype.PullRequest,
				SHA:           "abc123",
				SHATitle:      "feat: add new feature",
				BaseBranch:    "main",
				HeadBranch:    "feature-branch",
				DefaultBranch: "main",
				Organization:  "my-org",
				Repository:    "my-repo",
				URL:           "https://github.com/my-org/my-repo",
				SHAURL:        "https://github.com/my-org/my-repo/commit/abc123",
				BaseURL:       "https://github.com/my-org/my-repo",
				HeadURL:       "https://github.com/my-org/my-repo/tree/feature-branch",
				Sender:        "user123",
			},
			expectedFields: map[string]any{
				"event_type":         "pull_request",
				"trigger_target":     "pull_request",
				"sha":                "abc123",
				"sha_title":          "feat: add new feature",
				"base_branch":        "main",
				"head_branch":        "feature-branch",
				"default_branch":     "main",
				"organization":       "my-org",
				"repository":         "my-repo",
				"url":                "https://github.com/my-org/my-repo",
				"sha_url":            "https://github.com/my-org/my-repo/commit/abc123",
				"base_url":           "https://github.com/my-org/my-repo",
				"head_url":           "https://github.com/my-org/my-repo/tree/feature-branch",
				"sender":             "user123",
				"target_pipelinerun": "",
			},
			checkFields: []string{
				"event_type", "trigger_target", "sha", "sha_title",
				"base_branch", "head_branch", "default_branch",
				"organization", "repository", "url", "sha_url",
				"base_url", "head_url", "sender", "target_pipelinerun",
			},
		},
		{
			name: "pull request specific fields",
			event: &info.Event{
				EventType:         "pull_request",
				PullRequestNumber: 42,
				PullRequestTitle:  "Add new feature",
				PullRequestLabel:  []string{"enhancement", "needs-review"},
			},
			expectedFields: map[string]any{
				"pull_request_number": 42,
				"pull_request_title":  "Add new feature",
				"pull_request_labels": []string{"enhancement", "needs-review"},
			},
			checkFields: []string{
				"pull_request_number",
				"pull_request_title",
				"pull_request_labels",
			},
		},
		{
			name: "trigger comment field",
			event: &info.Event{
				TriggerComment: "/test",
			},
			expectedFields: map[string]any{
				"trigger_comment": "/test",
			},
			checkFields: []string{"trigger_comment"},
		},
		{
			name: "incoming webhook target pipelinerun",
			event: &info.Event{
				TargetPipelineRun: "my-pipeline-run",
			},
			expectedFields: map[string]any{
				"target_pipelinerun": "my-pipeline-run",
			},
			checkFields: []string{"target_pipelinerun"},
		},
		{
			name: "push event without PR fields",
			event: &info.Event{
				EventType:     "push",
				TriggerTarget: triggertype.Push,
				SHA:           "def456",
				BaseBranch:    "main",
				HeadBranch:    "main",
			},
			expectedFields: map[string]any{
				"event_type":     "push",
				"trigger_target": "push",
				"sha":            "def456",
				"base_branch":    "main",
				"head_branch":    "main",
			},
			checkFields: []string{
				"event_type",
				"trigger_target",
				"sha",
				"base_branch",
				"head_branch",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &tektonv1.PipelineRun{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pr",
					Namespace: "test-ns",
				},
			}
			repo := &v1alpha1.Repository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "test-ns",
				},
			}

			celContext, err := assembler.BuildCELContext(pr, tt.event, repo)
			assert.NilError(t, err)

			// Verify structure exists
			body, ok := celContext["body"].(map[string]any)
			assert.Assert(t, ok, "body should be a map")

			eventMap, ok := body["event"].(map[string]any)
			assert.Assert(t, ok, "body.event should be a map")

			// Check each expected field
			for _, field := range tt.checkFields {
				expectedValue, exists := tt.expectedFields[field]
				assert.Assert(t, exists, "expected field %s not found in test data", field)

				actualValue, exists := eventMap[field]
				assert.Assert(t, exists, "field %s should exist in event map", field)

				// For slices, use deep equal
				switch v := expectedValue.(type) {
				case []string:
					actualSlice, ok := actualValue.([]string)
					assert.Assert(t, ok, "field %s should be []string", field)
					assert.DeepEqual(t, actualSlice, v)
				default:
					assert.Equal(t, actualValue, expectedValue, "field %s has wrong value", field)
				}
			}
		})
	}
}

func TestBuildCELContextExcludedFields(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	kinteract := &kubeinteraction.Interaction{}
	assembler := NewAssembler(run, kinteract, logger)

	// Create event with fields that should be excluded
	event := &info.Event{
		EventType: "pull_request",
		Provider: &info.Provider{
			Token:         "secret-token",
			WebhookSecret: "webhook-secret",
			URL:           "https://api.github.com",
		},
		InstallationID:  12345,
		AccountID:       "bitbucket-account",
		GHEURL:          "https://ghe.example.com",
		CloneURL:        "https://bitbucket.example.com/scm/repo.git",
		SourceProjectID: 100,
		TargetProjectID: 200,
	}

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
		},
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-ns",
		},
	}

	celContext, err := assembler.BuildCELContext(pr, event, repo)
	assert.NilError(t, err)

	body, ok := celContext["body"].(map[string]any)
	assert.Assert(t, ok)

	eventMap, ok := body["event"].(map[string]any)
	assert.Assert(t, ok)

	// Verify excluded fields are NOT present
	excludedFields := []string{
		"provider",
		"installation_id",
		"account_id",
		"ghe_url",
		"clone_url",
		"source_project_id",
		"target_project_id",
		"request",
		"state",
	}

	for _, field := range excludedFields {
		_, exists := eventMap[field]
		assert.Assert(t, !exists, "field %s should be excluded from CEL context", field)
	}
}

func TestBuildCELContextNilEvent(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	kinteract := &kubeinteraction.Interaction{}
	assembler := NewAssembler(run, kinteract, logger)

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
		},
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-ns",
		},
	}

	celContext, err := assembler.BuildCELContext(pr, nil, repo)
	assert.NilError(t, err)

	body, ok := celContext["body"].(map[string]any)
	assert.Assert(t, ok)

	// Event should not exist in the map
	_, exists := body["event"]
	assert.Assert(t, !exists, "event should not exist when nil event is passed")
}

func TestBuildCELContextConditionalFields(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	kinteract := &kubeinteraction.Interaction{}
	assembler := NewAssembler(run, kinteract, logger)

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pr",
			Namespace: "test-ns",
		},
	}
	repo := &v1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-repo",
			Namespace: "test-ns",
		},
	}

	t.Run("without pull request fields", func(t *testing.T) {
		event := &info.Event{
			EventType:         "push",
			PullRequestNumber: 0, // No PR
		}

		celContext, err := assembler.BuildCELContext(pr, event, repo)
		assert.NilError(t, err)

		body, ok := celContext["body"].(map[string]any)
		assert.Assert(t, ok)
		eventMap, ok := body["event"].(map[string]any)
		assert.Assert(t, ok)
		// PR fields should not exist
		_, exists := eventMap["pull_request_number"]
		assert.Assert(t, !exists, "pull_request_number should not exist for push events")

		_, exists = eventMap["pull_request_title"]
		assert.Assert(t, !exists, "pull_request_title should not exist for push events")

		_, exists = eventMap["pull_request_labels"]
		assert.Assert(t, !exists, "pull_request_labels should not exist for push events")
	})

	t.Run("without trigger comment", func(t *testing.T) {
		event := &info.Event{
			EventType:      "pull_request",
			TriggerComment: "", // No comment
		}

		celContext, err := assembler.BuildCELContext(pr, event, repo)
		assert.NilError(t, err)

		body, ok := celContext["body"].(map[string]any)
		assert.Assert(t, ok)
		eventMap, ok := body["event"].(map[string]any)
		assert.Assert(t, ok)

		// trigger_comment should not exist
		_, exists := eventMap["trigger_comment"]
		assert.Assert(t, !exists, "trigger_comment should not exist when empty")
	})
}

func TestBuildCommitContent(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	kinteract := &kubeinteraction.Interaction{}
	assembler := NewAssembler(run, kinteract, logger)
	ctx, _ := rtesting.SetupFakeContext(t)

	t.Run("basic commit fields without provider", func(t *testing.T) {
		event := &info.Event{
			SHA:      "abc123",
			SHATitle: "feat: add new feature",
		}

		commitData, err := assembler.buildCommitContent(ctx, event, nil)
		assert.NilError(t, err)

		// Should have basic fields
		assert.Equal(t, commitData["sha"], "abc123")
		assert.Equal(t, commitData["message"], "feat: add new feature")

		// Should not have extended fields
		_, hasURL := commitData["url"]
		assert.Assert(t, !hasURL, "url should not exist without provider")
	})

	t.Run("with full commit information after GetCommitInfo", func(t *testing.T) {
		event := &info.Event{
			SHA:               "abc123",
			SHATitle:          "feat: add new feature",
			SHAURL:            "https://github.com/org/repo/commit/abc123",
			SHAMessage:        "feat: add new feature\n\nThis is the detailed commit message explaining the changes.",
			SHAAuthorName:     "John Doe",
			SHAAuthorEmail:    "john@example.com", // Populated but excluded from LLM context
			SHACommitterName:  "GitHub",
			SHACommitterEmail: "noreply@github.com", // Populated but excluded from LLM context
		}

		commitData, err := assembler.buildCommitContent(ctx, event, nil)
		assert.NilError(t, err)

		// Basic fields
		assert.Equal(t, commitData["sha"], "abc123")
		assert.Equal(t, commitData["message"], "feat: add new feature")
		assert.Equal(t, commitData["url"], "https://github.com/org/repo/commit/abc123")

		// Full message
		fullMsg, ok := commitData["full_message"].(string)
		assert.Assert(t, ok, "full_message should be a string")
		assert.Assert(t, strings.Contains(fullMsg, "detailed commit message"))

		// Author information (name only, email excluded for privacy)
		author, ok := commitData["author"].(map[string]any)
		assert.Assert(t, ok, "author should be a map")
		assert.Equal(t, author["name"], "John Doe")
		_, hasEmail := author["email"]
		assert.Assert(t, !hasEmail, "email should be excluded for privacy/PII reasons")

		// Committer information (name only, email excluded for privacy)
		committer, ok := commitData["committer"].(map[string]any)
		assert.Assert(t, ok, "committer should be a map")
		assert.Equal(t, committer["name"], "GitHub")
		_, hasEmail = committer["email"]
		assert.Assert(t, !hasEmail, "email should be excluded for privacy/PII reasons")
	})

	t.Run("with author date and committer date", func(t *testing.T) {
		authorDate := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		committerDate := time.Date(2024, 1, 15, 10, 31, 0, 0, time.UTC)

		event := &info.Event{
			SHA:              "abc123",
			SHATitle:         "fix: bug fix",
			SHAAuthorName:    "Jane Developer",
			SHAAuthorDate:    authorDate,
			SHACommitterName: "CI Bot",
			SHACommitterDate: committerDate,
		}

		commitData, err := assembler.buildCommitContent(ctx, event, nil)
		assert.NilError(t, err)

		author, ok := commitData["author"].(map[string]any)
		assert.Assert(t, ok)
		assert.DeepEqual(t, author["date"], authorDate)

		committer, ok := commitData["committer"].(map[string]any)
		assert.Assert(t, ok)
		assert.DeepEqual(t, committer["date"], committerDate)
	})

	t.Run("provider commit info error still returns base data", func(t *testing.T) {
		event := &info.Event{
			SHA:      "abc123",
			SHATitle: "fix: keep base data",
		}

		commitData, err := assembler.buildCommitContent(ctx, event, &tprovider.TestProviderImp{
			FailGetCommitInfo:  true,
			CommitInfoErrorMsg: "provider unavailable",
		})
		assert.NilError(t, err)
		assert.Equal(t, commitData["sha"], "abc123")
		assert.Equal(t, commitData["message"], "fix: keep base data")
	})

	t.Run("when full_message equals title, don't duplicate", func(t *testing.T) {
		event := &info.Event{
			SHA:        "abc123",
			SHATitle:   "fix: simple fix",
			SHAMessage: "fix: simple fix", // Same as title
		}

		commitData, err := assembler.buildCommitContent(ctx, event, nil)
		assert.NilError(t, err)

		// full_message should not be present since it's the same as title
		_, hasFull := commitData["full_message"]
		assert.Assert(t, !hasFull, "full_message should not exist when same as title")
	})

	t.Run("verify emails are always excluded even when present", func(t *testing.T) {
		event := &info.Event{
			SHA:               "abc123",
			SHATitle:          "feat: new feature",
			SHAAuthorName:     "John Doe",
			SHAAuthorEmail:    "john@example.com",   // Present but should be excluded
			SHACommitterEmail: "commit@example.com", // Present but should be excluded
		}

		commitData, err := assembler.buildCommitContent(ctx, event, nil)
		assert.NilError(t, err)

		// Email should never be included, even when populated
		author, ok := commitData["author"].(map[string]any)
		assert.Assert(t, ok, "author should exist")
		assert.Equal(t, author["name"], "John Doe")
		_, hasAuthorEmail := author["email"]
		assert.Assert(t, !hasAuthorEmail, "author email must be excluded for privacy")

		// Committer should not exist (no name provided)
		_, hasCommitter := commitData["committer"]
		assert.Assert(t, !hasCommitter, "committer should not exist without name or date")
	})

	t.Run("nil event", func(t *testing.T) {
		_, err := assembler.buildCommitContent(ctx, nil, nil)
		assert.ErrorContains(t, err, "event is nil")
	})
}

func TestBuildBasicPipelineContext(t *testing.T) {
	logger, _ := logger.GetLogger()
	assembler := NewAssembler(&params.Run{}, &kubeinteraction.Interaction{}, logger)

	t.Run("no conditions or timestamps", func(t *testing.T) {
		pr := &tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "test-ns"},
		}
		event := &info.Event{EventType: "push", SHA: "abc", BaseBranch: "main", HeadBranch: "feature"}

		data := assembler.buildBasicPipelineContext(pr, event)
		assert.Equal(t, data["name"], "test-pr")
		assert.Equal(t, data["namespace"], "test-ns")
		assert.Equal(t, data["status"], "unknown")
		assert.Equal(t, data["event_type"], "push")
		assert.Equal(t, data["sha"], "abc")
		assert.Equal(t, data["base_branch"], "main")
		assert.Equal(t, data["head_branch"], "feature")
	})

	t.Run("with condition and timestamps", func(t *testing.T) {
		startTime := metav1.NewTime(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
		completionTime := metav1.NewTime(time.Date(2024, 1, 1, 1, 0, 0, 0, time.UTC))
		pr := &tektonv1.PipelineRun{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "test-ns"},
		}
		pr.Status.Conditions = append(pr.Status.Conditions, apis.Condition{
			Status: corev1.ConditionTrue, Reason: "Succeeded", Message: "all good",
		})
		pr.Status.StartTime = &startTime
		pr.Status.CompletionTime = &completionTime

		data := assembler.buildBasicPipelineContext(pr, nil)
		assert.Equal(t, data["status"], corev1.ConditionTrue)
		assert.Equal(t, data["reason"], "Succeeded")
		assert.Equal(t, data["message"], "all good")
		assert.Equal(t, data["start_time"], startTime.Time)
		assert.Equal(t, data["completion_time"], completionTime.Time)
		_, hasEventType := data["event_type"]
		assert.Assert(t, !hasEventType, "event_type should not be set with nil event")
	})
}

func TestBuildPRContent(t *testing.T) {
	logger, _ := logger.GetLogger()
	assembler := NewAssembler(&params.Run{}, &kubeinteraction.Interaction{}, logger)
	ctx, _ := rtesting.SetupFakeContext(t)

	t.Run("no pull request", func(t *testing.T) {
		_, err := assembler.buildPRContent(ctx, &info.Event{}, nil)
		assert.ErrorContains(t, err, "no pull request information available")
	})

	t.Run("nil event", func(t *testing.T) {
		_, err := assembler.buildPRContent(ctx, nil, nil)
		assert.ErrorContains(t, err, "no pull request information available")
	})

	t.Run("with pull request", func(t *testing.T) {
		event := &info.Event{
			PullRequestNumber: 42,
			PullRequestTitle:  "my great PR",
			HeadBranch:        "feature",
			BaseBranch:        "main",
		}
		data, err := assembler.buildPRContent(ctx, event, nil)
		assert.NilError(t, err)
		assert.Equal(t, data["number"], 42)
		assert.Equal(t, data["title"], "my great PR")
		assert.Equal(t, data["head_branch"], "feature")
		assert.Equal(t, data["base_branch"], "main")
	})
}

func TestBuildErrorContent(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	assembler := NewAssembler(run, &kubeinteraction.Interaction{}, logger)
	ctx, _ := rtesting.SetupFakeContext(t)

	t.Run("no conditions", func(t *testing.T) {
		pr := &tektonv1.PipelineRun{}
		data := assembler.buildErrorContent(ctx, pr)
		assert.Assert(t, data == nil)
	})

	t.Run("condition not failed", func(t *testing.T) {
		pr := &tektonv1.PipelineRun{}
		pr.Status.Conditions = append(pr.Status.Conditions, apis.Condition{Status: corev1.ConditionTrue})
		data := assembler.buildErrorContent(ctx, pr)
		assert.Assert(t, data == nil)
	})

	t.Run("condition failed without task failures", func(t *testing.T) {
		pr := &tektonv1.PipelineRun{}
		pr.Status.Conditions = append(pr.Status.Conditions, apis.Condition{
			Status: corev1.ConditionFalse, Reason: "Failed", Message: "pipeline failed",
		})
		data := assembler.buildErrorContent(ctx, pr)
		assert.Assert(t, data != nil)
		assert.Equal(t, data["condition_reason"], "Failed")
		assert.Equal(t, data["condition_message"], "pipeline failed")
		_, hasFailedTasks := data["failed_tasks"]
		assert.Assert(t, !hasFailedTasks, "no failed_tasks expected without child references")
	})
}

func TestBuildErrorContentFailedTasks(t *testing.T) {
	tests := []buildErrorContentFailedTaskCase{
		{
			name:           "failed task includes pod logs and metadata",
			podOutput:      "compile failed\nsee details",
			wantLogSnippet: "compile failed\nsee details",
			setup: func(t *testing.T, tt *buildErrorContentFailedTaskCase) {
				t.Helper()
				tt.kinteract = &recordingPodLogsInterface{
					KinterfaceTest: kitesthelper.KinterfaceTest{
						GetPodLogsOutput: map[string]string{"build-pod": tt.podOutput},
					},
				}
			},
		},
		{
			name:           "log fetch error falls back to task message",
			wantLogSnippet: "task failed",
			setup: func(t *testing.T, tt *buildErrorContentFailedTaskCase) {
				t.Helper()
				tt.kinteract = &errorPodLogsInterface{err: errors.New("logs unavailable")}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(t, &tt)
			}
			assembler, ctx, pr := setupFailedPipelineContext(t, tt.kinteract)

			data := assembler.buildErrorContent(ctx, pr)
			assert.Assert(t, data != nil)
			assert.Equal(t, data["condition_reason"], "Failed")
			assert.Equal(t, data["condition_message"], "pipeline failed")

			failedTasks, ok := data["failed_tasks"].([]map[string]any)
			assert.Assert(t, ok, "failed_tasks should be []map[string]any")
			assert.Equal(t, len(failedTasks), 1)
			assert.Equal(t, failedTasks[0]["name"], "build")
			assert.Equal(t, failedTasks[0]["display_name"], "Build task")
			assert.Equal(t, failedTasks[0]["reason"], "Failed")
			assert.Equal(t, failedTasks[0]["message"], "task failed")
			assert.Equal(t, failedTasks[0]["log_snippet"], tt.wantLogSnippet)
			_, hasCompletionTime := failedTasks[0]["completion_time"]
			assert.Assert(t, hasCompletionTime, "completion_time should be set")
		})
	}
}

func TestBuildContainerLogs(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	assembler := NewAssembler(run, &kubeinteraction.Interaction{}, logger)
	ctx, _ := rtesting.SetupFakeContext(t)

	t.Run("no failed tasks returns nil", func(t *testing.T) {
		pr := &tektonv1.PipelineRun{}
		data := assembler.buildContainerLogs(ctx, pr, 50)
		assert.Assert(t, data == nil)
	})
}

func TestBuildContainerLogsFailedTasks(t *testing.T) {
	tests := []buildContainerLogsFailedTaskCase{
		{
			name:         "failed task includes pod log lines",
			podOutput:    "line one\nline two",
			maxLines:     2,
			wantLogLines: []string{"line one", "line two"},
			setup: func(t *testing.T, tt *buildContainerLogsFailedTaskCase) {
				t.Helper()
				tt.kinteract = &recordingPodLogsInterface{
					KinterfaceTest: kitesthelper.KinterfaceTest{
						GetPodLogsOutput: map[string]string{"build-pod": tt.podOutput},
					},
				}
			},
		},
		{
			name:         "log fetch error uses task message",
			maxLines:     5,
			wantLogLines: []string{"task failed"},
			setup: func(t *testing.T, tt *buildContainerLogsFailedTaskCase) {
				t.Helper()
				tt.kinteract = &errorPodLogsInterface{err: errors.New("logs unavailable")}
			},
		},
		{
			name:          "long pod logs are truncated safely",
			podOutput:     strings.Repeat("x", 70000),
			maxLines:      3,
			wantMaxLength: 65535,
			setup: func(t *testing.T, tt *buildContainerLogsFailedTaskCase) {
				t.Helper()
				tt.kinteract = &recordingPodLogsInterface{
					KinterfaceTest: kitesthelper.KinterfaceTest{
						GetPodLogsOutput: map[string]string{"build-pod": tt.podOutput},
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(t, &tt)
			}
			assembler, ctx, pr := setupFailedPipelineContext(t, tt.kinteract)

			data := assembler.buildContainerLogs(ctx, pr, tt.maxLines)
			assert.Assert(t, data != nil)
			assert.Equal(t, data["max_lines"], tt.maxLines)

			failedTasksLogs, ok := data["failed_tasks_logs"].([]map[string]any)
			assert.Assert(t, ok, "failed_tasks_logs should be []map[string]any")
			assert.Equal(t, len(failedTasksLogs), 1)
			assert.Equal(t, failedTasksLogs[0]["task_name"], "build")
			assert.Equal(t, failedTasksLogs[0]["display_name"], "Build task")

			logLines, ok := failedTasksLogs[0]["log_lines"].([]string)
			assert.Assert(t, ok, "log_lines should be []string")
			if tt.wantLogLines != nil {
				assert.DeepEqual(t, logLines, tt.wantLogLines)
			}
			if tt.wantMaxLength > 0 {
				logContent := strings.Join(logLines, "\n")
				assert.Assert(t, len(logContent) <= tt.wantMaxLength,
					"expected log content length at most %d, got %d", tt.wantMaxLength, len(logContent))
				assert.Assert(t, len(logContent) < len(tt.podOutput),
					"expected truncated logs to be shorter than original output")
			}
			if recorder, ok := tt.kinteract.(*recordingPodLogsInterface); ok {
				assert.Equal(t, recorder.tailLines, int64(tt.maxLines))
			}
		})
	}
}

func TestBuildContext(t *testing.T) {
	logger, _ := logger.GetLogger()
	run := &params.Run{}
	assembler := NewAssembler(run, &kubeinteraction.Interaction{}, logger)
	ctx, _ := rtesting.SetupFakeContext(t)

	pr := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pr", Namespace: "test-ns"},
	}
	event := &info.Event{EventType: "push", SHA: "abc"}

	t.Run("nil config returns basic pipeline context only", func(t *testing.T) {
		data, err := assembler.BuildContext(ctx, pr, event, nil, nil)
		assert.NilError(t, err)
		assert.Equal(t, data["name"], "test-pr")
	})

	t.Run("full config", func(t *testing.T) {
		config := &v1alpha1.ContextConfig{
			CommitContent: true,
			PRContent:     true,
			ErrorContent:  true,
			ContainerLogs: &v1alpha1.ContainerLogsConfig{Enabled: true, MaxLines: 10},
		}
		data, err := assembler.BuildContext(ctx, pr, event, config, nil)
		assert.NilError(t, err)

		commitData, ok := data["commit"].(map[string]any)
		assert.Assert(t, ok, "commit data should be present")
		assert.Equal(t, commitData["sha"], "abc")

		_, hasPR := data["pull_request"]
		assert.Assert(t, !hasPR, "no PR data since event has no pull request number")

		pipelineData, ok := data["pipeline"].(map[string]any)
		assert.Assert(t, ok, "pipeline data should always be present")
		assert.Equal(t, pipelineData["name"], "test-pr")
	})

	t.Run("with PR content", func(t *testing.T) {
		prEvent := &info.Event{PullRequestNumber: 7, PullRequestTitle: "title"}
		config := &v1alpha1.ContextConfig{PRContent: true}
		data, err := assembler.BuildContext(ctx, pr, prEvent, config, nil)
		assert.NilError(t, err)

		prData, ok := data["pull_request"].(map[string]any)
		assert.Assert(t, ok, "pull_request data should be present")
		assert.Equal(t, prData["number"], 7)
	})
}

func TestBuildContextErrorAndLogBranches(t *testing.T) {
	tests := []buildContextBranchCase{
		{
			name: "commit content error continues without commit",
			config: &v1alpha1.ContextConfig{
				CommitContent: true,
			},
			setup: func(t *testing.T, _ *buildContextBranchCase) (*Assembler, stdcontext.Context, *tektonv1.PipelineRun) {
				t.Helper()
				testLogger, _ := logger.GetLogger()
				return NewAssembler(&params.Run{}, &kubeinteraction.Interaction{}, testLogger),
					stdcontext.Background(),
					&tektonv1.PipelineRun{ObjectMeta: metav1.ObjectMeta{Name: "basic", Namespace: "test-ns"}}
			},
		},
		{
			name: "failed pipeline adds errors and default logs",
			config: &v1alpha1.ContextConfig{
				ErrorContent: true,
				ContainerLogs: &v1alpha1.ContainerLogsConfig{
					Enabled: true,
				},
			},
			event:        &info.Event{EventType: "push", SHA: "abc123"},
			wantErrors:   true,
			wantLogs:     true,
			wantLogLines: []string{"failure line"},
			wantMaxLines: DefaultMaxLogLines,
			setup: func(t *testing.T, _ *buildContextBranchCase) (*Assembler, stdcontext.Context, *tektonv1.PipelineRun) {
				t.Helper()
				return setupFailedPipelineContext(t, &recordingPodLogsInterface{
					KinterfaceTest: kitesthelper.KinterfaceTest{
						GetPodLogsOutput: map[string]string{"build-pod": "failure line"},
					},
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assembler, ctx, pr := tt.setup(t, &tt)
			data, err := assembler.BuildContext(ctx, pr, tt.event, tt.config, nil)
			assert.NilError(t, err)

			_, hasCommit := data["commit"]
			assert.Equal(t, hasCommit, tt.wantCommit)

			_, hasErrors := data["errors"]
			assert.Equal(t, hasErrors, tt.wantErrors)

			logData, hasLogs := data["logs"].(map[string]any)
			assert.Equal(t, hasLogs, tt.wantLogs)
			if tt.wantLogs {
				assert.Equal(t, logData["max_lines"], tt.wantMaxLines)
				failedTasksLogs, ok := logData["failed_tasks_logs"].([]map[string]any)
				assert.Assert(t, ok)
				logLines, ok := failedTasksLogs[0]["log_lines"].([]string)
				assert.Assert(t, ok)
				assert.DeepEqual(t, logLines, tt.wantLogLines)
			}

			pipelineData, ok := data["pipeline"].(map[string]any)
			assert.Assert(t, ok)
			assert.Equal(t, pipelineData["namespace"], "test-ns")
		})
	}
}

func TestMapConvertersNilInput(t *testing.T) {
	logger, _ := logger.GetLogger()
	assembler := NewAssembler(&params.Run{}, &kubeinteraction.Interaction{}, logger)

	tests := []struct {
		name      string
		convert   func() (map[string]any, error)
		wantNil   bool
		wantError bool
	}{
		{
			name: "nil pipelinerun returns nil map",
			convert: func() (map[string]any, error) {
				return assembler.pipelineRunToMap(nil)
			},
			wantNil: true,
		},
		{
			name: "nil repository returns nil map",
			convert: func() (map[string]any, error) {
				return assembler.repositoryToMap(nil)
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.convert()
			if tt.wantError {
				assert.Assert(t, err != nil, "expected error but got none")
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got == nil, tt.wantNil)
		})
	}
}
