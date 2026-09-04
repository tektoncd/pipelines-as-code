package main

import "testing"

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
			}
			if got := queueDebugEnabled(); got != tt.want {
				t.Errorf("queueDebugEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
