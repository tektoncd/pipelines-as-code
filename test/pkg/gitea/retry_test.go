package gitea

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"go.uber.org/zap"
	"gotest.tools/v3/assert"
)

func TestRetryOnAPIError(t *testing.T) {
	errBoom := errors.New("unknown API Error: 504")
	errLookup := errors.New("lookup blew up")

	tests := []struct {
		name string
		// failures is how many times fn fails before succeeding, a
		// negative value means it always fails.
		failures int
		attempts int
		// recover is nil to exercise the plain retry path, it is given
		// the number of writes issued so far and its own call index.
		recover      func(writes, call int) (string, bool, error)
		wantResult   string
		wantErr      string
		wantFnCalls  int
		wantRecovery bool
	}{
		{
			name:        "succeeds on the first attempt",
			failures:    0,
			attempts:    5,
			wantResult:  "created",
			wantFnCalls: 1,
		},
		{
			name:     "retries once the recovery confirms nothing landed",
			failures: 1,
			attempts: 5,
			recover: func(int, int) (string, bool, error) {
				return "", false, nil
			},
			wantResult:   "created",
			wantFnCalls:  2,
			wantRecovery: true,
		},
		{
			name:     "reuses the result when the write actually landed",
			failures: -1,
			attempts: 5,
			recover: func(int, int) (string, bool, error) {
				return "recovered", true, nil
			},
			wantResult:   "recovered",
			wantFnCalls:  1,
			wantRecovery: true,
		},
		{
			name:     "gives up rather than duplicate when the lookup never answers",
			failures: -1,
			attempts: 3,
			recover: func(int, int) (string, bool, error) {
				return "", false, errLookup
			},
			wantErr:      "could not confirm whether it succeeded",
			wantFnCalls:  1,
			wantRecovery: true,
		},
		{
			name:     "retries once a flaky lookup finally answers",
			failures: 1,
			attempts: 3,
			recover: func(_, call int) (string, bool, error) {
				if call == 0 {
					return "", false, errLookup
				}
				return "", false, nil
			},
			wantResult:   "created",
			wantFnCalls:  2,
			wantRecovery: true,
		},
		{
			name:     "returns the last error once the attempts run out",
			failures: -1,
			attempts: 3,
			recover: func(int, int) (string, bool, error) {
				return "", false, nil
			},
			wantErr:      errBoom.Error(),
			wantFnCalls:  3,
			wantRecovery: true,
		},
		{
			name:     "does not rewrite when an early 404 is just the server catching up",
			failures: -1,
			attempts: 5,
			recover: func(_, call int) (string, bool, error) {
				// The write is in flight: absent at first, visible on
				// the recheck. One absence must not be taken as proof.
				if call == 0 {
					return "", false, nil
				}
				return "recovered", true, nil
			},
			wantResult:   "recovered",
			wantFnCalls:  1,
			wantRecovery: true,
		},
		{
			name:     "recovers when the very last attempt lands",
			failures: -1,
			attempts: 2,
			recover: func(writes, _ int) (string, bool, error) {
				// Only the final write makes it through, and it still
				// reports a gateway timeout to the client.
				if writes < 2 {
					return "", false, nil
				}
				return "recovered", true, nil
			},
			wantResult:   "recovered",
			wantFnCalls:  2,
			wantRecovery: true,
		},
		{
			name:        "does not report a false success without any attempt",
			failures:    -1,
			attempts:    0,
			wantErr:     errBoom.Error(),
			wantFnCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop().Sugar()
			fnCalls := 0
			recoveryCalls := 0

			fn := func() (string, error) {
				fnCalls++
				if tt.failures < 0 || fnCalls <= tt.failures {
					return "", errBoom
				}
				return "created", nil
			}

			var tryRecover func() (string, bool, error)
			if tt.recover != nil {
				tryRecover = func() (string, bool, error) {
					call := recoveryCalls
					recoveryCalls++
					return tt.recover(fnCalls, call)
				}
			}

			result, err := retryOnAPIError(logger, "Creating thing", tt.attempts, 0, fn, tryRecover)

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			} else {
				assert.NilError(t, err)
				assert.Equal(t, tt.wantResult, result)
			}
			assert.Equal(t, tt.wantFnCalls, fnCalls, "unexpected number of write attempts")
			assert.Equal(t, tt.wantRecovery, recoveryCalls > 0, "unexpected recovery usage")
		})
	}
}

// TestRetryOnAPIErrorDoesNotWaitAfterConfirming checks that no time passes
// between confirming a write is absent and re-issuing it. Any gap there is a
// window in which the original write could still land, and we would then
// duplicate it. Rather than time the call, which flakes on a loaded machine,
// this records the exact sequence of writes, lookups and pauses, so the
// assertion pins where the pauses fall and not merely how many there are.
func TestRetryOnAPIErrorDoesNotWaitAfterConfirming(t *testing.T) {
	const delay = 50 * time.Millisecond
	const attempts = 3

	var events []string

	fnCalls := 0
	fn := func() (string, error) {
		fnCalls++
		events = append(events, "write")
		if fnCalls < attempts {
			return "", errors.New("unknown API Error: 504")
		}
		return "created", nil
	}
	tryRecover := func() (string, bool, error) {
		events = append(events, "lookup")
		return "", false, nil
	}
	fakeSleep := func(d time.Duration) {
		events = append(events, fmt.Sprintf("sleep(%s)", d))
	}

	result, err := retryOnAPIErrorWithSleep(zap.NewNop().Sugar(), "Creating thing", attempts, delay, fn, tryRecover, fakeSleep)

	assert.NilError(t, err)
	assert.Equal(t, "created", result)
	assert.Equal(t, attempts, fnCalls)

	// Each failed write is confirmed absent by settleChecks (2) lookups, which
	// pause once between them, and then the write is re-issued *immediately*.
	// A "sleep" appearing directly before any "write" would be the bug: it
	// reopens the window in which the previous write could still land.
	assert.DeepEqual(t, events, []string{
		"write", "lookup", "sleep(50ms)", "lookup",
		"write", "lookup", "sleep(50ms)", "lookup",
		"write",
	})
}

func TestLookup(t *testing.T) {
	value := "found"
	notFound := &forgejo.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}
	gatewayTimeout := &forgejo.Response{Response: &http.Response{StatusCode: http.StatusGatewayTimeout}}

	tests := []struct {
		name          string
		value         *string
		resp          *forgejo.Response
		err           error
		wantFound     bool
		wantErr       bool
		wantValueBack bool
	}{
		{
			name:          "found",
			value:         &value,
			resp:          &forgejo.Response{Response: &http.Response{StatusCode: http.StatusOK}},
			wantFound:     true,
			wantValueBack: true,
		},
		{
			name:      "a 404 confirms it is absent",
			resp:      notFound,
			err:       errors.New("404 Not Found"),
			wantFound: false,
		},
		{
			name:    "any other error is inconclusive",
			resp:    gatewayTimeout,
			err:     errors.New("unknown API Error: 504"),
			wantErr: true,
		},
		{
			name: "no error and no value is absent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := lookup(tt.value, tt.resp, tt.err)
			if tt.wantErr {
				assert.Assert(t, err != nil, "expected an inconclusive lookup")
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, tt.wantFound, found)
			if tt.wantValueBack {
				assert.Equal(t, &value, got)
			}
		})
	}
}
