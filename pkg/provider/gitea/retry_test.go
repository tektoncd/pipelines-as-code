package gitea

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/settings"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
)

func TestSetClientRetriesRateLimitedStatusRequest(t *testing.T) {
	tests := []struct {
		name          string
		enableRetry   bool
		wantStatusErr bool
		wantCalls     int64
	}{
		{
			name:        "retries when enabled",
			enableRetry: true,
			wantCalls:   2,
		},
		{
			name:          "does not retry when disabled",
			wantStatusErr: true,
			wantCalls:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var statusCalls int64
			server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				switch req.URL.Path {
				case "/api/v1/version":
					_, err := fmt.Fprint(rw, `{"version":"1.17.0"}`)
					assert.NilError(t, err)
				case "/api/v1/repos/org/repo/statuses/sha":
					assert.Equal(t, http.MethodPost, req.Method)
					if atomic.AddInt64(&statusCalls, 1) == 1 {
						rw.Header().Set("Retry-After", "0")
						rw.WriteHeader(http.StatusTooManyRequests)
						return
					}
					rw.WriteHeader(http.StatusCreated)
					_, err := fmt.Fprint(rw, `{"id":1,"status":"pending"}`)
					assert.NilError(t, err)
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
					rw.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			provider := &Provider{
				Logger: zap.NewNop().Sugar(),
				pacInfo: &info.PacOpts{
					Settings: settings.Settings{
						EnableAPIRetry:         tt.enableRetry,
						APIRetryMaxAttempts:    2,
						APIRetryMaxWaitSeconds: 1,
					},
				},
			}
			event := &info.Event{Provider: &info.Provider{URL: server.URL, Token: "token"}}
			assert.NilError(t, provider.SetClient(context.Background(), nil, event, nil, nil))

			_, _, err := provider.giteaClient.CreateStatus("org", "repo", "sha", forgejo.CreateStatusOption{
				State: forgejo.StatusPending,
			})
			if tt.wantStatusErr {
				assert.Assert(t, err != nil, "expected rate-limit error")
			} else {
				assert.NilError(t, err)
			}
			assert.Equal(t, tt.wantCalls, atomic.LoadInt64(&statusCalls))
		})
	}
}
