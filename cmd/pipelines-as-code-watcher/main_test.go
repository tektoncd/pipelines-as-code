package main

import (
	"net/http"
	"net/http/httptest"
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
			// t.Setenv in both branches so its cleanup restores whatever the
			// caller had exported; the unset case then clears it, which keeps
			// the default-off assertion independent of the parent environment.
			t.Setenv("PAC_ENABLE_QUEUE_DEBUG", tt.env)
			if !tt.set {
				if err := os.Unsetenv("PAC_ENABLE_QUEUE_DEBUG"); err != nil {
					t.Fatalf("cannot unset PAC_ENABLE_QUEUE_DEBUG: %v", err)
				}
			}
			if got := queueDebugEnabled(); got != tt.want {
				t.Errorf("queueDebugEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProbeMuxQueueDebugRoute(t *testing.T) {
	tests := []struct {
		name     string
		env      string
		set      bool
		wantCode int
	}{
		{name: "unset does not register the route", set: false, wantCode: http.StatusNotFound},
		{name: "invalid value does not register the route", env: "yolo", set: true, wantCode: http.StatusNotFound},
		{name: "false does not register the route", env: "false", set: true, wantCode: http.StatusNotFound},
		{name: "true registers the route", env: "true", set: true, wantCode: http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PAC_ENABLE_QUEUE_DEBUG", tt.env)
			if !tt.set {
				assert.NilError(t, os.Unsetenv("PAC_ENABLE_QUEUE_DEBUG"))
			}

			rec := httptest.NewRecorder()
			newProbeMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/queue", nil))

			assert.Equal(t, rec.Code, tt.wantCode)
		})
	}
}
