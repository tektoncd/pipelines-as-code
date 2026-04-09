package transform

import (
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
	"time"

	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/apis"
	duckv1 "knative.dev/pkg/apis/duck/v1"
)

// createRealisticRepository creates a Repository with realistic field sizes
// that would be seen in production environments.
func createRealisticRepository(name string) *pacv1alpha1.Repository {
	// Realistic managedFields (typically 500-2000 bytes).
	fieldsV1Data1 := []byte(`{"f:metadata":{"f:annotations":{"f:kubectl.kubernetes.io/last-applied-configuration":{}},"f:labels":{}},"f:spec":{"f:url":{},"f:git_provider":{"f:url":{},"f:type":{},"f:secret":{"f:name":{},"f:key":{}},"f:webhook_secret":{"f:name":{},"f:key":{}}},"f:settings":{"f:pipelinerun_provenance":{},"f:policy":{"f:ok_to_test":{},"f:pull_request":{}}},"f:params":{}}}`)
	fieldsV1Data2 := []byte(`{"f:pipelinerun_status":{}}`)

	now := metav1.NewTime(time.Now())

	// Realistic last-applied-configuration value: the applied Repository spec as JSON (typically 500-2000 bytes).
	lastAppliedJSON := []byte(`{"apiVersion":"pipelinesascode.tekton.dev/v1alpha1","kind":"Repository","metadata":{"name":"` + name + `","namespace":"pipelines"},"spec":{"url":"https://github.com/org/repo","git_provider":{"url":"https://api.github.com","type":"github","secret":{"name":"github-token","key":"token"},"webhook_secret":{"name":"github-webhook-secret","key":"secret"}},"settings":{"pipelinerun_provenance":"source","policy":{"ok_to_test":["user1","user2","user3"],"pull_request":["user4","user5"]}},"params":[{"name":"deploy-env","value":"staging"},{"name":"registry","value":"quay.io/myorg"},{"name":"image-tag","value":"latest"}]}}`)

	// PAC stores the last 5 pipeline run statuses in the Repository status.
	status := make([]pacv1alpha1.RepositoryRunStatus, 5)
	for i := range 5 {
		sha := fmt.Sprintf("abc%d%06d", i, i)
		shaURL := fmt.Sprintf("https://github.com/org/repo/commit/%s", sha)
		logURL := fmt.Sprintf("https://console.example.com/k8s/ns/pipelines/tekton.dev~v1~PipelineRun/pr-%s/logs", sha)
		branch := "main"
		eventType := "push"
		title := fmt.Sprintf("feat: add feature number %d", i)
		taskInfos := map[string]pacv1alpha1.TaskInfos{
			"build": {
				Name:        "build",
				DisplayName: "Build Image",
				Reason:      "Error",
				Message:     "container image build failed with exit code 1",
				LogSnippet:  "error: exit status 1\nbuild failed: cannot find package",
			},
		}
		status[i] = pacv1alpha1.RepositoryRunStatus{
			Status: duckv1.Status{
				Conditions: []apis.Condition{{
					Type:   apis.ConditionSucceeded,
					Status: "True",
					Reason: "Succeeded",
				}},
			},
			PipelineRunName:    fmt.Sprintf("pr-%s", sha),
			StartTime:          &now,
			CompletionTime:     &now,
			SHA:                &sha,
			SHAURL:             &shaURL,
			Title:              &title,
			LogURL:             &logURL,
			TargetBranch:       &branch,
			EventType:          &eventType,
			CollectedTaskInfos: &taskInfos,
		}
	}

	return &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "pipelines",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{
					Manager:    "kubectl-client-side-apply",
					Operation:  metav1.ManagedFieldsOperationApply,
					APIVersion: "pipelinesascode.tekton.dev/v1alpha1",
					Time:       &now,
					FieldsType: "FieldsV1",
					FieldsV1:   &metav1.FieldsV1{Raw: fieldsV1Data1},
				},
				{
					Manager:    "pipelines-as-code-controller",
					Operation:  metav1.ManagedFieldsOperationUpdate,
					APIVersion: "pipelinesascode.tekton.dev/v1alpha1",
					Time:       &now,
					FieldsType: "FieldsV1",
					FieldsV1:   &metav1.FieldsV1{Raw: fieldsV1Data2},
				},
			},
			Annotations: map[string]string{
				"kubectl.kubernetes.io/last-applied-configuration": string(lastAppliedJSON),
				"app.kubernetes.io/managed-by":                     "pipelines-as-code",
			},
			Labels: map[string]string{
				"app.kubernetes.io/part-of": "pipelines-as-code",
			},
		},
		Spec: pacv1alpha1.RepositorySpec{
			URL: "https://github.com/org/repo",
			GitProvider: &pacv1alpha1.GitProvider{
				URL:  "https://api.github.com",
				Type: "github",
				Secret: &pacv1alpha1.Secret{
					Name: "github-token",
					Key:  "token",
				},
				WebhookSecret: &pacv1alpha1.Secret{
					Name: "github-webhook-secret",
					Key:  "secret",
				},
			},
			Settings: &pacv1alpha1.Settings{
				PipelineRunProvenance: "source",
				Policy: &pacv1alpha1.Policy{
					OkToTest:    []string{"user1", "user2", "user3"},
					PullRequest: []string{"user4", "user5"},
				},
			},
			Params: &[]pacv1alpha1.Params{
				{Name: "deploy-env", Value: "staging"},
				{Name: "registry", Value: "quay.io/myorg"},
				{Name: "image-tag", Value: "latest"},
			},
		},
		Status: status,
	}
}

