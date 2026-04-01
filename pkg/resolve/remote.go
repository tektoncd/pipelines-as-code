package resolve

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

type NamedItem interface {
	GetName() string
}

func alreadyFetchedResource[T NamedItem](resources map[string]T, resourceName string) bool {
	if _, ok := resources[resourceName]; ok {
		return true
	}
	return false
}

// Tries to assemble task FQDNs based on the base URL
// of a remote pipeline.
//
// If there isn't a remote pipeline reference for the current
// run, tasks are returned as they are. Any task with an already
// valid URL is skipped.
func assembleTaskFQDNs(pipelineURL string, tasks []string) ([]string, error) {
	if pipelineURL == "" {
		return tasks, nil // no pipeline URL, return tasks as is
	}

	// Only HTTP(S) URLs and repository file paths can serve as base for relative task resolution.
	// Hub catalog references (e.g., "catalog://resource:version") use a
	// different scheme where relative paths are meaningless.
	lowered := strings.ToLower(pipelineURL)
	isHTTP := strings.HasPrefix(lowered, "http://") || strings.HasPrefix(lowered, "https://")
	isRepoPath := strings.Contains(lowered, "/") && !strings.Contains(lowered, "://")

	if !isHTTP && !isRepoPath {
		return tasks, nil
	}

	pURL, err := url.Parse(pipelineURL)
	if err != nil {
		return tasks, err
	}
	// pop the pipeline file path from the URL
	pURL.Path = path.Dir(pURL.Path)

	taskURLS := make([]string, len(tasks))
	for i, t := range tasks {
		tURL, err := url.Parse(t)
		if err == nil && tURL.Scheme != "" && tURL.Host != "" {
			taskURLS[i] = t
			continue // it's already an absolute URL
		}
		tURL = pURL
		tURL = tURL.JoinPath(t)
		taskURLS[i] = tURL.String()
	}
	return taskURLS, nil
}

