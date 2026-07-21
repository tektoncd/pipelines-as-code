package test

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestSetupBBCloudClient(t *testing.T) {
	client, _, tearDown := SetupBBCloudClient(t)
	defer tearDown()

	assert.Assert(t, client != nil)
}
