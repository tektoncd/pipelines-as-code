package resolve

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	pacerrors "github.com/openshift-pipelines/pipelines-as-code/pkg/errors"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	"go.uber.org/zap"
	k8scheme "k8s.io/client-go/kubernetes/scheme"
	yaml "sigs.k8s.io/yaml/goyaml.v2"
)

// Contains Resources Fetched from tektondir.
type TektonTypes struct {
	PipelineRuns     []*tektonv1.PipelineRun
	Pipelines        []*tektonv1.Pipeline
	TaskRuns         []*tektonv1.TaskRun
	Tasks            []*tektonv1.Task
	ValidationErrors []*pacerrors.PacYamlValidations
}

// Contains Fetched Resources for Event, with key equals to annotation value.
type FetchedResources struct {
	Tasks     map[string]*tektonv1.Task
	Pipelines map[string]*tektonv1.Pipeline
}

// Contains Fetched Resources for Run, with key equals to resource name from metadata.name field.
type FetchedResourcesForRun struct {
	Tasks       map[string]*tektonv1.Task
	Pipeline    *tektonv1.Pipeline
	PipelineURL string
}

func NewTektonTypes() TektonTypes {
	return TektonTypes{
		ValidationErrors: []*pacerrors.PacYamlValidations{},
	}
}

var yamlDocSeparatorRe = regexp.MustCompile(`(?m)^---\s*$`)

// detectAtleastNameOrGenerateNameAndSchemaFromPipelineRun detects the name or
// generateName of a yaml files even if there is an error decoding it as tekton types.
func detectAtleastNameOrGenerateNameAndSchemaFromPipelineRun(data string) (string, string) {
	var genericKubeObj struct {
		APIVersion string `yaml:"apiVersion"`
		Metadata   struct {
			Name         string `yaml:"name,omitempty"`
			GenerateName string `yaml:"generateName,omitempty"`
		} `yaml:"metadata"`
	}
	err := yaml.Unmarshal([]byte(data), &genericKubeObj)
	if err != nil {
		return "nokube", ""
	}
	if genericKubeObj.Metadata.Name != "" {
		return genericKubeObj.Metadata.Name, genericKubeObj.APIVersion
	}

	if genericKubeObj.Metadata.GenerateName != "" {
		return genericKubeObj.Metadata.GenerateName, genericKubeObj.APIVersion
	}
	return "unknown", genericKubeObj.APIVersion
}

// getPipelineByName returns the Pipeline with the given name the first one found
// will be matched. It does not handle conflicts so user has fetched multiple
// pipeline with the same name it will just pick up the first one.
// if the pipeline is not found it returns an error.
func getPipelineByName(name string, tasks []*tektonv1.Pipeline) (*tektonv1.Pipeline, error) {
	for _, value := range tasks {
		if value.Name == name {
			return value, nil
		}
	}
	return &tektonv1.Pipeline{}, fmt.Errorf("cannot find referenced pipeline %s. for a remote pipeline make sure to add it in the annotation", name)
}

func pipelineRunsWithSameName(prs []*tektonv1.PipelineRun) error {
	prNames := map[string]bool{}
	for _, pr := range prs {
		name := pr.GetName()
		generateName := pr.GetGenerateName()

		if name != "" {
			if _, exist := prNames[name]; exist {
				return fmt.Errorf("found multiple pipelinerun in .tekton with the same name: %v, please update", name)
			}
			prNames[name] = true
		}

		if generateName != "" {
			if _, exist := prNames[generateName]; exist {
				return fmt.Errorf("found multiple pipelinerun in .tekton with the same generateName: %v, please update", generateName)
			}
			prNames[generateName] = true
		}
	}
	return nil
}

func isTektonAPIVersion(apiVersion string) bool {
	return strings.HasPrefix(apiVersion, "tekton.dev/") || apiVersion == ""
}