// resolveRemoteResources will get remote tasks or Pipelines from annotations.
//
// It already has some tasks or pipeline coming from the tekton directory stored in [types]
//
// The precedence logic for tasks is in this order:
//
// * Tasks from the PipelineRun annotations
// * Tasks from the Pipeline annotations
// * Tasks from the Tekton directory
//
// The precedence logic for Pipeline is first from PipelineRun annotations and
// then from Tekton directory.
func resolveRemoteResources(ctx context.Context, rt *matcher.RemoteTasks, types TektonTypes, ropt *Opts) ([]*tektonv1.PipelineRun, error) {
	// contain Resources fetched for the event
	fetchedResourcesForEvent := FetchedResources{
		Tasks:     map[string]*tektonv1.Task{},
		Pipelines: map[string]*tektonv1.Pipeline{},
	}
	pipelineRuns := []*tektonv1.PipelineRun{}
	for _, pipelinerun := range types.PipelineRuns {
		// contain Resources specific to run
		fetchedResourcesForPipelineRun := FetchedResourcesForRun{
			Tasks:       map[string]*tektonv1.Task{},
			PipelineURL: "",
		}
		var pipeline *tektonv1.Pipeline
		var err error
		if ropt.RemoteTasks {
			// no annotations on run, then skip
			if pipelinerun.GetObjectMeta().GetAnnotations() == nil {
				continue
			}

			if len(pipelinerun.GetObjectMeta().GetAnnotations()) == 0 {
				continue
			}

			// get first all the pipeline from the pipelinerun annotations
			remotePipeline, err := matcher.GrabPipelineFromAnnotations(pipelinerun.GetObjectMeta().GetAnnotations())
			if err != nil {
				return []*tektonv1.PipelineRun{}, fmt.Errorf("error getting remote pipeline from pipelinerun annotations: %w", err)
			}

			// if we got the pipeline name from annotation, we need to fetch the pipeline
			if remotePipeline != "" {
				// making sure that the pipeline with same annotation name is not fetched
				if alreadyFetchedResource(fetchedResourcesForEvent.Pipelines, remotePipeline) {
					rt.Logger.Debugf("skipping already fetched pipeline %s in annotations on pipelinerun %s", remotePipeline, pipelinerun.GetName())
					// already fetched, then just get the pipeline to add to run specific Resources
					pipeline = fetchedResourcesForEvent.Pipelines[remotePipeline]
				} else {
					// seems like a new pipeline, fetch it based on name in annotation
					pipeline, err = rt.GetPipelineFromAnnotationName(ctx, remotePipeline)
					if err != nil {
						return []*tektonv1.PipelineRun{}, fmt.Errorf("error getting remote pipeline from pipelinerun annotations: %w", err)
					}
					// add the pipeline to the Resources fetched for the Event
					fetchedResourcesForEvent.Pipelines[remotePipeline] = pipeline
					// add the pipeline URL to the run specific Resources
					fetchedResourcesForPipelineRun.PipelineURL = remotePipeline
				}
			}
		}
		pipelineTasks := []string{}
		// if run is referring to the pipelineRef and pipeline fetched from annotation have name equal to the pipelineRef
		if pipelinerun.Spec.PipelineRef != nil && pipelinerun.Spec.PipelineRef.Resolver == "" {
			if pipeline == nil || pipeline.Name != pipelinerun.Spec.PipelineRef.Name {
				// if pipeline fetched from annotation is not having same name as PipelineRef, then we need to get a local pipeline if exist by same name
				pipeline, err = getPipelineByName(pipelinerun.Spec.PipelineRef.Name, types.Pipelines)
				if err != nil {
					return []*tektonv1.PipelineRun{}, err
				}
			}
			// add the pipeline to the run specific Resources
			fetchedResourcesForPipelineRun.Pipeline = pipeline
			// grab the tasks, that we may need to fetch for this pipeline from its annotations
			if pipeline.GetObjectMeta().GetAnnotations() != nil {
				// get all the tasks from the pipeline annotations
				pipelineTasks, err = matcher.GrabTasksFromAnnotations(pipeline.GetObjectMeta().GetAnnotations())
				if err != nil {
					return []*tektonv1.PipelineRun{}, fmt.Errorf("error getting remote task from pipeline annotations: %w", err)
				}
				// check for relative task references and assemble FQDNs
				pipelineTasks, err = assembleTaskFQDNs(fetchedResourcesForPipelineRun.PipelineURL, pipelineTasks)
				if err != nil {
					return []*tektonv1.PipelineRun{}, err
				}
			}
		}

		// now start fetching the tasks
		if ropt.RemoteTasks {
			// first get all the tasks from the pipelinerun annotations
			remoteTasks, err := matcher.GrabTasksFromAnnotations(pipelinerun.GetObjectMeta().GetAnnotations())
			if err != nil {
				return []*tektonv1.PipelineRun{}, fmt.Errorf("error getting remote task from pipelinerun annotations: %w", err)
			}

			// now fetch all the tasks from pipelinerun and pipeline annotations, giving preference to pipelinerun annotation tasks
			for _, remoteTask := range append(remoteTasks, pipelineTasks...) {
				var task *tektonv1.Task
				// if task is already fetched in the event, then just copy the task
				if alreadyFetchedResource(fetchedResourcesForEvent.Tasks, remoteTask) {
					rt.Logger.Debugf("skipping already fetched task %s in annotations on pipelinerun %s", remoteTask, pipelinerun.GetName())
					task = fetchedResourcesForEvent.Tasks[remoteTask]
				} else {
					// get the task from annotation name
					task, err = rt.GetTaskFromAnnotationName(ctx, remoteTask)
					if err != nil {
						return []*tektonv1.PipelineRun{}, fmt.Errorf("error getting remote task from pipelinerun annotations: %w", err)
					}
					// add the newly fetched tasks to fetchedResourcesForEvent with key annotation value
					fetchedResourcesForEvent.Tasks[remoteTask] = task
				}
				// now checking if run specific resources already contain a task with same name, then don't add it
				// this is to give preference to the pipelinerun annotation then pipeline annotation
				if !alreadyFetchedResource(fetchedResourcesForPipelineRun.Tasks, task.GetName()) {
					rt.Logger.Infof("skipping remote task %s as already fetched task %s for pipelinerun %s", remoteTask, task.GetName(), pipelinerun.GetName())
					fetchedResourcesForPipelineRun.Tasks[task.GetName()] = task
				}
			}
		}

		// now add all the tasks in .tekton directory to Tasks, as we add them by default if not found in annotation
		// we will skip the ones which exist in run specific resources with same name
		for _, task := range types.Tasks {
			if alreadyFetchedResource(fetchedResourcesForPipelineRun.Tasks, task.GetName()) {
				rt.Logger.Infof("overriding task %s coming from .tekton directory by an annotation task for pipelinerun %s", task.GetName(), pipelinerun.GetName())
				continue
			}
			fetchedResourcesForPipelineRun.Tasks[task.GetName()] = task
		}

		// if PipelineRef is used then, first resolve pipeline and replace all taskRef{Finally/Task} of Pipeline, then put inlinePipeline in PipelineRun
		if pipelinerun.Spec.PipelineRef != nil && pipelinerun.Spec.PipelineRef.Resolver == "" {
			pipelineResolved := fetchedResourcesForPipelineRun.Pipeline
			turns, err := inlineTasks(pipelineResolved.Spec.Tasks, ropt, fetchedResourcesForPipelineRun)
			if err != nil {
				return nil, err
			}
			pipelineResolved.Spec.Tasks = turns

			fruns, err := inlineTasks(pipelineResolved.Spec.Finally, ropt, fetchedResourcesForPipelineRun)
			if err != nil {
				return nil, err
			}
			pipelineResolved.Spec.Finally = fruns

			pipelinerun.Spec.PipelineRef = nil
			pipelinerun.Spec.PipelineSpec = &pipelineResolved.Spec
		}

		// if PipelineSpec is used then, now resolve the PipelineRun by replacing all taskRef{Finally/Task}
		if pipelinerun.Spec.PipelineSpec != nil {
			turns, err := inlineTasks(pipelinerun.Spec.PipelineSpec.Tasks, ropt, fetchedResourcesForPipelineRun)
			if err != nil {
				return nil, err
			}
			pipelinerun.Spec.PipelineSpec.Tasks = turns

			fruns, err := inlineTasks(pipelinerun.Spec.PipelineSpec.Finally, ropt, fetchedResourcesForPipelineRun)
			if err != nil {
				return nil, err
			}
			pipelinerun.Spec.PipelineSpec.Finally = fruns
		}

		// Add a GenerateName based on the pipeline name and a "-"
		// if we already have a GenerateName then just keep it like this
		if ropt.GenerateName && pipelinerun.GenerateName == "" {
			pipelinerun.GenerateName = pipelinerun.Name + "-"
			pipelinerun.Name = ""
		}
		pipelineRuns = append(pipelineRuns, pipelinerun)
	}
	// return all resolved PipelineRuns
	return pipelineRuns, nil
}
