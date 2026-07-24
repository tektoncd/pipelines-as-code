package wait

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	"knative.dev/pkg/apis"
)

var DefaultTimeout = 10 * time.Minute

type SuccessOpt struct {
	TargetNS        string
	OnEvent         string
	SHA             string
	Title           string
	MinNumberStatus int

	NumberofPRMatch int
}

func Succeeded(ctx context.Context, t *testing.T, runcnx *params.Run, opts options.E2E, sopt SuccessOpt) {
	t.Helper()
	runcnx.Clients.Log.Infof("Waiting for PipelineRuns to succeed")
	minNumberStatus := sopt.MinNumberStatus
	if minNumberStatus == 0 {
		minNumberStatus = sopt.NumberofPRMatch
	}
	var targetSHA []string
	if sopt.SHA != "" {
		targetSHA = []string{sopt.SHA}
	}
	waitOpts := Opts{
		Namespace:       sopt.TargetNS,
		MinNumberStatus: minNumberStatus,
		PollTimeout:     DefaultTimeout,
		TargetSHA:       targetSHA,
		TargetEventType: sopt.OnEvent,
	}
	prs, err := UntilPipelineRunsFinished(ctx, runcnx.Clients, waitOpts)
	assert.NilError(t, err)

	assert.Assert(t, len(prs) > 0, "no successful pipelineruns found")

	var pr tektonv1.PipelineRun
	if sopt.SHA != "" {
		found := false
		for i := len(prs) - 1; i >= 0; i-- {
			if prs[i].Annotations[keys.SHA] == sopt.SHA {
				pr = prs[i]
				found = true
				break
			}
		}
		if !found {
			availableSHAs := make([]string, 0, len(prs))
			for _, p := range prs {
				availableSHAs = append(availableSHAs, p.Annotations[keys.SHA])
			}
			assert.Assert(t, false, "no matching pipelinerun found for SHA %s; available SHAs: %v", sopt.SHA, availableSHAs)
		}
	} else {
		pr = prs[len(prs)-1]
	}

	runcnx.Clients.Log.Infof("Check if we have the pipelinerun set as succeeded")
	cond := pr.Status.GetCondition(apis.ConditionSucceeded)
	assert.Assert(t, cond != nil)
	assert.Equal(t, corev1.ConditionTrue, cond.Status)
	if sopt.SHA != "" {
		assert.Equal(t, sopt.SHA, pr.Annotations[keys.SHA])
		assert.Equal(t, sopt.SHA, filepath.Base(pr.Annotations[keys.ShaURL]))
	}
	shaTitle := strings.TrimSpace(pr.Annotations[keys.ShaTitle])
	if sopt.Title != "" {
		assert.Equal(t, sopt.Title, shaTitle)
	} else {
		assert.Assert(t, pr.Annotations[keys.ShaTitle] != "")
	}
	assert.Assert(t, pr.Annotations[keys.LogURL] != "")

	assert.Equal(t, sopt.OnEvent, pr.Annotations[keys.EventType])
	assert.Equal(t, sopt.TargetNS, pr.Annotations[keys.Repository])

	if opts.Organization != "" {
		assert.Equal(t, opts.Organization, pr.Annotations[keys.URLOrg])
	}
	if opts.Repo != "" {
		assert.Equal(t, opts.Repo, pr.Annotations[keys.URLRepository])
	}
	assert.Equal(t, shaTitle, strings.TrimSpace(pr.Annotations[keys.ShaTitle]))
	runcnx.Clients.Log.Infof("Success, number of pipelineruns %d has been matched", sopt.NumberofPRMatch)
}
