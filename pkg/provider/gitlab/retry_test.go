package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	gitlabclient "gitlab.com/gitlab-org/api/client-go"
	"gotest.tools/v3/assert"
)

func TestClientOptions(t *testing.T) {
	tests := []struct {
		name     string
		pacInfo  *info.PacOpts
		wantOpts int
	}{
		{
			name:     "nil pacinfo only base url",
			pacInfo:  nil,
			wantOpts: 2,
		},
		{
			name:     "disabled by default only base url",
			pacInfo:  &info.PacOpts{Settings: settings.DefaultSettings()},
			wantOpts: 2,
		},
		{
			name: "enabled adds retry options",
			pacInfo: &info.PacOpts{
				Settings: settings.Settings{
					EnableAPIRetry:         true,
					APIRetryMaxAttempts:    7,
					APIRetryMaxWaitSeconds: 42,
				},
			},
			wantOpts: 6,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &Provider{pacInfo: tt.pacInfo}
			opts := v.clientOptions("https://gitlab.example.com")
			assert.Equal(t, tt.wantOpts, len(opts))
		})
	}
}

func TestClientRetryAttempts(t *testing.T) {
	tests := []struct {
		name        string
		enableRetry bool
		wantCalls   int64
	}{
		{
			name:      "disabled makes one attempt",
			wantCalls: 1,
		},
		{
			name:        "enabled honors total attempt limit",
			enableRetry: true,
			wantCalls:   4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt64(&calls, 1)
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()

			v := &Provider{
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						EnableAPIRetry:         tt.enableRetry,
						APIRetryMaxAttempts:    4,
						APIRetryMaxWaitSeconds: 1,
					},
				},
			}
			client, err := gitlabclient.NewClient("", v.clientOptions(server.URL)...)
			assert.NilError(t, err)

			_, _, err = client.Users.ListUsers(nil)
			assert.Assert(t, err != nil)
			assert.Equal(t, tt.wantCalls, atomic.LoadInt64(&calls))
		})
	}
}

func TestGitLabRetryWaitCap(t *testing.T) {
	maxWait := 2 * time.Second
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"3600"}},
	}

	retry, err := gitlabRetryPolicy(maxWait)(t.Context(), resp, nil)
	assert.NilError(t, err)
	assert.Assert(t, !retry)

	wait := gitlabRetryBackoff(maxWait)(time.Second, maxWait, 0, resp)
	assert.Assert(t, wait <= maxWait)
}

func TestGitLabRetryPolicyMethods(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		status    int
		wantRetry bool
	}{
		{
			name:      "retry server error for GET",
			method:    http.MethodGet,
			status:    http.StatusInternalServerError,
			wantRetry: true,
		},
		{
			name:   "do not retry server error for POST",
			method: http.MethodPost,
			status: http.StatusInternalServerError,
		},
		{
			name:      "retry rate limit for POST",
			method:    http.MethodPost,
			status:    http.StatusTooManyRequests,
			wantRetry: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tt.status,
				Header:     make(http.Header),
				Request: &http.Request{
					Method: tt.method,
				},
			}
			retry, err := gitlabRetryPolicy(time.Minute)(t.Context(), resp, nil)
			assert.NilError(t, err)
			assert.Equal(t, tt.wantRetry, retry)
		})
	}
}

func TestGitLabRetryPolicyNetworkErrors(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		wantRetry bool
	}{
		{
			name:      "retry network error for GET",
			method:    http.MethodGet,
			wantRetry: true,
		},
		{
			name:   "do not retry network error for POST",
			method: http.MethodPost,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(t.Context(), retryMethodContextKey{}, tt.method)
			retry, err := gitlabRetryPolicy(time.Minute)(ctx, nil, errors.New("network failure"))
			assert.NilError(t, err)
			assert.Equal(t, tt.wantRetry, retry)
		})
	}
}
