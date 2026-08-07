package github

import (
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"gotest.tools/v3/assert"
)

func TestRetryOptions(t *testing.T) {
	tests := []struct {
		name            string
		pacInfo         *info.PacOpts
		wantNil         bool
		wantMaxAttempts int
		wantMaxWait     time.Duration
	}{
		{
			name:    "nil pacinfo",
			pacInfo: nil,
			wantNil: true,
		},
		{
			name:    "disabled by default",
			pacInfo: &info.PacOpts{Settings: settings.DefaultSettings()},
			wantNil: true,
		},
		{
			name: "enabled with settings",
			pacInfo: &info.PacOpts{
				Settings: settings.Settings{
					EnableAPIRetry:         true,
					APIRetryMaxAttempts:    7,
					APIRetryMaxWaitSeconds: 42,
				},
			},
			wantMaxAttempts: 7,
			wantMaxWait:     42 * time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &Provider{pacInfo: tt.pacInfo}
			opts := v.retryOptions()
			if tt.wantNil {
				assert.Assert(t, opts == nil)
				return
			}
			assert.Assert(t, opts != nil)
			assert.Equal(t, tt.wantMaxAttempts, opts.MaxAttempts)
			assert.Equal(t, tt.wantMaxWait, opts.MaxWait)
		})
	}
}
