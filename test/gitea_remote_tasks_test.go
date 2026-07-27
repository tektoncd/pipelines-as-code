//go:build e2e

package test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/tektoncd/pipeline/pkg/names"
	"gotest.tools/v3/assert"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
)

func TestGiteaPullRequestTaskAnnotations(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipeline.yaml":                        "testdata/pipeline_in_tektondir.yaml",
			".other-tasks/task-referenced-internally.yaml": "testdata/task_referenced_internally.yaml",
			".tekton/pr.yaml":                              "testdata/pipelinerun_remote_task_annotations.yaml",
		},
		CheckForStatus: "success",
		ExtraArgs: map[string]string{
			"RemoteTaskURL":  options.RemoteTaskURL,
			"RemoteTaskName": options.RemoteTaskName,
		},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

// TestGiteaGetTaskURI verifies that remote tasks hosted on the same Gitea
// instance are fetched using the provider's authenticated GetTaskURI path
// rather than falling back to unauthenticated HTTP.
func TestGiteaGetTaskURI(t *testing.T) {
	ctx := context.Background()
	runcnx, opts, giteacnx, err := tgitea.Setup(ctx)
	assert.NilError(t, err)

	remoteRepoName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("remote-task-repo")
	hookURL := os.Getenv("TEST_GITEA_SMEEURL")
	webhookSecret := os.Getenv("TEST_EL_WEBHOOK_SECRET")
	remoteRepo, err := tgitea.CreateGiteaRepo(
		giteacnx.Client(), opts.Organization,
		remoteRepoName, options.MainBranch, hookURL, webhookSecret,
		false, runcnx.Clients.Log,
	)
	assert.NilError(t, err)

	defer func() {
		if os.Getenv("TEST_NOCLEANUP") != "true" {
			_, _ = giteacnx.Client().DeleteRepo(opts.Organization, remoteRepoName)
		}
	}()

	taskFiles := []struct {
		remoteFile string
		refType    string
	}{
		{"task-branch.yaml", "branch"},
		{"task-tag.yaml", "tag"},
		{"task-commit.yaml", "commit"},
	}
	var commitSHA string
	for _, tf := range taskFiles {
		content := remoteTaskYAML(tf.refType)
		fr, _, createErr := giteacnx.Client().CreateFile(
			opts.Organization, remoteRepoName, tf.remoteFile,
			forgejo.CreateFileOptions{
				Content: base64.StdEncoding.EncodeToString([]byte(content)),
				FileOptions: forgejo.FileOptions{
					Message:    "Add " + tf.remoteFile,
					BranchName: options.MainBranch,
				},
			},
		)
		assert.NilError(t, createErr)
		if tf.refType == "commit" {
			commitSHA = fr.Commit.SHA
		}
	}

	tagName := "v0.0.1"
	_, _, err = giteacnx.Client().CreateTag(opts.Organization, remoteRepoName, forgejo.CreateTagOption{
		TagName: tagName,
		Target:  options.MainBranch,
	})
	assert.NilError(t, err)

	branchURL := fmt.Sprintf("%s/raw/branch/%s/task-branch.yaml", remoteRepo.HTMLURL, options.MainBranch)
	tagURL := fmt.Sprintf("%s/src/tag/%s/task-tag.yaml", remoteRepo.HTMLURL, tagName)
	commitURL := fmt.Sprintf("%s/raw/commit/%s/task-commit.yaml", remoteRepo.HTMLURL, commitSHA)

	runcnx.Clients.Log.Infof("Remote task URLs: branch=%s tag=%s commit=%s", branchURL, tagURL, commitURL)

	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun_remote_task_on_gitea.yaml",
		},
		CheckForStatus: "success",
		ExtraArgs: map[string]string{
			"RemoteTaskBranchURL": branchURL,
			"RemoteTaskTagURL":    tagURL,
			"RemoteTaskCommitURL": commitURL,
		},
		ParamsRun: runcnx,
		Opts:      opts,
		GiteaCNX:  giteacnx,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	for _, tf := range taskFiles {
		err = twait.RegexpMatchingInPodLog(ctx, runcnx, topts.TargetNS,
			fmt.Sprintf("tekton.dev/pipelineTask=task-from-%s", tf.refType),
			"step-echo",
			*regexp.MustCompile(fmt.Sprintf("Hello from %s ref", tf.refType)),
			"", 2, nil)
		assert.NilError(t, err, "task-from-%s did not produce expected log output", tf.refType)
	}
}

