//go:build e2e

package test

import (
	"regexp"
	"testing"

	"github.com/tektoncd/pipeline/pkg/names"
	tgitlab "github.com/tektoncd/pipelines-as-code/test/pkg/gitlab"
	twait "github.com/tektoncd/pipelines-as-code/test/pkg/wait"
	"gotest.tools/v3/assert"
)

func TestGitlabDeleteTagEvent(t *testing.T) {
	topts := &tgitlab.TestOpts{
		NoMRCreation: true,
	}
	ctx, cleanup := tgitlab.TestMR(t, topts)
	defer cleanup()

	tagName := names.SimpleNameGenerator.RestrictLengthWithRandomSuffix("v1.0")
	err := tgitlab.CreateTag(topts.GLProvider.Client(), topts.ProjectID, tagName)
	defer tgitlab.CleanTag(topts.GLProvider.Client(), topts.ProjectID, tagName)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Created Tag %s in project %d", tagName, topts.ProjectID)

	err = tgitlab.DeleteTag(topts.GLProvider.Client(), topts.ProjectID, tagName)
	assert.NilError(t, err)
	topts.ParamsRun.Clients.Log.Infof("Deleted Tag %s in project %d", tagName, topts.ProjectID)

	logLinesToCheck := int64(1000)
	reg := regexp.MustCompile("event Delete Tag Push Hook is not supported.*")
	err = twait.RegexpMatchingInControllerLog(ctx, topts.ParamsRun, *reg, 10, "controller", &logLinesToCheck, nil)
	assert.NilError(t, err)
}

// Local Variables:
// compile-command: "go test -tags=e2e -v -run TestGitlabDeleteTagEvent$ ."
// End:
