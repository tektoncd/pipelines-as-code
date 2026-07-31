package retryhttp

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		body           string
		maxAttempts    int
		maxWait        time.Duration
		handler        func(calls int64, w http.ResponseWriter, r *http.Request)
		wantStatus     int
		wantCalls      int64
		wantErr        bool
		wantBodyOnLast string
		unreplayable   bool
	}{
		{
			name:        "no retry on success",
			method:      http.MethodGet,
			maxAttempts: 3,
			handler: func(_ int64, w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			},
			wantStatus: http.StatusOK,
			wantCalls:  1,
		},
		{
			name:        "retries 429 until success",
			method:      http.MethodGet,
			maxAttempts: 3,
			handler: func(calls int64, w http.ResponseWriter, _ *http.Request) {
				if calls < 3 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			wantStatus: http.StatusOK,
			wantCalls:  3,
		},
		{
			name:        "gives up after max attempts",
			method:      http.MethodGet,
			maxAttempts: 2,
			handler: func(_ int64, w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
			},
			wantStatus: http.StatusTooManyRequests,
			wantCalls:  2,
		},
		{
			name:        "no retry on 404",
			method:      http.MethodGet,
			maxAttempts: 3,
			handler: func(_ int64, w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			wantStatus: http.StatusNotFound,
			wantCalls:  1,
		},
		{
			name:        "no retry on plain 403 without rate limit headers",
			method:      http.MethodGet,
			maxAttempts: 3,
			handler: func(_ int64, w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			},
			wantStatus: http.StatusForbidden,
			wantCalls:  1,
		},
		{
			name:        "retries github 403 rate limit",
			method:      http.MethodGet,
			maxAttempts: 3,
			handler: func(calls int64, w http.ResponseWriter, _ *http.Request) {
				if calls < 2 {
					w.Header().Set("X-RateLimit-Remaining", "0")
					w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Unix()))
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			wantStatus: http.StatusOK,
			wantCalls:  2,
		},
		{
			name:        "gives up when reset is beyond max wait",
			method:      http.MethodGet,
			maxAttempts: 3,
			maxWait:     1 * time.Second,
			handler: func(_ int64, w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "3600")
				w.WriteHeader(http.StatusTooManyRequests)
			},
			wantStatus: http.StatusTooManyRequests,
			wantCalls:  1,
		},
		{
			name:        "retries 500 on GET",
			method:      http.MethodGet,
			maxAttempts: 3,
			handler: func(calls int64, w http.ResponseWriter, _ *http.Request) {
				if calls < 2 {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			wantStatus: http.StatusOK,
			wantCalls:  2,
		},
		{
			name:        "no retry of 500 on POST",
			method:      http.MethodPost,
			body:        "hello",
			maxAttempts: 3,
			handler: func(_ int64, w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			wantStatus: http.StatusInternalServerError,
			wantCalls:  1,
		},
		{
			name:        "replays POST body on 429 retry",
			method:      http.MethodPost,
			body:        "hello",
			maxAttempts: 3,
			handler: func(calls int64, w http.ResponseWriter, _ *http.Request) {
				if calls < 2 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				w.WriteHeader(http.StatusOK)
			},
			wantStatus:     http.StatusOK,
			wantCalls:      2,
			wantBodyOnLast: "hello",
		},
		{
			name:         "does not truncate or retry unreplayable body",
			method:       http.MethodPost,
			body:         strings.Repeat("x", 3*1024*1024),
			maxAttempts:  3,
			unreplayable: true,
			handler: func(_ int64, w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
			},
			wantStatus: http.StatusTooManyRequests,
			wantCalls:  1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls int64
			var lastBody atomic.Value
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := atomic.AddInt64(&calls, 1)
				b, _ := io.ReadAll(r.Body)
				lastBody.Store(string(b))
				tt.handler(n, w, r)
			}))
			defer srv.Close()

			client := &http.Client{Transport: Wrap(nil, Options{MaxAttempts: tt.maxAttempts, MaxWait: tt.maxWait})}
			var reqBody io.Reader
			if tt.body != "" {
				reqBody = strings.NewReader(tt.body)
				if tt.unreplayable {
					reqBody = io.NopCloser(reqBody)
				}
			}

			req, err := http.NewRequestWithContext(t.Context(), tt.method, srv.URL, reqBody)
			assert.NilError(t, err)
			resp, err := client.Do(req)
			if tt.wantErr {
				assert.Assert(t, err != nil)
				return
			}
			assert.NilError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			assert.Equal(t, tt.wantCalls, atomic.LoadInt64(&calls))
			if tt.unreplayable {
				got, ok := lastBody.Load().(string)
				assert.Assert(t, ok)
				assert.Equal(t, tt.body, got)
			}
			if tt.wantBodyOnLast != "" {
				got, ok := lastBody.Load().(string)
				assert.Assert(t, ok)
				assert.Equal(t, tt.wantBodyOnLast, got)
			}
		})
	}
}

func TestGetBodyFailureDoesNotReturnClosedResponse(t *testing.T) {
	var calls int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&calls, 1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL, strings.NewReader("body"))
	assert.NilError(t, err)
	req.GetBody = func() (io.ReadCloser, error) {
		return nil, errors.New("cannot rebuild body")
	}

	client := &http.Client{Transport: Wrap(nil, Options{MaxAttempts: 2})}
	resp, err := client.Do(req)
	assert.ErrorContains(t, err, "cannot rebuild body")
	if resp != nil {
		resp.Body.Close()
	}
	assert.Assert(t, resp == nil)
	assert.Equal(t, int64(1), atomic.LoadInt64(&calls))
}

func TestBackoffLargeAttemptDoesNotOverflow(t *testing.T) {
	maxWait := 2 * time.Second
	tr := &transport{opts: Options{MaxWait: maxWait}}

	wait, ok := tr.backoff(1_000_000, nil)
	assert.Assert(t, ok)
	assert.Assert(t, wait >= 0)
	assert.Assert(t, wait <= maxWait)
}
