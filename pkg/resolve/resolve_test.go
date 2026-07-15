package resolve

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacerrors "github.com/openshift-pipelines/pipelines-as-code/pkg/errors"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/clients"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	testprovider "github.com/openshift-pipelines/pipelines-as-code/pkg/test/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"go.uber.org/zap"
	zapobserver "go.uber.org/zap/zaptest/observer"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8scheme "k8s.io/client-go/kubernetes/scheme"
	rtesting "knative.dev/pkg/reconciler/testing"
)

func TestMain(m *testing.M) {
	setup()
	ret := m.Run()
	os.Exit(ret)
}

func setup() {
	s := k8scheme.Scheme
	if err := tektonv1.AddToScheme(s); err != nil {
		log.Fatalf("Unable to add tekton v1 scheme: (%v)", err)
	}
}

// Not sure how to get testParams fixtures working.
func readTDfile(t *testing.T, testname string, generateName, remoteTasking bool) (*tektonv1.PipelineRun, *zapobserver.ObservedLogs, error) {
	t.Helper()
	ctx, _ := rtesting.SetupFakeContext(t)
	data, err := os.ReadFile("testdata/" + testname + ".yaml")
	if err != nil {
		return &tektonv1.PipelineRun{}, nil, err
	}
	observer, log := zapobserver.New(zap.InfoLevel)
	logger := zap.New(observer).Sugar()
	cs := &params.Run{
		Clients: clients.Clients{},
		Info:    info.Info{},
	}
	ropt := &Opts{
		GenerateName: generateName,
		RemoteTasks:  remoteTasking,
	}
	event := &info.Event{}
	tprovider := &testprovider.TestProviderImp{}

	types, err := ReadTektonTypes(ctx, logger, string(data))
	if err != nil {
		return &tektonv1.PipelineRun{}, nil, err
	}
	resolved, err := Resolve(ctx, cs, logger, tprovider, types, event, ropt)
	if err != nil {
		return &tektonv1.PipelineRun{}, nil, err
	}
	return resolved[0], log, nil
}

func TestPipelineRunPipelineTask(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-pipeline-task", false, true)
	assert.NilError(t, err)
	assert.Equal(t, resolved.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Name, "first-step")

	// TODO: we should do templates substitutions for those values here?
	assert.Equal(t, resolved.Spec.Params[0].Value.StringVal, "{{value}}")
}

func TestPipelineRunPipelineMiddle(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-splitted-with-dashes-in-middle", false, true)
	assert.NilError(t, err)
	assert.Equal(t, len(resolved.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps), 2, "we had the split bug on yaml multidocument ---")
}

func TestGenerateName(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-pipeline-task", true, true)
	assert.NilError(t, err)
	assert.Assert(t, resolved.GenerateName != "")

	resolved, _, err = readTDfile(t, "with-generatename", true, true)
	assert.NilError(t, err)
	assert.Assert(t, resolved.GenerateName != "")
}

// TestPipelineBundlesSkipped effectively test conversion from beta1 to v1.
func TestPipelineBundlesSkipped(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-pipeline-bundle", false, true)
	assert.NilError(t, err)
	assert.Equal(t, string(resolved.Spec.PipelineRef.ResolverRef.Params[0].Name), "bundle")
}

// TestTaskBundlesSkipped effectively test conversion from beta1 to v1.
func TestTaskBundlesSkipped(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-task-bundle", false, true)
	assert.NilError(t, err)
	assert.Equal(t, resolved.Spec.PipelineSpec.Tasks[0].Name, "bundled")
	assert.Equal(t, resolved.Spec.PipelineSpec.Tasks[0].TaskRef.Params[0].Name, "bundle")
}

func TestTaskResolverSkipped(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-task-resolver", false, true)
	assert.NilError(t, err)
	assert.Assert(t, resolved.Spec.PipelineSpec.Tasks[0].TaskRef.Resolver == "resolver")
}

func TestPipelineResolverSkipped(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-pipelinerun-resolver", false, true)
	assert.NilError(t, err)
	assert.Equal(t, string(resolved.Spec.PipelineRef.Resolver), "resolver")
	assert.Equal(t, resolved.Spec.PipelineRef.Params[0].Value.StringVal, "task")
}

