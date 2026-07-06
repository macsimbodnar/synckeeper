package driveclient

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"time"

	"google.golang.org/api/googleapi"
)

const maxAttempts = 5

// baseBackoff is the first retry delay; tests shrink it.
var baseBackoff = time.Second

// retryable reports whether the error is worth retrying: rate/quota 403,
// 429, and 5xx per the Drive API guidance.
func retryable(err error) bool {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	switch {
	case gerr.Code == http.StatusTooManyRequests:
		return true
	case gerr.Code >= 500 && gerr.Code < 600:
		return true
	case gerr.Code == http.StatusForbidden:
		for _, e := range gerr.Errors {
			if e.Reason == "rateLimitExceeded" || e.Reason == "userRateLimitExceeded" {
				return true
			}
		}
	}
	return false
}

// withRetry runs fn with exponential backoff plus jitter, up to maxAttempts.
// Non-retryable errors and context cancellation return immediately.
func withRetry(ctx context.Context, fn func() error) error {
	var err error
	backoff := baseBackoff
	for attempt := 1; ; attempt++ {
		err = fn()
		if err == nil || !retryable(err) || attempt == maxAttempts {
			return err
		}
		delay := backoff + time.Duration(rand.Int63n(int64(backoff)))
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return ctx.Err()
		}
		backoff *= 2
	}
}