// measureObjectSize returns the approximate JSON size of an object in bytes.
func measureObjectSize(obj any) int {
	data, _ := json.Marshal(obj) //nolint:errchkjson // test helper
	return len(data)
}

// BenchmarkRepoTransformMemorySavings benchmarks the transform function
// and reports the JSON size reduction it achieves.
func BenchmarkRepoTransformMemorySavings(b *testing.B) { //nolint:dupl // parallel structure for Repo and PipelineRun benchmarks is intentional
	original := createRealisticRepository("benchmark-repo")
	originalSize := measureObjectSize(original)

	b.Run("Original", func(b *testing.B) {
		b.ReportAllocs()
		for i := range b.N {
			repo := createRealisticRepository(fmt.Sprintf("repo-%d", i))
			_ = measureObjectSize(repo)
		}
	})

	b.Run("Transformed", func(b *testing.B) {
		b.ReportAllocs()
		for i := range b.N {
			repo := createRealisticRepository(fmt.Sprintf("repo-%d", i))
			transformed, _ := RepositoryForCache(repo)
			_ = measureObjectSize(transformed)
		}
	})

	transformed, _ := RepositoryForCache(original.DeepCopy())
	transformedSize := measureObjectSize(transformed)
	reduction := float64(originalSize-transformedSize) / float64(originalSize) * 100
	b.Logf("Original size: %d bytes", originalSize)
	b.Logf("Transformed size: %d bytes", transformedSize)
	b.Logf("Reduction: %.1f%%", reduction)
}

// BenchmarkRepoTransformCacheMemoryUsage simulates caching many Repositories
// and measures the heap impact with and without the transform applied.
func BenchmarkRepoTransformCacheMemoryUsage(b *testing.B) { //nolint:dupl // parallel structure for Repo and PipelineRun benchmarks is intentional
	const numObjects = 1000

	b.Run("WithoutTransform", func(b *testing.B) {
		b.ReportAllocs()
		runtime.GC()
		var memBefore runtime.MemStats
		runtime.ReadMemStats(&memBefore)

		repos := make([]*pacv1alpha1.Repository, numObjects)
		for i := range numObjects {
			repos[i] = createRealisticRepository(fmt.Sprintf("repo-%d", i))
		}

		runtime.GC()
		var memAfter runtime.MemStats
		runtime.ReadMemStats(&memAfter)

		memUsed := memAfter.HeapAlloc - memBefore.HeapAlloc
		b.Logf("Memory for %d Repositories (no transform): %d bytes (%.2f KB each)",
			numObjects, memUsed, float64(memUsed)/float64(numObjects)/1024)

		runtime.KeepAlive(repos)
	})

	b.Run("WithTransform", func(b *testing.B) {
		b.ReportAllocs()
		runtime.GC()
		var memBefore runtime.MemStats
		runtime.ReadMemStats(&memBefore)

		repos := make([]any, numObjects)
		for i := range numObjects {
			repo := createRealisticRepository(fmt.Sprintf("repo-%d", i))
			repos[i], _ = RepositoryForCache(repo)
		}

		runtime.GC()
		var memAfter runtime.MemStats
		runtime.ReadMemStats(&memAfter)

		memUsed := memAfter.HeapAlloc - memBefore.HeapAlloc
		b.Logf("Memory for %d Repositories (with transform): %d bytes (%.2f KB each)",
			numObjects, memUsed, float64(memUsed)/float64(numObjects)/1024)

		runtime.KeepAlive(repos)
	})
}