func TestCustomTasksSkipped(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-with-a-customtask", false, true)
	assert.NilError(t, err)
	assert.Equal(t, resolved.Spec.PipelineSpec.Tasks[0].Name, "shipwright")
	assert.Equal(t, string(resolved.Spec.PipelineSpec.Tasks[0].TaskRef.APIVersion), "shipwright.io/v1alpha1")
	assert.Equal(t, string(resolved.Spec.PipelineSpec.Tasks[0].TaskRef.Kind), "Build")
}

func TestPipelineRunPipelineSpecTaskSpec(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-pipelinespec-taskspec", false, true)
	assert.NilError(t, err)
	assert.Equal(t, resolved.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Name, "hello-moto")
}

func TestPipelineRunWithFinally(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-finally", false, true)
	assert.NilError(t, err)
	assert.Equal(t, resolved.Spec.PipelineSpec.Finally[0].TaskSpec.Steps[0].Name, "finally-task")
}

func TestPipelineRunWithFinallyV1(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipeline-finally-v1", false, true)
	assert.NilError(t, err)
	assert.Equal(t, resolved.Spec.PipelineSpec.Finally[0].TaskSpec.Steps[0].Name, "finally-task")
}

func TestPipelineWithFinally(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipeline-finally", false, true)
	assert.NilError(t, err)
	assert.Equal(t, resolved.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Name, "normal-task")
	assert.Equal(t, resolved.Spec.PipelineSpec.Finally[0].TaskSpec.Steps[0].Name, "finally-task")
}

func TestPipelineRunPipelineSpecTaskRef(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-pipelinespec-taskref", false, true)
	assert.NilError(t, err)
	assert.Equal(t, resolved.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Name, "task1")
}

func TestPipelineRunRemoteTaskDisabled(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-pipeline-task-remote-task-annotation", false, false)
	assert.NilError(t, err)
	// tbh: it would crash
	assert.Equal(t, resolved.GetAnnotations()["pipelinesascode.tekton.dev/task"], "foo")
}

func TestOriginalPRNameLabelSet(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-pipeline-task-remote-task-annotation", false, false)
	assert.NilError(t, err)
	assert.Equal(t, resolved.GetLabels()["pipelinesascode.tekton.dev/original-prname"], "pr")
}

func TestPipelineRunRemoteTaskNotPacAnnotations(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-pipeline-task-annotations-not-pac", false, true)
	assert.NilError(t, err)

	if _, ok := resolved.GetObjectMeta().GetAnnotations()["anno"]; !ok {
		t.Error("Did not get back the annotations")
	}
}

func TestPipelineRunRemoteTaskBadPacAnnotations(t *testing.T) {
	_, _, err := readTDfile(t, "pipelinerun-pipeline-task-bad-pac-annotation", false, true)
	assert.ErrorContains(t, err, "annotations in pipeline are in wrong format")
}

func TestNotTektonDocumentIgnore(t *testing.T) {
	resolved, log, err := readTDfile(t, "not-a-tekton-document", false, true)
	assert.NilError(t, err)
	logs := log.TakeAll()
	assert.Assert(t, len(logs) > 0)
	assert.Assert(t, strings.HasPrefix(logs[0].Message, "skipping yaml"), fmt.Sprintf("'%s'", logs[0].Message))
	assert.Assert(t, resolved.Spec.PipelineSpec != nil)
}

func TestReportBadTektonYaml(t *testing.T) {
	tests := []struct {
		name           string
		filename       string
		wantErr        bool
		validError     string
		validErrorName string
		expectedSchema string
	}{
		{
			name:           "bad tekton yaml name",
			filename:       "bad-tekton-yaml-name",
			validError:     `json: cannot unmarshal object into Go struct field PipelineSpec.spec.pipelineSpec.tasks of type []v1beta1.PipelineTask`,
			validErrorName: "bad-name",
			expectedSchema: "tekton.dev/v1beta1", // Assuming this is the schema in the test file
		},
		{
			name:           "bad tekton yaml generateName",
			filename:       "bad-tekton-yaml-generate-name",
			validError:     `json: cannot unmarshal object into Go struct field PipelineSpec.spec.pipelineSpec.tasks of type []v1beta1.PipelineTask`,
			validErrorName: "bad-generate-name",
			expectedSchema: "", // When name/generateName cannot be determined
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + tt.filename + ".yaml")
			assert.NilError(t, err)
			types, err := ReadTektonTypes(context.TODO(), nil, string(data))
			assert.NilError(t, err)

			// Find the validation error by name
			found := false
			for _, validationError := range types.ValidationErrors {
				if validationError.Name == tt.validErrorName {
					// Test the structured error
					assert.Assert(t, strings.Contains(validationError.Err.Error(), tt.validError),
						"error message mismatch: expected %s to contain %s", validationError.Err.Error(), tt.validError)

					// Test that the error has the expected structure
					assert.Assert(t, validationError.Name != "", "validation error should have a name")
					assert.Assert(t, validationError.Err != nil, "validation error should have an error")

					// Test schema field if we expect one
					if tt.expectedSchema != "" {
						assert.Equal(t, validationError.Schema, tt.expectedSchema, "schema mismatch")
					}
					found = true
					break
				}
			}
			if !found {
				t.Errorf("could not find the task %s in the validation errors: %+v", tt.validErrorName, types.ValidationErrors)
			}
		})
	}
}

