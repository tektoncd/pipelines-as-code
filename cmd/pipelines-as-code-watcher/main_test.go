package main

import (
	"os"
	"testing"

	"gotest.tools/v3/assert"
)

func TestQueueDebugEnabled(t *testing.T) {
	tests := []struct {
		name string
		env  string
		set  bool
		want bool
	}{
		{name: "unset defaults to disabled", set: false, want: false},
		{name: "empty string is not a valid bool, fails closed", env: "", set: true, want: false},
		{name: "true enables it", env: "true", set: true, want: true},
		{name: "1 enables it", env: "1", set: true, want: true},
		{name: "false disables it", env: "false", set: true, want: false},
		{name: "0 disables it", env: "0", set: true, want: false},
		{name: "garbage fails closed", env: "yolo", set: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("PAC_ENABLE_QUEUE_DEBUG", tt.env)
			} else {
				value, exists := os.LookupEnv("PAC_ENABLE_QUEUE_DEBUG")
				assert.NilError(t, os.Unsetenv("PAC_ENABLE_QUEUE_DEBUG"))
				t.Cleanup(func() {
					if exists {
						assert.NilError(t, os.Setenv("PAC_ENABLE_QUEUE_DEBUG", value))
						return
					}
					assert.NilError(t, os.Unsetenv("PAC_ENABLE_QUEUE_DEBUG"))
				})
			}
			assert.Equal(t, queueDebugEnabled(), tt.want)
		})
	}
}
