//go:build e2e

package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	tlogs "github.com/openshift-pipelines/pipelines-as-code/test/pkg/podlogs"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/secret"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
)

const (
	provenanceFromDefaultBranch = "PROVENANCE_FROM_DEFAULT_BRANCH"
	provenanceFromTargetBranch  = "PROVENANCE_FROM_TARGET_BRANCH"
)

// provenanceTaskYaml returns a Task that echoes marker, so pod logs reveal
// which branch the Task was resolved from.
func provenanceTaskYaml(marker string) string {
	return fmt.Sprintf(`---
apiVersion: tekton.dev/v1
kind: Task
metadata:
  name: provenance-marker
spec:
  steps:
    - name: marker
      image: registry.access.redhat.com/ubi10/ubi-micro
      command: ["/bin/echo", "%s"]
`, marker)
}

// TestGiteaIncomingDefaultBranchProvenance verifies that incoming events with
// provenance "default_branch" resolve tasks from the default branch, not the
// target branch, and that DefaultBranch is backfilled from the API (incoming
// payloads don't carry it).
func TestGiteaIncomingDefaultBranchProvenance(t *testing.T) {
	targetBranch := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("incoming-branch")

	topts := &tgitea.TestOpts{
		TargetEvent:     triggertype.Incoming.String(),
		SkipEventsCheck: true,
		// TestPR returns before creating any branch; this test builds them.
		NoPullRequestCreation: true,
		Settings: &v1alpha1.Settings{
			PipelineRunProvenance: "default_branch",
		},
		Incomings: &[]v1alpha1.Incoming{
			{
				Type: "webhook-url",
				Secret: v1alpha1.Secret{
					Name: incomingSecretName,
					Key:  "incoming",
				},
				Targets: []string{targetBranch},
			},
		},
	}

	ctx, f := tgitea.TestPR(t, topts)
	defer f()

	assert.NilError(t, secret.Create(ctx, topts.ParamsRun,
		map[string]string{"incoming": incomingSecreteValue}, topts.TargetNS, incomingSecretName))

	// 1. Push PipelineRun + Task to the default branch.
	entries, err := payload.GetEntries(map[string]string{
		".tekton/pipelinerun-incoming.yaml": "testdata/pipelinerun-incoming-remote-task.yaml",
	}, topts.TargetNS, targetBranch, triggertype.Incoming.String(), map[string]string{})
	assert.NilError(t, err)
	entries[".tasks/incoming-task.yaml"] = provenanceTaskYaml(provenanceFromDefaultBranch)

	scm.PushFilesToRefGit(t, &scm.Opts{
		CommitTitle:   "Push pipelinerun and task to the default branch",
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.DefaultBranch,
		BaseRefName:   topts.DefaultBranch,
		GitURL:        topts.GitCloneURL,
	}, entries)

	// 2. Push only a decoy Task to the target branch — no .tekton/ dir.
	// NoCheckOutFromBase prevents inheriting the default branch's files.
	scm.PushFilesToRefGit(t, &scm.Opts{
		CommitTitle:        "Push a decoy task to the incoming target branch",
		Log:                topts.ParamsRun.Clients.Log,
		WebURL:             topts.GitHTMLURL,
		TargetRefName:      targetBranch,
		BaseRefName:        topts.DefaultBranch,
		NoCheckOutFromBase: true,
		GitURL:             topts.GitCloneURL,
	}, map[string]string{
		".tasks/incoming-task.yaml": provenanceTaskYaml(provenanceFromTargetBranch),
		"README.md":                 "# incoming target branch",
	})

	// 3. Assert target branch has no .tekton/; check status code because
	// the forgejo SDK can return a non-nil error even on 200.
	_, resp, err := topts.GiteaCNX.Client().ListContents(
		topts.Opts.Organization, topts.Opts.Repo, targetBranch, ".tekton",
	)
	assert.Assert(t, err != nil, "expected no .tekton directory on the target branch %s", targetBranch)
	assert.Assert(t, resp != nil && resp.StatusCode == http.StatusNotFound,
		"target branch %s must have no .tekton directory, got response %v", targetBranch, resp)

	// 4. Fire the incoming webhook.
	incomingURL := fmt.Sprintf("%s/incoming", topts.Opts.ControllerURL)
	jsonData, err := json.Marshal(map[string]any{
		"repository":  topts.TargetNS,
		"branch":      targetBranch,
		"pipelinerun": "pipelinerun-incoming-remote-task",
		"secret":      incomingSecreteValue,
	})
	assert.NilError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, incomingURL, strings.NewReader(string(jsonData)))
	assert.NilError(t, err)
	req.Header.Add("Content-Type", "application/json")

	httpResp, err := (&http.Client{}).Do(req)
	assert.NilError(t, err)
	defer httpResp.Body.Close()
	assert.Assert(t, httpResp.StatusCode >= 200 && httpResp.StatusCode < 300,
		"incoming webhook returned %d", httpResp.StatusCode)
	topts.ParamsRun.Clients.Log.Infof("Incoming webhook posted to %s, status %d", incomingURL, httpResp.StatusCode)

	// 5. Wait for a successful PipelineRun (covers DefaultBranch backfill).
	waitOpts := twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1,
		PollTimeout:     twait.DefaultTimeout,
	}
	_, err = twait.UntilPipelineRunHasReason(ctx, topts.ParamsRun.Clients,
		tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)

	// 6. Verify the Task came from the default branch.
	incomingSelector := "pipelinesascode.tekton.dev/event-type=" + triggertype.Incoming.String()
	assert.NilError(t, twait.RegexpMatchingInPodLog(ctx, topts.ParamsRun, topts.TargetNS,
		incomingSelector, "step-marker", *regexp.MustCompile(provenanceFromDefaultBranch), "", 10, nil))

	// Also confirm the target-branch marker is absent.
	numLines := int64(100)
	out, err := tlogs.GetPodLog(ctx, topts.ParamsRun.Clients.Kube.CoreV1(), topts.TargetNS,
		incomingSelector, "step-marker", &numLines, nil)
	assert.NilError(t, err)
	assert.Assert(t, !strings.Contains(out, provenanceFromTargetBranch),
		"task was resolved from the incoming target branch instead of the default branch, log: %s", out)
}