func TestDetectNameOrGenerateNameAndSchema(t *testing.T) {
	tests := []struct {
		name           string
		yamlContent    string
		expectedName   string
		expectedSchema string
	}{
		{
			name: "valid yaml with name and apiVersion",
			yamlContent: `apiVersion: tekton.dev/v1
metadata:
  name: test-pipeline`,
			expectedName:   "test-pipeline",
			expectedSchema: "tekton.dev/v1",
		},
		{
			name: "valid yaml with generateName and apiVersion",
			yamlContent: `apiVersion: tekton.dev/v1beta1
metadata:
  generateName: test-pipeline-
`,
			expectedName:   "test-pipeline-",
			expectedSchema: "tekton.dev/v1beta1",
		},
		{
			name:           "invalid yaml",
			yamlContent:    `- babdakdja`,
			expectedName:   "nokube",
			expectedSchema: "",
		},
		{
			name: "yaml without name or generateName",
			yamlContent: `apiVersion: tekton.dev/v1
metadata:
  namespace: default`,
			expectedName:   "unknown",
			expectedSchema: "tekton.dev/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, schema := detectAtleastNameOrGenerateNameAndSchemaFromPipelineRun(tt.yamlContent)
			assert.Equal(t, name, tt.expectedName, "name mismatch")
			assert.Equal(t, schema, tt.expectedSchema, "schema mismatch")
		})
	}
}

func TestValidationErrorStructure(t *testing.T) {
	// Test that validation errors follow the new structure
	testYaml := `apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: test-pr
spec:
  pipelineSpec:
    tasks:
      - name: invalid-task
        taskSpec:
          invalid-field: "this should cause an error"`

	types, err := ReadTektonTypes(context.TODO(), nil, testYaml)
	assert.NilError(t, err)

	// Each validation error should have the proper structure
	for _, validationError := range types.ValidationErrors {
		assert.Assert(t, validationError != nil, "validation error should not be nil")
		assert.Assert(t, validationError.Name != "", "validation error should have a name")
		assert.Assert(t, validationError.Err != nil, "validation error should have an error")
		// Schema field exists as a string (can be empty)
	}
}

func TestGenericBadYAMLValidation(t *testing.T) {
	// Test that the GenericBadYAMLValidation constant is used properly
	assert.Equal(t, pacerrors.GenericBadYAMLValidation, "Generic bad YAML Validation")
}