func inlineTasks(tasks []tektonv1.PipelineTask, ropt *Opts, remoteResource FetchedResourcesForRun) ([]tektonv1.PipelineTask, error) {
	pipelineTasks := []tektonv1.PipelineTask{}
	for _, task := range tasks {
		if task.TaskRef != nil &&
			task.TaskRef.Resolver == "" &&
			isTektonAPIVersion(task.TaskRef.APIVersion) &&
			string(task.TaskRef.Kind) != "ClusterTask" &&
			!slices.Contains(ropt.SkipInlining, task.TaskRef.Name) {
			taskResolved, ok := remoteResource.Tasks[task.TaskRef.Name]
			if !ok {
				return nil, fmt.Errorf("cannot find referenced task %s. if it's a remote task make sure to add it in the annotations", task.TaskRef.Name)
			}
			tmd := tektonv1.PipelineTaskMetadata{
				Annotations: taskResolved.GetObjectMeta().GetAnnotations(),
				Labels:      taskResolved.GetObjectMeta().GetLabels(),
			}
			task.TaskRef = nil
			task.TaskSpec = &tektonv1.EmbeddedTask{
				TaskSpec: taskResolved.Spec,
				Metadata: tmd,
			}
		}
		pipelineTasks = append(pipelineTasks, task)
	}
	return pipelineTasks, nil
}

type Opts struct {
	GenerateName  bool     // whether to GenerateName
	RemoteTasks   bool     // whether to parse annotation to fetch tasks from remote
	SkipInlining  []string // task to skip inlining
	ProviderToken string
}

func ReadTektonTypes(ctx context.Context, log *zap.SugaredLogger, data string) (TektonTypes, error) {
	types := NewTektonTypes()
	decoder := k8scheme.Codecs.UniversalDeserializer()
	docs := yamlDocSeparatorRe.Split(data, -1)
	debugf(log, "ReadTektonTypes: data length=%d docs=%d", len(data), len(docs))

	for _, doc := range docs {
		if strings.TrimSpace(doc) == "" {
			continue
		}

		obj, _, err := decoder.Decode([]byte(doc), nil, nil)
		if err != nil {
			dt, dv := detectAtleastNameOrGenerateNameAndSchemaFromPipelineRun(doc)
			debugf(log, "ReadTektonTypes: decode error for doc name=%s schema=%s err=%v", dt, dv, err)
			types.ValidationErrors = append(types.ValidationErrors, &pacerrors.PacYamlValidations{
				Name:   dt,
				Err:    fmt.Errorf("error decoding yaml document: %w", err),
				Schema: dv,
			})
			continue
		}
		switch o := obj.(type) {
		case *tektonv1beta1.Pipeline: //nolint: staticcheck // we need to support v1beta1
			c := &tektonv1.Pipeline{}
			if err := o.ConvertTo(ctx, c); err != nil {
				return types, fmt.Errorf("pipeline v1beta1 %s cannot be converted as v1: err: %w", o.GetName(), err)
			}
			types.Pipelines = append(types.Pipelines, c)
			debugf(log, "ReadTektonTypes: loaded pipeline v1beta1 name=%s", c.GetName())
		case *tektonv1beta1.PipelineRun: //nolint: staticcheck // we need to support v1beta1
			c := &tektonv1.PipelineRun{}
			if err := o.ConvertTo(ctx, c); err != nil {
				return types, fmt.Errorf("pipelinerun v1beta1 %s cannot be converted as v1: err: %w", o.GetName(), err)
			}
			types.PipelineRuns = append(types.PipelineRuns, c)
			debugf(log, "ReadTektonTypes: loaded pipelinerun v1beta1 name=%s", c.GetName())
		case *tektonv1beta1.Task: //nolint: staticcheck // we need to support v1beta1
			c := &tektonv1.Task{}
			if err := o.ConvertTo(ctx, c); err != nil {
				return types, fmt.Errorf("task v1beta1 %s cannot be converted as v1: err: %w", o.GetName(), err)
			}
			types.Tasks = append(types.Tasks, c)
			debugf(log, "ReadTektonTypes: loaded task v1beta1 name=%s", c.GetName())
		case *tektonv1.PipelineRun:
			types.PipelineRuns = append(types.PipelineRuns, o)
			debugf(log, "ReadTektonTypes: loaded pipelinerun v1 name=%s", o.GetName())
		case *tektonv1.Pipeline:
			types.Pipelines = append(types.Pipelines, o)
			debugf(log, "ReadTektonTypes: loaded pipeline v1 name=%s", o.GetName())
		case *tektonv1.Task:
			types.Tasks = append(types.Tasks, o)
			debugf(log, "ReadTektonTypes: loaded task v1 name=%s", o.GetName())
		default:
			logInfo(log, "skipping yaml document not looking like a tekton resource we can Resolve.")
		}
	}

	debugf(log, "ReadTektonTypes: result pipelineruns=%d pipelines=%d tasks=%d validation_errors=%d", len(types.PipelineRuns), len(types.Pipelines), len(types.Tasks), len(types.ValidationErrors))
	return types, nil
}

