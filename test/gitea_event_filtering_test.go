//go:build e2e

package test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/triggertype"
	tgitea "github.com/openshift-pipelines/pipelines-as-code/test/pkg/gitea"
	twait "github.com/openshift-pipelines/pipelines-as-code/test/pkg/wait"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
)

func TestGiteaOnPathChange(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml":       "testdata/pipelinerun-on-path-change.yaml",
			"doc/foo/bar/README.md": "README.md",
		},
		CheckForStatus: "success",
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

func TestGiteaBranchWithComma(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent:   triggertype.PullRequest.String(),
		DefaultBranch: "branch,with,comma",
		YAMLFiles: map[string]string{
			".tekton/pr.yaml": "testdata/pipelinerun-target-branch-with-comma.yaml",
		},
		CheckForStatus: "success",
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

// TestGiteaOnPathChangeIgnore will test that pipelinerun is not triggered when
// a path is ignored but all other will do.
func TestGiteaOnPathChangeIgnore(t *testing.T) {
	// This should trigger a pipelinerun since we ignore the path
	// on-path-change-ignore: "[doc/foo/***.md]"
	// and we create a file doc/bar/README.md
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr2.yaml":  "testdata/pipelinerun-on-path-change-ignore.yaml",
			"doc/bar/README.md": "README.md",
		},
		CheckForStatus:       "success",
		CheckForNumberStatus: 1,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	// This should not trigger a pipelinerun since we have
	// on-path-change-ignore: "[doc/foo/***.md]"
	// and the file doc/foo/README.md is created
	topts2 := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr2.yaml":  "testdata/pipelinerun-on-path-change-ignore.yaml",
			"doc/foo/README.md": "README.md",
		},
		CheckForNumberStatus: 0,
	}
	_, f2 := tgitea.TestPR(t, topts2)
	defer f2()
}

// TestGiteaOnPathChangeAndOnPathChangeIgnore will test that
// on-path-change and on-path-change-ignore both work together.
func TestGiteaOnPathChangeAndOnPathChangeIgnore(t *testing.T) {
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			".tekton/pr.yaml":       "testdata/pipelinerun-on-path-change.yaml",
			".tekton/pr2.yaml":      "testdata/pipelinerun-on-path-change-and-ignore.yaml",
			"doc/foo/bar/README.md": "README.md",
		},
		CheckForStatus:       "success",
		CheckForNumberStatus: 1,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()
}

func TestGiteaOnPullRequestLabels(t *testing.T) {
	prName := "on-label"
	topts := &tgitea.TestOpts{
		TargetEvent: triggertype.PullRequest.String(),
		YAMLFiles: map[string]string{
			fmt.Sprintf(".tekton/%s.yaml", prName): "testdata/pipelinerun-on-label.yaml",
		},
		ExpectEvents:         false,
		CheckForNumberStatus: 0,
	}
	_, f := tgitea.TestPR(t, topts)
	defer f()

	tgitea.AddLabelToIssue(t, topts, "bug")

	waitOpts := twait.Opts{
		Namespace:       topts.TargetNS,
		MinNumberStatus: 1, // 1 means 2 🙃
		PollTimeout:     twait.DefaultTimeout,
		TargetSHA:       []string{topts.PullRequest.Head.Sha},
	}
	prs, err := twait.UntilPipelineRunHasReason(context.Background(), topts.ParamsRun.Clients, tektonv1.PipelineRunReasonSuccessful, waitOpts)
	assert.NilError(t, err)

	topts.CheckForStatus = "success"
	tgitea.WaitForStatus(t, topts, topts.TargetRefName, "", true)

	assert.Equal(t, len(prs), 1, "should have only one pipelinerun, but we have: %d", len(prs))

	twait.GoldenPodLog(context.Background(), t, topts.ParamsRun, topts.TargetNS,
		fmt.Sprintf("tekton.dev/pipelineRun=%s,tekton.dev/pipelineTask=task", prs[0].GetName()),
		"step-success", strings.ReplaceAll(fmt.Sprintf("%s.golden", t.Name()), "/", "-"), 2, nil)

	// Make sure the on-label pr has triggered and post status
	topts.Regexp = regexp.MustCompile(fmt.Sprintf("Pipelines as Code CI/%s.* has <b>successfully</b> validated your commit", prName))
	tgitea.WaitForPullRequestCommentMatch(t, topts)
}
