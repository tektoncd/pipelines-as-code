package completion

import (
	"strings"
	"testing"

	"github.com/tektoncd/pipelines-as-code/pkg/test/cli"
	"gotest.tools/v3/assert"
)

func TestCommand(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "zsh",
		},
		{
			name: "bash",
		},
		{
			name: "fish",
		},
		{
			name: "powershell",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name+" completion", func(t *testing.T) {
			got, err := cli.ExecuteCommand(Command(), tt.name)
			assert.NilError(t, err)

			assert.Assert(t, strings.Contains(got, tt.name+" completion"))
		})
	}
}