// TestMeasureRepoTransformSavings reports the memory savings from
// the cache transform. Run with: go test -v -run TestMeasureRepoTransformSavings.
func TestMeasureRepoTransformSavings(t *testing.T) {
	original := createRealisticRepository("test-repo")
	transformed, _ := RepositoryForCache(original.DeepCopy())

	originalSize := measureObjectSize(original)
	transformedSize := measureObjectSize(transformed)

	if originalSize == 0 {
		t.Fatal("Original size is 0, JSON marshaling failed")
	}

	jsonSavings := float64(originalSize-transformedSize) / float64(originalSize) * 100

	t.Logf("\n=== JSON Size Report ===")
	t.Logf("Original JSON size:    %d bytes", originalSize)
	t.Logf("Transformed JSON size: %d bytes", transformedSize)
	t.Logf("Size saved:            %d bytes (%.1f%%)", originalSize-transformedSize, jsonSavings)
	t.Logf("=======================")

	transformedRepo, _ := transformed.(*pacv1alpha1.Repository)
	t.Logf("\n=== Fields Stripped ===")
	t.Logf("ManagedFields: %v (was %d entries)", transformedRepo.ManagedFields == nil, len(original.ManagedFields))
	t.Logf("Annotations:   %v (was %d entries)", transformedRepo.Annotations == nil, len(original.Annotations))
	t.Logf("Status:        %v (was %d entries)", transformedRepo.Status == nil, len(original.Status))
	t.Logf("=======================")

	t.Logf("\n=== Individual Field Sizes ===")
	t.Logf("ManagedFields: %d bytes", measureObjectSize(original.ManagedFields))
	t.Logf("Annotations:   %d bytes", measureObjectSize(original.Annotations))
	t.Logf("Status:        %d bytes", measureObjectSize(original.Status))
	t.Logf("==============================")

	if jsonSavings < 20 {
		t.Errorf("Expected at least 20%% size reduction, got %.1f%%", jsonSavings)
	}
}

