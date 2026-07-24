package test

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestSetupBBDataCenterClient(t *testing.T) {
	client, _, tearDown, _ := SetupBBDataCenterClient()
	defer tearDown()

	assert.Assert(t, client != nil)
}
