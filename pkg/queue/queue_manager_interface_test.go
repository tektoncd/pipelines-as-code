package queue

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestSplitPrKey(t *testing.T) {
	tests := []struct {
		name          string
		key           string
		wantNamespace string
		wantName      string
		wantOK        bool
	}{
		{name: "valid key", key: "ns/name", wantNamespace: "ns", wantName: "name", wantOK: true},
		{name: "trims surrounding and inner whitespace", key: "  ns / name  ", wantNamespace: "ns", wantName: "name", wantOK: true},
		{name: "empty string", key: "", wantOK: false},
		{name: "missing separator", key: "no-slash", wantOK: false},
		{name: "empty namespace", key: "/name-only", wantOK: false},
		{name: "empty name", key: "ns-only/", wantOK: false},
		{name: "name contains a slash", key: "too/many/slashes", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			namespace, name, ok := SplitPrKey(tt.key)
			assert.Equal(t, ok, tt.wantOK)
			if tt.wantOK {
				assert.Equal(t, namespace, tt.wantNamespace)
				assert.Equal(t, name, tt.wantName)
			}
		})
	}
}
