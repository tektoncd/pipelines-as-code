package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	gitlab2 "github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitlab"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/cctx"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/payload"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/scm"
	"github.com/tektoncd/pipeline/pkg/names"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
)

type TestOpts struct {
	TargetNS             string
	TargetRefName        string
	TargetEvent          string
	DefaultBranch        string
	ProjectID            int
	ProjectInfo          *gitlab.Project
	MRNumber             int
	GitCloneURL          string
	GitHTMLURL           string
	SHA                  string
	ParamsRun            *params.Run
	GLProvider           gitlab2.Provider
	SecondGLProvider     gitlab2.Provider
	Opts                 options.E2E
	SecondOpts           options.E2E
	YAMLFiles            map[string]string
	ExtraArgs            map[string]string
	Regexp               *regexp.Regexp
	CheckForStatus       string
	CheckForNumberStatus int
	Settings             *v1alpha1.Settings
	Incomings            *[]v1alpha1.Incoming
	NoMRCreation         bool
	SkipEventsCheck      bool
	ExpectEvents         bool
}

// TestMR is the unified lifecycle function for GitLab E2E tests.
// It creates a fresh project, sets up the CRD, pushes files,
// creates a merge request, and returns a cleanup function.
func TestMR(t *testing.T, topts *TestOpts) (context.Context, func()) {
	t.Helper()
	ctx := context.Background()

	if topts.ParamsRun == nil {
		runcnx, opts, glprovider, err := Setup(ctx)
		assert.NilError(t, err, fmt.Errorf("cannot do gitlab setup: %w", err))
		topts.GLProvider = glprovider
		topts.ParamsRun = runcnx
		topts.Opts = opts
	}
	ctx, err := cctx.GetControllerCtxInfo(ctx, topts.ParamsRun)
	assert.NilError(t, err)

	if topts.TargetRefName == "" {
		topts.TargetRefName = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-test")
	}
	if topts.TargetNS == "" {
		topts.TargetNS = names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("pac-e2e-ns")
	}
	if topts.DefaultBranch == "" {
		topts.DefaultBranch = options.MainBranch
	}
	if topts.ExtraArgs == nil {
		topts.ExtraArgs = map[string]string{}
	}

	// Update incoming targets to use the actual TargetNS
	if topts.Incomings != nil {
		for i := range *topts.Incomings {
			(*topts.Incomings)[i].Targets = []string{topts.TargetNS}
		}
	}

	// Create a fresh GitLab project
	groupPath := os.Getenv("TEST_GITLAB_GROUP")
	hookURL := os.Getenv("TEST_GITLAB_SMEEURL")
	webhookSecret := os.Getenv("TEST_EL_WEBHOOK_SECRET")
	project, err := CreateGitLabProject(topts.GLProvider.Client(), groupPath, topts.TargetRefName, hookURL, webhookSecret, topts.ParamsRun.Clients.Log)
	assert.NilError(t, err)
	topts.ProjectID = int(project.ID)
	topts.ProjectInfo = project
	topts.GitHTMLURL = project.WebURL
	topts.DefaultBranch = project.DefaultBranch

	// Create CRD (namespace, secrets, Repository CR)
	err = CreateCRD(ctx, topts)
	assert.NilError(t, err)

	cleanup := func() {
		if os.Getenv("TEST_NOCLEANUP") != "true" {
			TearDown(ctx, t, topts)
		}
	}

	// Build git clone URL
	gitCloneURL, err := scm.MakeGitCloneURL(project.WebURL, topts.Opts.UserName, topts.Opts.Password)
	assert.NilError(t, err)
	topts.GitCloneURL = gitCloneURL

	if topts.NoMRCreation {
		return ctx, cleanup
	}

	// Process YAML files and push
	entries, err := payload.GetEntries(topts.YAMLFiles,
		topts.TargetNS,
		topts.DefaultBranch,
		topts.TargetEvent,
		topts.ExtraArgs)
	assert.NilError(t, err)

	scmOpts := &scm.Opts{
		GitURL:        topts.GitCloneURL,
		Log:           topts.ParamsRun.Clients.Log,
		WebURL:        topts.GitHTMLURL,
		TargetRefName: topts.TargetRefName,
		BaseRefName:   topts.DefaultBranch,
	}
	topts.SHA = scm.PushFilesToRefGit(t, scmOpts, entries)
	topts.ParamsRun.Clients.Log.Infof("Branch %s has been created and pushed with files", topts.TargetRefName)

	// Create MR
	mrTitle := "TestMergeRequest - " + topts.TargetRefName
	mrID, err := CreateMR(topts.GLProvider.Client(), topts.ProjectID, topts.TargetRefName, topts.DefaultBranch, mrTitle)
	assert.NilError(t, err)
	topts.MRNumber = mrID
	topts.ParamsRun.Clients.Log.Infof("MergeRequest %s/-/merge_requests/%d has been created", topts.GitHTMLURL, mrID)

	return ctx, cleanup
}

func CreateMR(client *gitlab.Client, pid int, sourceBranch, targetBranch, title string) (int, error) {
	mr, _, err := client.MergeRequests.CreateMergeRequest(pid, &gitlab.CreateMergeRequestOptions{
		Title:        &title,
		SourceBranch: &sourceBranch,
		TargetBranch: &targetBranch,
	})
	if err != nil {
		return -1, err
	}
	return int(mr.IID), nil
}

func SetupSecondIdentity(ctx context.Context, topts *TestOpts) error {
	if topts.SecondOpts.Password != "" {
		return nil
	}
	if topts.ParamsRun == nil {
		return fmt.Errorf("primary GitLab test setup must be initialized before the second identity")
	}

	opts, provider, err := SetupSecond(ctx, topts.ParamsRun)
	if err != nil {
		return err
	}
	topts.SecondOpts = opts
	topts.SecondGLProvider = provider

	return nil
}

func CreateTag(client *gitlab.Client, pid int, tagName string) error {
	_, resp, err := client.Tags.CreateTag(pid, &gitlab.CreateTagOptions{
		TagName: gitlab.Ptr(tagName),
		Ref:     gitlab.Ptr("main"),
	})
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("failed to create tag : %d", resp.StatusCode)
	}

	return nil
}

func DeleteTag(client *gitlab.Client, pid int, tagName string) error {
	resp, err := client.Tags.DeleteTag(pid, tagName)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to delete tag : %d", resp.StatusCode)
	}

	return nil
}
