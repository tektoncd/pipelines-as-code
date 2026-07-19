package info

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"gotest.tools/v3/assert"
)

func TestNewInfo(t *testing.T) {
	info := NewInfo()
	assert.Equal(t, info.Pac.ApplicationName, "Pipelines as Code CI")

	value, ok := info.Pac.HubCatalogs.Load("default")
	assert.Assert(t, ok)

	catalog, ok := value.(settings.HubCatalog)
	assert.Assert(t, ok)
	assert.Equal(t, catalog.Index, "default")
	assert.Equal(t, catalog.URL, settings.ArtifactHubURLDefaultValue)
}
