package gitlab

import (
	"context"
	"os"
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitlab"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/options"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/repository"
	"github.com/openshift-pipelines/pipelines-as-code/test/pkg/setup"
	gitlab2 "gitlab.com/gitlab-org/api/client-go"
)

func Setup(ctx context.Context) (*params.Run, options.E2E, *gitlab.Provider, error) {
	if err := setup.RequireEnvs(
		"TEST_GITLAB_API_URL",
		"TEST_GITLAB_TOKEN",
		"TEST_GITLAB_GROUP",
		"TEST_EL_WEBHOOK_SECRET",
		"TEST_EL_URL",
		"TEST_GITLAB_SMEEURL",
	); err != nil {
		return nil, options.E2E{}, nil, err
	}
	gitlabURL := os.Getenv("TEST_GITLAB_API_URL")
	gitlabToken := os.Getenv("TEST_GITLAB_TOKEN")
	controllerURL := os.Getenv("TEST_EL_URL")

	run := params.New()
	if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
		return nil, options.E2E{}, nil, err
	}

	e2eoptions := options.E2E{
		ControllerURL: controllerURL,
		UserName:      "oauth2",
		Password:      gitlabToken,
	}
	glprovider := &gitlab.Provider{}
	err := glprovider.SetClient(ctx, run, &info.Event{
		Provider: &info.Provider{
			Token: gitlabToken,
			URL:   gitlabURL,
		},
	}, nil, nil)
	if err != nil {
		return nil, options.E2E{}, nil, err
	}
	return run, e2eoptions, glprovider, nil
}

func HasSecondIdentity() bool {
	token, ok := os.LookupEnv("TEST_GITLAB_SECOND_TOKEN")
	return ok && token != ""
}

func SetupSecond(ctx context.Context, run *params.Run) (options.E2E, *gitlab.Provider, error) {
	if err := setup.RequireEnvs(
		"TEST_GITLAB_API_URL",
		"TEST_GITLAB_SECOND_TOKEN",
	); err != nil {
		return options.E2E{}, nil, err
	}

	gitlabURL := os.Getenv("TEST_GITLAB_API_URL")
	gitlabToken := os.Getenv("TEST_GITLAB_SECOND_TOKEN")
	controllerURL := os.Getenv("TEST_EL_URL")

	if run == nil {
		run = params.New()
		if err := run.Clients.NewClients(ctx, &run.Info); err != nil {
			return options.E2E{}, nil, err
		}
	}

	e2eoptions := options.E2E{
		ControllerURL: controllerURL,
		UserName:      "oauth2",
		Password:      gitlabToken,
	}
	glprovider := &gitlab.Provider{}
	err := glprovider.SetClient(ctx, run, &info.Event{
		Provider: &info.Provider{
			Token: gitlabToken,
			URL:   gitlabURL,
		},
	}, nil, nil)
	if err != nil {
		return options.E2E{}, nil, err
	}

	return e2eoptions, glprovider, nil
}

func TearDown(ctx context.Context, t *testing.T, topts *TestOpts) {
	if os.Getenv("TEST_NOCLEANUP") == "true" {
		topts.ParamsRun.Clients.Log.Infof("Not cleaning up and closing PR since TEST_NOCLEANUP is set")
		return
	}

	if topts.MRNumber > 0 {
		topts.ParamsRun.Clients.Log.Infof("Closing MR %d", topts.MRNumber)
		_, _, err := topts.GLProvider.Client().MergeRequests.UpdateMergeRequest(topts.ProjectID, int64(topts.MRNumber),
			&gitlab2.UpdateMergeRequestOptions{StateEvent: gitlab2.Ptr("close")})
		if err != nil {
			t.Logf("Error closing MR %d: %v", topts.MRNumber, err)
		}
	}

	repository.NSTearDown(ctx, t, topts.ParamsRun, topts.TargetNS)

	if topts.ProjectID != 0 {
		topts.ParamsRun.Clients.Log.Infof("Deleting GitLab project %d", topts.ProjectID)
		_, err := topts.GLProvider.Client().Projects.DeleteProject(topts.ProjectID, nil)
		if err != nil {
			t.Logf("Error deleting GitLab project %d: %v", topts.ProjectID, err)
		}
	}
}

// CleanTag a wrapper function on DeleteTag to ignore errors in cleaning.
func CleanTag(client *gitlab2.Client, pid int, tagName string) {
	_ = DeleteTag(client, pid, tagName)
}