func TestGiteaUseDisplayName(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      regexp.MustCompile(`.*The Task name is Task.*`),
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun.yaml",
		},
		CheckForStatus: "success",
		ExtraArgs: map[string]string{
			"RemoteTaskURL":  options.RemoteTaskURL,
			"RemoteTaskName": options.RemoteTaskName,
		},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

func TestGiteaPullRequestPipelineAnnotations(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun_remote_pipeline_annotations.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "success",
		ExtraArgs: map[string]string{
			"RemoteTaskURL":  options.RemoteTaskURL,
			"RemoteTaskName": options.RemoteTaskName,
		},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

// TestGiteaPullRequestRemotePipelineRelativeTask verifies that relative task paths
// in a Pipeline's annotations are resolved correctly when the pipeline is
// fetched from a repository path.
func TestGiteaPullRequestRemotePipelineRelativeTask(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml":                        "testdata/pipelinerun_remote_pipeline_repo_path.yaml",
			".pipelines/pipeline.yaml":               "testdata/pipeline_relative_task.yaml",
			".tasks/task-referenced-internally.yaml": "testdata/task_referenced_internally.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "success",
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

func TestGiteaPullRequestResolvePipelineOnlyAssociatedWithPipelineRunFilterted(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr1.yaml":       "testdata/pipelinerun1_remote_task_annotations.yaml",
			".tekton/pr2.yaml":       "testdata/pipelinerun2_remote_task_annotations.yaml",
			".tekton/pipeline1.yaml": "testdata/pipeline1_in_tektondir.yaml",
			".tekton/pipeline2.yaml": "testdata/pipeline2_in_tektondir.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "success",
		ExtraArgs: map[string]string{
			"RemoteTaskURL":  options.RemoteTaskURL,
			"RemoteTaskName": options.RemoteTaskName,
		},
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

// TestGiteaPullRequestResolvedTektonParamsRemotePipeline see
// https://issues.redhat.com/browse/SRVKP-4070 for details
func TestGiteaPullRequestResolvedTektonParamsRemotePipeline(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml":       "testdata/pipelinerun_with_tekton_params.yaml",
			".tekton/pipeline.yaml": "testdata/pipeline_with_tekton_params.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "success",
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	// check the output of the PipelineRun logs
	err := twait.RegexpMatchingInPodLog(context.Background(),
		topts.ParamsRun,
		topts.TargetNS, "pipelinesascode.tekton.dev/event-type=pull_request", "step-task",
		*regexp.MustCompile("Hello " + topts.TargetRepoName), "", 2, nil)
	assert.NilError(t, err)
}

func TestGiteaStepActions(t *testing.T) {
	topts := &tgitea.TestOpts{
		Regexp:      tgitea.SuccessRegexp,
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pipelinerun-stepaction.yaml": "testdata/pipelinerun-stepactions.yaml",
		},
		ExpectEvents:   false,
		CheckForStatus: "success",
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
	tgitea.WaitForSecretDeletion(t, topts, topts.TargetRefName)
}

// TestGiteaHubTaskNotFound tests that we fail gracefully when a task is not
// found in the hub, or a specific version of a task is not found.
func TestGiteaHubTaskNotFound(t *testing.T) {
	tests := []struct {
		name     string
		yamlFile string
		errMsg   string
	}{
		{
			name:     "task not found",
			yamlFile: "testdata/pipelinerun-hub-task-not-found.yaml",
			errMsg:   ".*could not fetch remote task.*i-am-a-task-that-does-not-exist-i-hope.*",
		},
		{
			name:     "task version not found",
			yamlFile: "testdata/pipelinerun-hub-task-version-not-found.yaml",
			errMsg:   ".*could not fetch remote task.*git-clone:99.99.99.*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topts := &tgitea.TestOpts{
				TargetEvent:  triggertype.PullRequest.String(),
				YAMLFiles:    map[string]string{".tekton/pr.yaml": tt.yamlFile},
				ExpectEvents: true,
			}
			_, f := tgitea.TestPR(t, topts)
			defer f()
			topts.Regexp = regexp.MustCompile(tt.errMsg)
			tgitea.WaitForPullRequestCommentMatch(t, topts)
		})
	}
}

func remoteTaskYAML(refType string) string {
	return fmt.Sprintf(`apiVersion: tekton.dev/v1beta1
kind: Task
metadata:
  name: remote-task-gitea-%s
spec:
  steps:
    - name: echo
      image: registry.access.redhat.com/ubi10/ubi-micro
      script: |
        echo "Hello from %s ref"
`, refType, refType)
}