// createRealisticPipelineRun creates a PipelineRun with realistic field sizes
// matching production data (must-gather avg: ~36KB per PR without managedFields;
// status.pipelineSpec is the dominant field at ~20KB each).
func createRealisticPipelineRun(name string) *tektonv1.PipelineRun {
	now := metav1.NewTime(time.Now())

	fieldsV1Data1 := []byte(`{"f:metadata":{"f:annotations":{"f:pipelinesascode.tekton.dev/state":{},"f:pipelinesascode.tekton.dev/repository":{}},"f:labels":{}},"f:spec":{"f:pipelineRef":{"f:name":{}},"f:params":{},"f:workspaces":{}}}`)
	fieldsV1Data2 := []byte(`{"f:status":{"f:conditions":{},"f:pipelineSpec":{},"f:childReferences":{},"f:startTime":{},"f:completionTime":{},"f:provenance":{},"f:spanContext":{}}}`)

	// Build a realistic PipelineSpec (~20KB) — each PR stores the full pipeline
	// definition in status.pipelineSpec as it was at execution time.
	tasks := make([]tektonv1.PipelineTask, 15)
	for i := range 15 {
		tasks[i] = tektonv1.PipelineTask{
			Name: fmt.Sprintf("task-%02d", i),
			TaskRef: &tektonv1.TaskRef{
				Name: fmt.Sprintf("my-task-%02d", i),
			},
			Params: tektonv1.Params{
				{Name: "image", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "$(params.image)"}},
				{Name: "revision", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "$(params.revision)"}},
				{Name: "context", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: fmt.Sprintf("/workspace/source/service-%02d", i)}},
			},
		}
		if i > 0 {
			tasks[i].RunAfter = []string{fmt.Sprintf("task-%02d", i-1)}
		}
	}
	pipelineSpec := &tektonv1.PipelineSpec{
		Description: "Production pipeline with build, test, and deploy stages for a microservices application",
		Params: []tektonv1.ParamSpec{
			{Name: "image", Type: tektonv1.ParamTypeString},
			{Name: "revision", Type: tektonv1.ParamTypeString},
			{Name: "repo-url", Type: tektonv1.ParamTypeString},
			{Name: "deploy-env", Type: tektonv1.ParamTypeString, Default: &tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "staging"}},
		},
		Tasks: tasks,
	}

	// Realistic ChildReferences (one per task run)
	childRefs := make([]tektonv1.ChildStatusReference, len(tasks))
	for i, task := range tasks {
		childRefs[i] = tektonv1.ChildStatusReference{
			Name:             fmt.Sprintf("%s-taskrun-%d", task.Name, i),
			DisplayName:      task.Name,
			PipelineTaskName: task.Name,
		}
	}

	return &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "pipelines",
			Annotations: map[string]string{
				"pipelinesascode.tekton.dev/state":                 "completed",
				"pipelinesascode.tekton.dev/repository":            "my-repo",
				"pipelinesascode.tekton.dev/sha":                   "abc123def456",
				"kubectl.kubernetes.io/last-applied-configuration": `{"apiVersion":"tekton.dev/v1","kind":"PipelineRun","metadata":{"name":"` + name + `"}}`,
			},
			Labels: map[string]string{
				"pipelinesascode.tekton.dev/repository": "my-repo",
				"tekton.dev/pipeline":                   "my-pipeline",
			},
			ManagedFields: []metav1.ManagedFieldsEntry{
				{
					Manager:    "kubectl-client-side-apply",
					Operation:  metav1.ManagedFieldsOperationApply,
					APIVersion: "tekton.dev/v1",
					Time:       &now,
					FieldsType: "FieldsV1",
					FieldsV1:   &metav1.FieldsV1{Raw: fieldsV1Data1},
				},
				{
					Manager:    "tekton-pipelines-controller",
					Operation:  metav1.ManagedFieldsOperationUpdate,
					APIVersion: "tekton.dev/v1",
					Time:       &now,
					FieldsType: "FieldsV1",
					FieldsV1:   &metav1.FieldsV1{Raw: fieldsV1Data2},
				},
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			PipelineRef:  &tektonv1.PipelineRef{Name: "my-pipeline"},
			PipelineSpec: pipelineSpec,
			Params: tektonv1.Params{
				{Name: "image", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "quay.io/myorg/myapp:abc123"}},
				{Name: "revision", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "abc123def456"}},
				{Name: "repo-url", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "https://github.com/org/repo"}},
				{Name: "deploy-env", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "production"}},
			},
			Workspaces: []tektonv1.WorkspaceBinding{
				{Name: "source", EmptyDir: &corev1.EmptyDirVolumeSource{}},
				{Name: "cache", EmptyDir: &corev1.EmptyDirVolumeSource{}},
			},
		},
		Status: tektonv1.PipelineRunStatus{
			PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
				StartTime:       &now,
				CompletionTime:  &now,
				PipelineSpec:    pipelineSpec,
				ChildReferences: childRefs,
				Provenance: &tektonv1.Provenance{
					RefSource: &tektonv1.RefSource{
						URI:    "https://github.com/org/repo.git",
						Digest: map[string]string{"sha1": "abc123def456"},
					},
				},
				SpanContext: map[string]string{"traceID": "abc123", "spanID": "def456"},
			},
			Status: duckv1.Status{
				Conditions: []apis.Condition{{
					Type:   apis.ConditionSucceeded,
					Status: "True",
					Reason: "Succeeded",
				}},
			},
		},
	}
}

// BenchmarkPipelineRunTransformMemorySavings benchmarks the transform function
// and reports the JSON size reduction it achieves.
func BenchmarkPipelineRunTransformMemorySavings(b *testing.B) { //nolint:dupl // parallel structure for Repo and PipelineRun benchmarks is intentional
	original := createRealisticPipelineRun("benchmark-pr")
	originalSize := measureObjectSize(original)

	b.Run("Original", func(b *testing.B) {
		b.ReportAllocs()
		for i := range b.N {
			pr := createRealisticPipelineRun(fmt.Sprintf("pr-%d", i))
			_ = measureObjectSize(pr)
		}
	})

	b.Run("Transformed", func(b *testing.B) {
		b.ReportAllocs()
		for i := range b.N {
			pr := createRealisticPipelineRun(fmt.Sprintf("pr-%d", i))
			transformed, _ := PipelineRunForCache(pr)
			_ = measureObjectSize(transformed)
		}
	})

	transformed, _ := PipelineRunForCache(original.DeepCopy())
	transformedSize := measureObjectSize(transformed)
	reduction := float64(originalSize-transformedSize) / float64(originalSize) * 100
	b.Logf("Original size: %d bytes", originalSize)
	b.Logf("Transformed size: %d bytes", transformedSize)
	b.Logf("Reduction: %.1f%%", reduction)
}

