//go:build e2e

package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/tektoncd/pipelines-as-code/pkg/params/triggertype"
	tgitlab "github.com/tektoncd/pipelines-as-code/test/pkg/gitlab"
	"github.com/tektoncd/pipelines-as-code/test/pkg/payload"
	"github.com/tektoncd/pipelines-as-code/test/pkg/scm"
	"github.com/tektoncd/pipelines-as-code/test/pkg/secret"
	"github.com/tektoncd/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Constants moved to test/github_incoming_test.go to avoid redeclaration

func TestGitlabIncomingWebhookLegacy(t *testing.T) {
	testGitlabIncomingWebhook(t, true)
}

func TestGitlabIncomingWebhookJsonBody(t *testing.T) {
	testGitlabIncomingWebhook(t, false)
}

func testGitlabIncomingWebhook(t *testing.T, useLegacy bool) {
	topts := &tgitlab.TestOpts{
		NoMRCreation: true,
		Incomings: &[]v1alpha1.Incoming{
			{
				Type: "webhook-url",
				Secret: v1alpha1.Secret{
					Name: incomingSecretName,
					Key:  "incoming",
				},
				Targets: []string{}, // filled in by TestMR
			},
		},
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	// Update the incoming target to match the actual TargetNS and recreate the CRD
	// The CRD was already created by TestMR, but we need the incoming secret
	err := secret.Create(ctx, topts.ParamsRun, map[string]string{"incoming": incomingSecreteValue}, topts.TargetNS, incomingSecretName)
	assert.NilError(t, err)

	// Push files for incoming webhook
	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming.yaml",
		".tekton/subdir/pr.yaml":            "testdata/pipelinerun-clone.yaml",
	}, topts.TargetNS, topts.TargetNS, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)

	title := "TestIncomingWebhook - " + topts.TargetNS
	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetNS,
		BaseRefName:   topts.DefaultBranch,
		CommitTitle:   title,
	}
	_ = scm.PushFilesToRefGit(t, scmOpts, entries)
	topts.ParamsRun.Clients.Log.Infof("Branch %s has been created and pushed with files", topts.TargetNS)

	var req *http.Request
	var incomingURL string
	client := &http.Client{}

	if useLegacy {
		incomingURL = fmt.Sprintf("%s/incoming?repository=%s&branch=%s&pipelinerun=%s&secret=%s",
			topts.Opts.ControllerURL, topts.TargetNS, topts.TargetNS, "pipelinerun-incoming", incomingSecreteValue)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, incomingURL, nil)
		assert.NilError(t, err)
	} else {
		incomingURL = fmt.Sprintf("%s/incoming", topts.Opts.ControllerURL)
		jsonBody := map[string]interface{}{
			"repository":  topts.TargetNS,
			"branch":      topts.TargetNS,
			"pipelinerun": "pipelinerun-incoming",
			"secret":      incomingSecreteValue,
		}
		jsonData, err := json.Marshal(jsonBody)
		assert.NilError(t, err)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, incomingURL, strings.NewReader(string(jsonData)))
		assert.NilError(t, err)
		req.Header.Add("Content-Type", "application/json")
	}

	httpResp, err := client.Do(req)
	assert.NilError(t, err)
	defer httpResp.Body.Close()
	topts.ParamsRun.Clients.Log.Infof("Kicked off on incoming-webhook URL: %s", incomingURL)
	assert.Assert(t, httpResp.StatusCode >= 200 && httpResp.StatusCode < 300)

	sopt := wait.SuccessOpt{
		Title:           title,
		OnEvent:         triggertype.Incoming.String(),
		TargetNS:        topts.TargetNS,
		NumberofPRMatch: 1,
		SHA:             "",
	}
	wait.Succeeded(ctx, t, topts.ParamsRun, topts.Opts, sopt)
	prsNew, err := topts.ParamsRun.Clients.Tekton.TektonV1().PipelineRuns(topts.TargetNS).List(ctx, metav1.ListOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(prsNew.Items) == 1)

	prName := prsNew.Items[0].GetName()
	index := strings.LastIndex(prsNew.Items[0].GetGenerateName(), "-")
	if index != -1 {
		prName = prsNew.Items[0].GetGenerateName()[:index]
	}
	assert.Assert(t, prName == "pipelinerun-incoming")
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -run TestGitlabIncomingWebhook$ ."
// End:
