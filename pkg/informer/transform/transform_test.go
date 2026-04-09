package transform

import (
	"testing"

	pacv1alpha1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
)

func makeRepo(annotations map[string]string, managedFields []metav1.ManagedFieldsEntry, status []pacv1alpha1.RepositoryRunStatus) *pacv1alpha1.Repository {
	return &pacv1alpha1.Repository{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "test-repo",
			Namespace:     "test-ns",
			Annotations:   annotations,
			ManagedFields: managedFields,
		},
		Spec:   pacv1alpha1.RepositorySpec{URL: "https://github.com/org/repo"},
		Status: status,
	}
}

func TestRepositoryForCache(t *testing.T) {
	tests := []struct {
		name          string
		input         any
		wantStatusNil bool
		wantSpecURL   string
	}{
		{
			name: "strips managedFields, annotations, and status, keeps spec",
			input: makeRepo(
				map[string]string{"keep-me": "yes"},
				[]metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
				[]pacv1alpha1.RepositoryRunStatus{{PipelineRunName: "pr-1"}},
			),
			wantStatusNil: true,
			wantSpecURL:   "https://github.com/org/repo",
		},
		{
			name: "nil annotations handled safely",
			input: makeRepo(
				nil,
				[]metav1.ManagedFieldsEntry{{Manager: "controller"}},
				nil,
			),
			wantStatusNil: true,
			wantSpecURL:   "https://github.com/org/repo",
		},
		{
			name:  "non-Repository object passed through unchanged",
			input: struct{ Name string }{"something-else"},
		},
		{
			name: "tombstone wrapping Repository is unwrapped and transformed",
			input: cache.DeletedFinalStateUnknown{
				Key: "test-ns/test-repo",
				Obj: makeRepo(
					map[string]string{"kubectl.kubernetes.io/last-applied-configuration": "data"},
					[]metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
					[]pacv1alpha1.RepositoryRunStatus{{PipelineRunName: "pr-1"}},
				),
			},
			wantStatusNil: true,
			wantSpecURL:   "https://github.com/org/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RepositoryForCache(tt.input)
			assert.NilError(t, err)

			switch v := result.(type) {
			case *pacv1alpha1.Repository:
				assert.Assert(t, v.ManagedFields == nil, "ManagedFields should be nil")
				assert.Assert(t, v.Annotations == nil, "Annotations should be nil")
				assert.Equal(t, v.Spec.URL, tt.wantSpecURL)
				if tt.wantStatusNil {
					assert.Assert(t, v.Status == nil, "Status should be nil")
				}
			case cache.DeletedFinalStateUnknown:
				repo, ok := v.Obj.(*pacv1alpha1.Repository)
				assert.Assert(t, ok, "tombstone Obj should be *Repository")
				assert.Assert(t, repo.ManagedFields == nil, "ManagedFields should be nil after tombstone transform")
				assert.Assert(t, repo.Annotations == nil, "Annotations should be nil after tombstone transform")
				assert.Assert(t, repo.Status == nil, "Status should be nil after tombstone transform")
				assert.Equal(t, repo.Spec.URL, tt.wantSpecURL)
			default:
				// non-Repository pass-through: just verify no error
			}
		})
	}
}

func makePR(annotations map[string]string, managedFields []metav1.ManagedFieldsEntry) *tektonv1.PipelineRun {
	pipelineSpec := &tektonv1.PipelineSpec{Description: "full spec"}
	return &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "pr-1",
			Namespace:     "test-ns",
			Annotations:   annotations,
			ManagedFields: managedFields,
		},
		Spec: tektonv1.PipelineRunSpec{
			PipelineRef:  &tektonv1.PipelineRef{Name: "my-pipeline"},
			PipelineSpec: pipelineSpec,
			Params:       tektonv1.Params{{Name: "key", Value: tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: "val"}}},
			Workspaces:   []tektonv1.WorkspaceBinding{{Name: "ws"}},
			Timeouts:     &tektonv1.TimeoutFields{},
		},
		Status: tektonv1.PipelineRunStatus{
			PipelineRunStatusFields: tektonv1.PipelineRunStatusFields{
				PipelineSpec:    pipelineSpec,
				ChildReferences: []tektonv1.ChildStatusReference{{Name: "tr-1"}},
				SpanContext:     map[string]string{"traceID": "abc"},
			},
		},
	}
}

func TestPipelineRunForCache(t *testing.T) {
	tests := []struct {
		name  string
		input any
	}{
		{
			name: "strips large spec and status fields, keeps conditions and timing",
			input: makePR(
				map[string]string{"pipelinesascode.tekton.dev/state": "started"},
				[]metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
			),
		},
		{
			name:  "non-PipelineRun passed through unchanged",
			input: struct{ Name string }{"something"},
		},
		{
			name: "tombstone wrapping PipelineRun is unwrapped and transformed",
			input: cache.DeletedFinalStateUnknown{
				Key: "test-ns/pr-1",
				Obj: makePR(
					map[string]string{"pipelinesascode.tekton.dev/state": "started"},
					[]metav1.ManagedFieldsEntry{{Manager: "kubectl"}},
				),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := PipelineRunForCache(tt.input)
			assert.NilError(t, err)

			checkPR := func(pr *tektonv1.PipelineRun) {
				assert.Assert(t, pr.ManagedFields == nil, "ManagedFields should be nil")
				// annotations preserved (PAC reads state/repo annotations from cache)
				assert.Assert(t, pr.Annotations != nil, "Annotations should be preserved")
				// large spec fields stripped
				assert.Assert(t, pr.Spec.PipelineRef == nil, "Spec.PipelineRef should be nil")
				assert.Assert(t, pr.Spec.PipelineSpec == nil, "Spec.PipelineSpec should be nil")
				assert.Assert(t, pr.Spec.Params == nil, "Spec.Params should be nil")
				assert.Assert(t, pr.Spec.Workspaces == nil, "Spec.Workspaces should be nil")
				assert.Assert(t, pr.Spec.Timeouts == nil, "Spec.Timeouts should be nil")
				// large status fields stripped
				assert.Assert(t, pr.Status.PipelineSpec == nil, "Status.PipelineSpec should be nil")
				assert.Assert(t, pr.Status.ChildReferences == nil, "Status.ChildReferences should be nil")
				assert.Assert(t, pr.Status.SpanContext == nil, "Status.SpanContext should be nil")
				// name and namespace preserved
				assert.Equal(t, pr.Name, "pr-1")
				assert.Equal(t, pr.Namespace, "test-ns")
			}

			switch v := result.(type) {
			case *tektonv1.PipelineRun:
				checkPR(v)
			case cache.DeletedFinalStateUnknown:
				pr, ok := v.Obj.(*tektonv1.PipelineRun)
				assert.Assert(t, ok, "tombstone Obj should be *PipelineRun")
				checkPR(pr)
			default:
				// non-PipelineRun pass-through
			}
		})
	}
}
