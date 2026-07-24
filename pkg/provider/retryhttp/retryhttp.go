// Package retryhttp provides a rate-limit-aware retrying http.RoundTripper
// for Git provider API clients.
package retryhttp

import (
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"
)

const (
	defaultMaxAttempts = 4
	defaultMaxWait     = 120 * time.Second
	initialBackoff     = 1 * time.Second
)

// Options configures the retry transport.
type Options struct {
	// MaxAttempts is the maximum number of attempts (initial request included).
	MaxAttempts int
	// MaxWait caps the wait between attempts. If a rate limit reset is
	// further away than MaxWait, the transport gives up instead of blocking.
	MaxWait time.Duration
	// Logger is optional, used to log retries.
	Logger *zap.SugaredLogger
}

type transport struct {
	base http.RoundTripper
	opts Options
}

// Wrap returns a RoundTripper retrying rate-limited (429, or 403 with rate
// limit headers as used by GitHub) and transient (5xx on idempotent methods)
// failures with jittered exponential backoff. Retry-After and
// X-RateLimit-Reset headers are honored, capped at opts.MaxWait.
func Wrap(base http.RoundTripper, opts Options) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultMaxAttempts
	}
	if opts.MaxWait <= 0 {
		opts.MaxWait = defaultMaxWait
	}
	return &transport{base: base, opts: opts}
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	canReplay := req.Body == nil || req.GetBody != nil

	var resp *http.Response
	var err error
	for attempt := 1; ; attempt++ {
		if req.GetBody != nil && attempt > 1 {
			if req.Body, err = req.GetBody(); err != nil {
				return nil, err
			}
		}

		resp, err = t.base.RoundTrip(req)
		if !canReplay || attempt >= t.opts.MaxAttempts || !t.shouldRetry(req, resp, err) {
			return resp, err
		}

		wait, ok := t.backoff(attempt, resp)
		if !ok {
			// reset is too far away to wait for
			return resp, err
		}
		if resp != nil && resp.Body != nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}

		if t.opts.Logger != nil {
			status := "network error"
			if resp != nil {
				status = fmt.Sprintf("status %d", resp.StatusCode)
			}
			t.opts.Logger.Infof("retrying %s %s after %s (attempt %d/%d): %s",
				req.Method, req.URL.Path, wait.Round(time.Millisecond), attempt, t.opts.MaxAttempts, status)
		}

		timer := time.NewTimer(wait)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
}

func (t *transport) shouldRetry(req *http.Request, resp *http.Response, err error) bool {
	idempotent := req.Method == http.MethodGet || req.Method == http.MethodHead || req.Method == http.MethodOptions
	if err != nil || resp == nil {
		// network level errors: only retry idempotent requests, the server
		// may have processed the request without us seeing the response.
		return idempotent
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return true
	case resp.StatusCode == http.StatusForbidden && isRateLimited(resp):
		// GitHub primary/secondary rate limits use 403 with rate limit headers.
		return true
	case resp.StatusCode >= 500 && resp.StatusCode != http.StatusNotImplemented:
		return idempotent
	}
	return false
}

func isRateLimited(resp *http.Response) bool {
	if resp.Header.Get("Retry-After") != "" {
		return true
	}
	return resp.Header.Get("X-RateLimit-Remaining") == "0"
}

// backoff returns how long to wait before the next attempt. The boolean is
// false when the rate limit reset is further away than MaxWait, meaning we
// should give up rather than block.
func (t *transport) backoff(attempt int, resp *http.Response) (time.Duration, bool) {
	if resp != nil {
		if s := resp.Header.Get("Retry-After"); s != "" {
			if secs, err := strconv.Atoi(s); err == nil {
				wait := time.Duration(secs) * time.Second
				if wait > t.opts.MaxWait {
					return 0, false
				}
				return t.addJitter(wait, time.Second), true
			}
		}
		if s := resp.Header.Get("X-RateLimit-Reset"); s != "" && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			if epoch, err := strconv.ParseInt(s, 10, 64); err == nil {
				wait := time.Until(time.Unix(epoch, 0))
				if wait > t.opts.MaxWait {
					return 0, false
				}
				if wait < 0 {
					wait = 0
				}
				return t.addJitter(wait, time.Second), true
			}
		}
	}
	wait := initialBackoff
	for retry := 1; retry < attempt && wait < t.opts.MaxWait; retry++ {
		if wait > t.opts.MaxWait/2 {
			wait = t.opts.MaxWait
			break
		}
		wait *= 2
	}
	if wait > t.opts.MaxWait {
		wait = t.opts.MaxWait
	}
	return t.addJitter(wait, wait/2), true
}

func (t *transport) addJitter(wait, maxJitter time.Duration) time.Duration {
	remaining := t.opts.MaxWait - wait
	if remaining <= 0 {
		return t.opts.MaxWait
	}
	if maxJitter > remaining {
		maxJitter = remaining
	}
	return wait + jitter(maxJitter)
}

func jitter(maxJitter time.Duration) time.Duration {
	if maxJitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(maxJitter))) //nolint: gosec
}
