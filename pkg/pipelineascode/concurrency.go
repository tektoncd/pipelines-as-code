package pipelineascode

import (
	"fmt"
	"sync"

	v1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipelines-as-code/pkg/sort"
	"k8s.io/apimachinery/pkg/runtime"
)

const namePath = "{.metadata.name}"

type ConcurrencyManager struct {
	enabled      bool
	pipelineRuns []*v1.PipelineRun
	mutex        *sync.Mutex
}

func NewConcurrencyManager() *ConcurrencyManager {
	return &ConcurrencyManager{
		pipelineRuns: []*v1.PipelineRun{},
		mutex:        &sync.Mutex{},
	}
}

func (c *ConcurrencyManager) AddPipelineRun(pr *v1.PipelineRun) {
	if !c.enabled {
		return
	}
	if pr == nil {
		return
	}
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.pipelineRuns = append(c.pipelineRuns, pr)
}

func (c *ConcurrencyManager) Enable() {
	c.enabled = true
}

func (c *ConcurrencyManager) GetExecutionOrder() (string, []*v1.PipelineRun) {
	if !c.enabled {
		return "", nil
	}

	if len(c.pipelineRuns) == 0 {
		return "", nil
	}

	runtimeObjs := []runtime.Object{}
	for _, pr := range c.pipelineRuns {
		if pr != nil && pr.Name != "" {
			runtimeObjs = append(runtimeObjs, pr)
		}
	}

	if len(runtimeObjs) == 0 {
		return "", nil
	}

	// sort runs by name
	sort.ByField(namePath, runtimeObjs)

	sortedPipelineRuns := []*v1.PipelineRun{}
	for _, run := range runtimeObjs {
		pr, _ := run.(*v1.PipelineRun)
		sortedPipelineRuns = append(sortedPipelineRuns, pr)
	}
	c.pipelineRuns = sortedPipelineRuns

	return getOrderByName(c.pipelineRuns), c.pipelineRuns
}

func getOrderByName(runs []*v1.PipelineRun) string {
	var order string
	for _, run := range runs {
		if order == "" {
			order = fmt.Sprintf("%s/%s", run.GetNamespace(), run.GetName())
			continue
		}
		order = order + "," + fmt.Sprintf("%s/%s", run.GetNamespace(), run.GetName())
	}
	return order
}