// BenchmarkPipelineRunTransformCacheMemoryUsage simulates caching many
// PipelineRuns and measures the heap impact with and without the transform.
func BenchmarkPipelineRunTransformCacheMemoryUsage(b *testing.B) {
	const numObjects = 683 // matches production must-gather count

	b.Run("WithoutTransform", func(b *testing.B) {
		b.ReportAllocs()
		runtime.GC()
		var memBefore runtime.MemStats
		runtime.ReadMemStats(&memBefore)

		prs := make([]*tektonv1.PipelineRun, numObjects)
		for i := range numObjects {
			prs[i] = createRealisticPipelineRun(fmt.Sprintf("pr-%d", i))
		}

		runtime.GC()
		var memAfter runtime.MemStats
		runtime.ReadMemStats(&memAfter)

		memUsed := memAfter.HeapAlloc - memBefore.HeapAlloc
		b.Logf("Memory for %d PipelineRuns (no transform): %d bytes (%.1f KB each)",
			numObjects, memUsed, float64(memUsed)/float64(numObjects)/1024)

		runtime.KeepAlive(prs)
	})

	b.Run("WithTransform", func(b *testing.B) {
		b.ReportAllocs()
		runtime.GC()
		var memBefore runtime.MemStats
		runtime.ReadMemStats(&memBefore)

		prs := make([]any, numObjects)
		for i := range numObjects {
			pr := createRealisticPipelineRun(fmt.Sprintf("pr-%d", i))
			prs[i], _ = PipelineRunForCache(pr)
		}

		runtime.GC()
		var memAfter runtime.MemStats
		runtime.ReadMemStats(&memAfter)

		memUsed := memAfter.HeapAlloc - memBefore.HeapAlloc
		b.Logf("Memory for %d PipelineRuns (with transform): %d bytes (%.1f KB each)",
			numObjects, memUsed, float64(memUsed)/float64(numObjects)/1024)

		runtime.KeepAlive(prs)
	})
}

// TestMeasurePipelineRunTransformSavings reports the memory savings from
// the cache transform. Run with: go test -v -run TestMeasurePipelineRunTransformSavings.
func TestMeasurePipelineRunTransformSavings(t *testing.T) {
	original := createRealisticPipelineRun("test-pr")
	transformed, _ := PipelineRunForCache(original.DeepCopy())

	originalSize := measureObjectSize(original)
	transformedSize := measureObjectSize(transformed)

	if originalSize == 0 {
		t.Fatal("Original size is 0, JSON marshaling failed")
	}

	jsonSavings := float64(originalSize-transformedSize) / float64(originalSize) * 100

	t.Logf("\n=== JSON Size Report ===")
	t.Logf("Original JSON size:    %d bytes", originalSize)
	t.Logf("Transformed JSON size: %d bytes", transformedSize)
	t.Logf("Size saved:            %d bytes (%.1f%%)", originalSize-transformedSize, jsonSavings)
	t.Logf("=======================")

	t.Logf("\n=== Individual Field Sizes ===")
	t.Logf("ManagedFields:          %d bytes", measureObjectSize(original.ManagedFields))
	t.Logf("Status.PipelineSpec:    %d bytes", measureObjectSize(original.Status.PipelineSpec))
	t.Logf("Spec.PipelineSpec:      %d bytes", measureObjectSize(original.Spec.PipelineSpec))
	t.Logf("Status.ChildReferences: %d bytes", measureObjectSize(original.Status.ChildReferences))
	t.Logf("Spec.Params:            %d bytes", measureObjectSize(original.Spec.Params))
	t.Logf("Spec.Workspaces:        %d bytes", measureObjectSize(original.Spec.Workspaces))
	t.Logf("Status.Provenance:      %d bytes", measureObjectSize(original.Status.Provenance))
	t.Logf("==============================")

	if jsonSavings < 50 {
		t.Errorf("Expected at least 50%% size reduction, got %.1f%%", jsonSavings)
	}
}