// Resolve gets a large string which is a yaml multi documents containing
// Pipeline/PipelineRuns/Tasks and resolve them inline as a single PipelineRun
// generateName can be set as True to set the name as a generateName + "-" for
// unique pipelinerun.
func Resolve(ctx context.Context, cs *params.Run, logger *zap.SugaredLogger, providerintf provider.Interface, types TektonTypes, event *info.Event, ropt *Opts) ([]*tektonv1.PipelineRun, error) {
	if len(types.PipelineRuns) == 0 {
		return []*tektonv1.PipelineRun{}, fmt.Errorf("could not find any PipelineRun in your .tekton/ directory")
	}
	debugf(logger, "Resolve: pipelineruns=%d pipelines=%d tasks=%d remote_tasks=%t generate_name=%t", len(types.PipelineRuns), len(types.Pipelines), len(types.Tasks), ropt.RemoteTasks, ropt.GenerateName)

	if _, err := MetadataResolve(types.PipelineRuns); err != nil {
		return []*tektonv1.PipelineRun{}, err
	}

	rt := &matcher.RemoteTasks{
		Run:               cs,
		Event:             event,
		ProviderInterface: providerintf,
		Logger:            logger,
	}

	fetchedResources, err := resolveRemoteResources(ctx, rt, types, ropt)
	if err != nil {
		return []*tektonv1.PipelineRun{}, err
	}
	debugf(logger, "Resolve: resolved pipelineruns=%d", len(fetchedResources))
	return fetchedResources, nil
}

func MetadataResolve(prs []*tektonv1.PipelineRun) ([]*tektonv1.PipelineRun, error) {
	if err := pipelineRunsWithSameName(prs); err != nil {
		return []*tektonv1.PipelineRun{}, err
	}

	for _, prun := range prs {
		originPipelineRunName := prun.GetName()
		if originPipelineRunName == "" && prun.GenerateName != "" {
			originPipelineRunName = prun.GetGenerateName()
		}

		// keep the originalPipelineRun in a label
		// because we would need it later on when grouping by cleanups and we
		// can attach that pr file from .tekton directory.

		// Don't overwrite the labels if there is some who already exist set by the user in repo
		if prun.GetLabels() == nil {
			prun.Labels = map[string]string{}
		}
		// Don't overwrite the annotation if there is some who already exist set by the user in repo
		if prun.GetAnnotations() == nil {
			prun.Annotations = map[string]string{}
		}
		prun.GetLabels()[apipac.OriginalPRName] = formatting.CleanValueKubernetes(originPipelineRunName)
		prun.GetAnnotations()[apipac.OriginalPRName] = originPipelineRunName
	}
	return prs, nil
}

//nolint:gochecknoinits
func init() {
	_ = tektonv1.AddToScheme(k8scheme.Scheme)
	_ = tektonv1beta1.AddToScheme(k8scheme.Scheme)
}
