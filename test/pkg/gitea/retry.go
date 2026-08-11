package gitea

import (
	"fmt"
	"net/http"
	"time"

	"codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v3"
	"go.uber.org/zap"
)

// settleChecks is how many consecutive lookups must agree that a write did
// not land before we believe them. A write that failed with a gateway
// timeout may still be in flight on the server, so a single immediate 404
// is not proof of absence: re-issuing the write on the strength of it would
// be exactly the duplicate we are trying to avoid.
const settleChecks = 2

// retryOnAPIError retries fn up to attempts times (sleeping delay between
// each attempt), to work around transient Gitea/Forgejo API errors (e.g.
// 504 upstream timeouts) seen against the test instance under load.
//
// The writes being retried are not idempotent, and a request that fails on
// the client side can still have succeeded on the server. So after every
// failed write, including the last one, retryOnAPIError asks tryRecover
// whether the attempt actually landed:
//
//   - (value, true, nil)  the write landed, reuse value and stop
//   - (_, false, nil)     it is not there
//   - (_, _, err)         inconclusive, the lookup itself failed
//
// fn is only re-issued once the write is confirmed absent by settleChecks
// consecutive successful lookups, and it is re-issued straight away so that
// nothing happens between the confirmation and the write. If the lookups
// never agree, or keep failing, retryOnAPIError gives up rather than risk a
// duplicate write.
//
// tryRecover may be nil, in which case fn is simply retried.
func retryOnAPIError[T any](logger *zap.SugaredLogger, action string, attempts int, delay time.Duration, fn func() (T, error), tryRecover func() (T, bool, error)) (T, error) {
	return retryOnAPIErrorWithSleep(logger, action, attempts, delay, fn, tryRecover, time.Sleep)
}

// retryOnAPIErrorWithSleep is retryOnAPIError with the pause between attempts
// injected, so a test can observe how the loop paces itself without spending
// real time and without a mutable package global.
func retryOnAPIErrorWithSleep[T any](logger *zap.SugaredLogger, action string, attempts int, delay time.Duration, fn func() (T, error), tryRecover func() (T, bool, error), sleep func(time.Duration)) (T, error) {
	var result T
	var err error
	if attempts < 1 {
		attempts = 1
	}
	for i := 0; i < attempts; i++ {
		if result, err = fn(); err == nil {
			return result, nil
		}
		logger.Infof("%s has failed, attempt %d/%d, err: %v", action, i+1, attempts, err)
		if tryRecover != nil {
			value, found, confirmed := confirmOutcome(logger, action, attempts, delay, tryRecover, sleep)
			if found {
				logger.Infof("%s already succeeded, reusing the existing result", action)
				return value, nil
			}
			if !confirmed {
				return result, fmt.Errorf("%s failed and we could not confirm whether it succeeded, not retrying to avoid a duplicate: %w", action, err)
			}
		}
		if i == attempts-1 {
			return result, err
		}
		if tryRecover == nil {
			// Nothing has paced this loop, so wait before hammering the
			// API again. With a recovery check we deliberately do not
			// wait: confirmOutcome has already spent that time polling
			// and just told us the write is absent, and sleeping again
			// would reopen the window where it could quietly appear.
			sleep(delay)
		}
	}
	return result, err
}

// confirmOutcome polls tryRecover until it finds the write, sees it stay
// absent across enough consecutive lookups to rule out a slow server, or
// runs out of budget. It reports the recovered value, whether the write was
// found, and whether the answer is conclusive at all.
func confirmOutcome[T any](logger *zap.SugaredLogger, action string, attempts int, delay time.Duration, tryRecover func() (T, bool, error), sleep func(time.Duration)) (T, bool, bool) {
	var zero T
	needed := min(settleChecks, attempts)
	absences := 0
	for i := 0; i < attempts; i++ {
		value, found, err := tryRecover()
		switch {
		case err != nil:
			absences = 0
			logger.Infof("%s: cannot tell whether the attempt succeeded, rechecking %d/%d, err: %v", action, i+1, attempts, err)
		case found:
			return value, true, true
		default:
			absences++
			if absences >= needed {
				return zero, false, true
			}
			logger.Infof("%s: not there yet, it may still be in flight, rechecking %d/%d", action, i+1, attempts)
		}
		if i < attempts-1 {
			sleep(delay)
		}
	}
	return zero, false, false
}

// absent reports whether a failed lookup means the object is genuinely
// missing (a 404) rather than the lookup itself having failed. The SDK
// surfaces errors untyped, so only the response carries the status code.
func absent(resp *forgejo.Response, err error) bool {
	return err != nil && resp != nil && resp.StatusCode == http.StatusNotFound
}

// lookup turns a Gitea "get" call into the tri-state answer that
// retryOnAPIError expects: found, not there, or inconclusive.
func lookup[T any](value *T, resp *forgejo.Response, err error) (*T, bool, error) {
	switch {
	case err == nil && value != nil:
		return value, true, nil
	case absent(resp, err):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	default:
		// No error but nothing returned either, treat as not there.
		return nil, false, nil
	}
}
