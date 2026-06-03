package resolve

import (
	"context"
	"fmt"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/matcher"
)

type NamedItem interface {
	GetName() string
}

func alreadySeen[T NamedItem](items []T, item T) bool {
	for _, value := range items {
		if value.GetName() == item.GetName() {
			return true
		}
	}
	return false
}

// getRemotes will get remote tasks or Pipelines from annotations.
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
func getRemotes(ctx context.Context, rt *matcher.RemoteTasks, types TektonTypes) (TektonTypes, error) {
	remoteType := &TektonTypes{}
	for _, pipelinerun := range types.PipelineRuns {
		if len(pipelinerun.GetObjectMeta().GetAnnotations()) == 0 {
			continue
		}

		// get first all the tasks from the pipelinerun annotations
		remoteTasks, err := rt.GetTaskFromAnnotations(ctx, pipelinerun.GetObjectMeta().GetAnnotations())
		if err != nil {
			return TektonTypes{}, fmt.Errorf("error getting remote task from pipelinerun annotations: %w", err)
		}

		for _, task := range remoteTasks {
			if alreadySeen(remoteType.Tasks, task) {
				rt.Logger.Debugf("skipping already fetched task %s in annotations on pipelinerun %s", task.GetName(), pipelinerun.GetName())
				continue
			}
			remoteType.Tasks = append(remoteType.Tasks, task.DeepCopy())
		}

		// get the pipeline from the remote annotation if any
		remotePipeline, err := rt.GetPipelineFromAnnotations(ctx, pipelinerun.GetObjectMeta().GetAnnotations())
		if err != nil {
			return TektonTypes{}, fmt.Errorf("error getting remote pipeline from pipelinerun annotation: %w", err)
		}

		if remotePipeline != nil {
			remoteType.Pipelines = append(remoteType.Pipelines, remotePipeline.DeepCopy())
		}
	}

	// grab the tasks from the remote pipeline
	for _, pipeline := range remoteType.Pipelines {
		if pipeline.GetObjectMeta().GetAnnotations() == nil {
			continue
		}
		remoteTasks, err := rt.GetTaskFromAnnotations(ctx, pipeline.GetObjectMeta().GetAnnotations())
		if err != nil {
			return TektonTypes{}, fmt.Errorf("error getting remote tasks from remote pipeline %s: %w", pipeline.GetName(), err)
		}

		for _, remoteTask := range remoteTasks {
			if alreadySeen(remoteType.Tasks, remoteTask) {
				rt.Logger.Infof("skipping remote task %s from remote pipeline %s as already defined in pipelinerun", remoteTask.GetName(), pipeline.GetName())
				continue
			}
			remoteType.Tasks = append(remoteType.Tasks, remoteTask.DeepCopy())
		}
	}

	ret := TektonTypes{
		PipelineRuns: types.PipelineRuns,
	}
	// first get the remote types and then the local ones so remote takes precedence
	for _, task := range append(remoteType.Tasks, types.Tasks...) {
		if alreadySeen(ret.Tasks, task) {
			rt.Logger.Infof("overriding task %s coming from tekton directory by an annotation task on the pipeline or pipelinerun", task.GetName())
			continue
		}
		ret.Tasks = append(ret.Tasks, task)
	}
	for _, remotePipeline := range append(remoteType.Pipelines, types.Pipelines...) {
		if alreadySeen(ret.Pipelines, remotePipeline) {
			rt.Logger.Infof("overriding pipeline %s coming from tekton directory by the annotation pipelinerun", remotePipeline.GetName())
			continue
		}
		ret.Pipelines = append(ret.Pipelines, remotePipeline)
	}
	return ret, nil
}