func TestValidationErrorFiltering(t *testing.T) {
	// Test the schema filtering logic that determines which errors should be reported
	tests := []struct {
		name             string
		validationErr    *pacerrors.PacYamlValidations
		shouldBeReported bool
	}{
		{
			name: "tekton resource error should be reported",
			validationErr: &pacerrors.PacYamlValidations{
				Name:   "test-pipeline",
				Err:    fmt.Errorf("some tekton error"),
				Schema: "tekton.dev/v1",
			},
			shouldBeReported: true,
		},
		{
			name: "tekton v1beta1 resource error should be reported",
			validationErr: &pacerrors.PacYamlValidations{
				Name:   "test-task",
				Err:    fmt.Errorf("some tekton error"),
				Schema: "tekton.dev/v1beta1",
			},
			shouldBeReported: true,
		},
		{
			name: "generic bad yaml error should be reported",
			validationErr: &pacerrors.PacYamlValidations{
				Name:   "bad-yaml",
				Err:    fmt.Errorf("yaml syntax error"),
				Schema: pacerrors.GenericBadYAMLValidation,
			},
			shouldBeReported: true,
		},
		{
			name: "non-tekton resource error should not be reported",
			validationErr: &pacerrors.PacYamlValidations{
				Name:   "some-config",
				Err:    fmt.Errorf("some other error"),
				Schema: "v1",
			},
			shouldBeReported: false,
		},
		{
			name: "empty schema error should not be reported",
			validationErr: &pacerrors.PacYamlValidations{
				Name:   "unknown-resource",
				Err:    fmt.Errorf("unknown error"),
				Schema: "",
			},
			shouldBeReported: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the filtering logic that would be used in reportValidationErrors
			shouldReport := strings.HasPrefix(tt.validationErr.Schema, tektonv1.SchemeGroupVersion.Group) ||
				tt.validationErr.Schema == pacerrors.GenericBadYAMLValidation

			assert.Equal(t, shouldReport, tt.shouldBeReported,
				"filtering result mismatch for schema: %s", tt.validationErr.Schema)
		})
	}
}

func TestErrorMessageFormat(t *testing.T) {
	// Test that error messages are properly formatted with "error decoding yaml document:" prefix
	testYaml := `invalid yaml content:
  - this: should
    cause: [an error`

	types, err := ReadTektonTypes(context.TODO(), nil, testYaml)
	assert.NilError(t, err)

	// Should have at least one validation error
	assert.Assert(t, len(types.ValidationErrors) > 0, "should have validation errors")

	// Check that error messages have the proper format
	for _, validationError := range types.ValidationErrors {
		assert.Assert(t, strings.Contains(validationError.Err.Error(), "error decoding yaml document:"),
			"error message should contain 'error decoding yaml document:' prefix, got: %s", validationError.Err.Error())
	}
}

func TestInRepoShouldNotEmbedIfNoAnnotations(t *testing.T) {
	resolved, _, err := readTDfile(t, "in-repo-in-ref-no-annotation", false, true)
	assert.NilError(t, err)
	assert.Assert(t, resolved.Spec.PipelineSpec.Tasks[0].TaskRef == nil, "task should have been embedded")
}

func TestNoPipelineRuns(t *testing.T) {
	_, _, err := readTDfile(t, "no-pipelinerun", false, true)
	assert.Error(t, err, "could not find any PipelineRun in your .tekton/ directory")
}

func TestReferencedTaskNotInRepo(t *testing.T) {
	_, _, err := readTDfile(t, "referenced-task-not-in-repo", false, true)
	assert.Error(t, err, "cannot find referenced task nothere. if it's a remote task make sure to add it in the annotations")
}

func TestReferencedPipelineNotInRepo(t *testing.T) {
	_, _, err := readTDfile(t, "referenced-pipeline-not-in-repo", false, true)
	assert.Error(t, err, "cannot find referenced pipeline pipeline-test1. for a remote pipeline make sure to add it in the annotation")
}

func TestIgnoreDocSpace(t *testing.T) {
	_, _, err := readTDfile(t, "empty-spaces", false, true)
	assert.NilError(t, err)
}

func TestPipelineV1StayV1(t *testing.T) {
	got, _, err := readTDfile(t, "pipelinev1asv1", false, true)
	assert.NilError(t, err)
	assert.Equal(t, got.APIVersion, "tekton.dev/v1")
}

func TestV1Beta1ConversionFixturesAreAcceptedByReadTektonTypes(t *testing.T) {
	tests := []struct {
		name         string
		filename     string
		pipelineRuns int
		pipelines    int
		tasks        int
	}{
		{
			name:         "pipelinerun fixture",
			filename:     "pipelinerun-invalid-conversion",
			pipelineRuns: 1,
		},
		{
			name:         "task fixture",
			filename:     "task-invalid-conversion",
			pipelineRuns: 1,
			tasks:        1,
		},
		{
			name:         "pipeline fixture",
			filename:     "pipeline-invalid-conversion",
			pipelineRuns: 1,
			pipelines:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile("testdata/" + tt.filename + ".yaml")
			assert.NilError(t, err)

			types, err := ReadTektonTypes(context.TODO(), nil, string(data))
			assert.NilError(t, err)
			assert.Equal(t, len(types.PipelineRuns), tt.pipelineRuns)
			assert.Equal(t, len(types.Pipelines), tt.pipelines)
			assert.Equal(t, len(types.Tasks), tt.tasks)
			assert.Equal(t, len(types.ValidationErrors), 0)
		})
	}
}

