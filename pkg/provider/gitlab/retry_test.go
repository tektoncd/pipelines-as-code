package gitlab

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
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
			name:     "nil pacinfo keeps client defaults",
			pacInfo:  nil,
			wantOpts: 1,
		},
		{
			name:     "disabled keeps client defaults",
			pacInfo:  &info.PacOpts{Settings: settings.DefaultSettings()},
			wantOpts: 1,
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
		maxAttempts int
		wantCalls   int64
	}{
		{
			// go-gitlab retries by default, disabling the setting must not
			// change that pre-existing behaviour.
			name:        "disabled keeps client default retries",
			maxAttempts: 4,
			wantCalls:   6,
		},
		{
			name:        "enabled honors total attempt limit",
			enableRetry: true,
			maxAttempts: 4,
			wantCalls:   4,
		},
		{
			name:        "enabled with unset max attempts uses default",
			enableRetry: true,
			maxAttempts: 0,
			wantCalls:   settings.DefaultAPIRetryMaxAttempts,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				atomic.AddInt64(&calls, 1)
				w.Header().Set("Retry-After", "0")
				w.Header().Set("RateLimit-Reset", strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10))
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()

			v := &Provider{
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						EnableAPIRetry:         tt.enableRetry,
						APIRetryMaxAttempts:    tt.maxAttempts,
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

func TestGitLabRetryBackoffTransient(t *testing.T) {
	maxWait := 120 * time.Second
	minWait := time.Second
	tests := []struct {
		name    string
		resp    *http.Response
		attempt int
		wantMax time.Duration
	}{
		{
			// A transient failure must not be delayed by the rate limit
			// window, only an actual 429 may use those headers.
			name: "server error ignores rate limit headers",
			resp: &http.Response{
				StatusCode: http.StatusInternalServerError,
				Header: http.Header{
					"RateLimit-Reset": []string{strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)},
				},
			},
			wantMax: 2 * minWait,
		},
		{
			name:    "network failure without response stays short",
			resp:    nil,
			wantMax: 2 * minWait,
		},
		{
			name:    "later attempts grow but stay bounded",
			resp:    &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header)},
			attempt: 2,
			wantMax: 6 * minWait,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wait := gitlabRetryBackoff(maxWait)(minWait, maxWait, tt.attempt, tt.resp)
			assert.Assert(t, wait >= minWait, "wait %s below minimum", wait)
			assert.Assert(t, wait <= tt.wantMax, "wait %s above expected %s", wait, tt.wantMax)
		})
	}
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
