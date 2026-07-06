package driveclient

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
)

func TestMain(m *testing.M) {
	baseBackoff = time.Millisecond
	m.Run()
}

func apiErr(code int, reason string) error {
	e := &googleapi.Error{Code: code}
	if reason != "" {
		e.Errors = []googleapi.ErrorItem{{Reason: reason}}
	}
	return e
}

func TestRetryableClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil-ish plain error", errors.New("boom"), false},
		{"429", apiErr(http.StatusTooManyRequests, ""), true},
		{"500", apiErr(500, ""), true},
		{"503", apiErr(503, ""), true},
		{"403 rate limit", apiErr(403, "rateLimitExceeded"), true},
		{"403 user rate limit", apiErr(403, "userRateLimitExceeded"), true},
		{"403 forbidden", apiErr(403, "insufficientPermissions"), false},
		{"404", apiErr(404, ""), false},
		{"401", apiErr(401, ""), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := retryable(tc.err); got != tc.want {
				t.Errorf("retryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestWithRetryEventualSuccess(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), func() error {
		calls++
		if calls < 3 {
			return apiErr(503, "")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d, want nil and 3", err, calls)
	}
}

func TestWithRetryGivesUpAfterMaxAttempts(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), func() error {
		calls++
		return apiErr(503, "")
	})
	if err == nil || calls != maxAttempts {
		t.Fatalf("err=%v calls=%d, want error and %d", err, calls, maxAttempts)
	}
}

func TestWithRetryStopsOnPermanentError(t *testing.T) {
	calls := 0
	err := withRetry(context.Background(), func() error {
		calls++
		return apiErr(404, "")
	})
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d, want error and 1", err, calls)
	}
}

func TestWithRetryHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := withRetry(ctx, func() error { return apiErr(503, "") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
}