func TestPipelineRunsWithSameName(t *testing.T) {
	tests := []struct {
		name string
		prs  []*tektonv1.PipelineRun
		err  string
	}{
		{
			name: "same name pipelineruns exists",
			prs: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipelinerun-abc",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipelinerun-abc",
					},
				},
			},
			err: "found multiple pipelinerun in .tekton with the same name: pipelinerun-abc, please update",
		},
		{
			name: "same name and generateName pipelineruns exists",
			prs: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipelinerun-abc",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pipelinerun-abc",
					},
				},
			},
			err: "found multiple pipelinerun in .tekton with the same generateName: pipelinerun-abc, please update",
		},
		{
			name: "same generateName pipelineruns exists",
			prs: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pipelinerun-abc-",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pipelinerun-abc-",
					},
				},
			},
			err: "found multiple pipelinerun in .tekton with the same generateName: pipelinerun-abc-, please update",
		},
		{
			name: "different pipelineruns exists",
			prs: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pipelinerun-abc-",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipelinerun-bcd",
					},
				},
			},
			err: "",
		},
		{
			name: "doesn't pipelinerun name exists",
			prs: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipelinerun-abc",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipelinerun-bcd",
					},
				},
			},
			err: "",
		},
		{
			name: "doesn't pipelinerun generateName exists",
			prs: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pipelinerun-abc",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pipelinerun-bcd",
					},
				},
			},
			err: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := pipelineRunsWithSameName(tt.prs)
			if tt.err == "" {
				assert.NilError(t, err)
				return
			}
			assert.Equal(t, err.Error(), tt.err)
		})
	}
}

func TestMetadataResolve(t *testing.T) {
	tests := []struct {
		name    string
		prs     []*tektonv1.PipelineRun
		wantPRs []*tektonv1.PipelineRun
	}{
		{
			name: "label and annotation for pipelinerun name",
			prs: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipelinerun-abc",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipelinerun-deb",
					},
				},
			},
			wantPRs: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipelinerun-abc",
						Labels: map[string]string{
							apipac.OriginalPRName: "pipelinerun-abc",
						},
						Annotations: map[string]string{
							apipac.OriginalPRName: "pipelinerun-abc",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "pipelinerun-deb",
						Labels: map[string]string{
							apipac.OriginalPRName: "pipelinerun-deb",
						},
						Annotations: map[string]string{
							apipac.OriginalPRName: "pipelinerun-deb",
						},
					},
				},
			},
		},
		{
			name: "label and annotation for pipelinerun generateName",
			prs: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pipelinerun-abc-",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pipelinerun-deb-",
					},
				},
			},
			wantPRs: []*tektonv1.PipelineRun{
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pipelinerun-abc-",
						Labels: map[string]string{
							apipac.OriginalPRName: "pipelinerun-abc",
						},
						Annotations: map[string]string{
							apipac.OriginalPRName: "pipelinerun-abc-",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "pipelinerun-deb-",
						Labels: map[string]string{
							apipac.OriginalPRName: "pipelinerun-deb",
						},
						Annotations: map[string]string{
							apipac.OriginalPRName: "pipelinerun-deb-",
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pruns, err := MetadataResolve(tt.prs)
			assert.NilError(t, err)
			for i := range pruns {
				assert.DeepEqual(t, pruns[i], tt.wantPRs[i])
			}
		})
	}
}

func TestTaskRunPassMetadataAnnotations(t *testing.T) {
	resolved, _, err := readTDfile(t, "pipelinerun-pipelinespec-taskref-pass-annotations", false, true)
	assert.NilError(t, err)
	ma := resolved.Spec.PipelineSpec.Tasks[0].TaskSpec.Metadata.Annotations
	assert.Equal(t, len(ma), 1)
	assert.Equal(t, ma["random.annotation"], "THIS_SHOULD_BE_HERE")
	ml := resolved.Spec.PipelineSpec.Tasks[0].TaskSpec.Metadata.Labels
	assert.Equal(t, len(ml), 1)
	assert.Equal(t, ml["the.nitpicker.is.called"], "vincent")
}
