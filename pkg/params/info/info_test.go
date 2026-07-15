package info

import (
	"testing"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"github.com/stretchr/testify/assert"
)

func TestNewInfo(t *testing.T) {
	info := NewInfo()
	assert.Equal(t, info.Pac.ApplicationName, "Pipelines as Code CI")

	value, ok := info.Pac.HubCatalogs.Load("default")
	assert.True(t, ok)

	catalog, ok := value.(settings.HubCatalog)
	assert.True(t, ok)
	assert.Equal(t, "default", catalog.Index)
	assert.Equal(t, settings.ArtifactHubURLDefaultValue, catalog.URL)
}
