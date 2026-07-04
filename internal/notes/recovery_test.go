package notes

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// TestAuthAttemptThrottleRecovery locks the IP, advances simulated time past
// the 15-minute lockout, and asserts that the throttle releases. It also
// verifies that a fresh 5-failure window starts after recovery and that the
// lockout can be re-triggered and recovered again.
//
// The throttle uses the package-level `now` variable (defined in handlers.go
// as `var now = time.Now`) so we can override it here to simulate the
// passage of time deterministically.
//
// Note: the 15-minute lockout is measured from the 6th (lockout-triggering)
// attempt, not from the 5th or from the first observed 429. The test
// therefore derives lockoutExpiresAt from the 6th attempt's timestamp.
func TestAuthAttemptThrottleRecovery(t *testing.T) {
	origNow := now
	t.Cleanup(func() { now = origNow })

	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	current := t0
	now = func() time.Time { return current }

	auth := newBrowserAuth("correct-token")
	handler := requireAccessToken(auth, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	makeReq := func(label string) (int, string) {
		req := httptest.NewRequest("GET", "/api/presentations", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		req.RemoteAddr = "10.0.0.1:12345"
		resp := httptest.NewRecorder()
		handler(resp, req)
		retryAfter := resp.Header().Get("Retry-After")
		t.Logf("[%s] status=%d Retry-After=%q", label, resp.Code, retryAfter)
		return resp.Code, retryAfter
	}

	// Phase 1: 5 wrong attempts in 5 seconds — all 401, no lockout yet, no header.
	for i := 1; i <= 5; i++ {
		current = t0.Add(time.Duration(i) * time.Second)
		got, ra := makeReq(fmt.Sprintf("attempt %d (T+%ds)", i, i))
		if got != 401 {
			t.Fatalf("attempt %d: expected 401, got %d", i, got)
		}
		if ra != "" {
			t.Fatalf("attempt %d: did not expect Retry-After on 401, got %q", i, ra)
		}
	}

	// Phase 2: 6th attempt at T+6s triggers lockout. Lockout expires at T+6s+15min.
	lockoutTriggerTime := t0.Add(6 * time.Second)
	lockoutExpiresAt := lockoutTriggerTime.Add(15 * time.Minute)
	current = lockoutTriggerTime
	got, ra := makeReq("attempt 6 (T+6s) — triggers lockout")
	if got != 429 {
		t.Fatalf("6th attempt: expected 429 (throttled), got %d", got)
	}
	// Just-triggered lockout: Retry-After should be ~900 (15 min) seconds.
	assertRetryAfterInRange(t, ra, 895, 900)

	// Phase 3: still throttled 1s before lockout expiry. Header should reflect ~1s.
	current = lockoutExpiresAt.Add(-1 * time.Second)
	got, ra = makeReq("1s before lockout expires")
	if got != 429 {
		t.Fatalf("just before lockout: expected 429, got %d", got)
	}
	assertRetryAfterInRange(t, ra, 1, 1)

	// Phase 4: at the exact lockout boundary — should recover.
	current = lockoutExpiresAt
	got, ra = makeReq("exactly at lockout expiry")
	if got != 401 {
		t.Fatalf("at lockout boundary: expected 401 (recovered), got %d", got)
	}
	if ra != "" {
		t.Fatalf("after recovery: did not expect Retry-After on 401, got %q", ra)
	}

	// Phase 5: long after the lockout — also recovered.
	current = lockoutExpiresAt.Add(1 * time.Hour)
	got, ra = makeReq("1 hour after lockout expiry")
	if got != 401 {
		t.Fatalf("long after lockout: expected 401 (recovered), got %d", got)
	}
	if ra != "" {
		t.Fatalf("long after lockout: did not expect Retry-After on 401, got %q", ra)
	}

	// Phase 6: the user can rack up 5 fresh failures in a new window
	// before being throttled again (the 6th triggers a new lockout).
	// The first failure in this new window is the one at the recovery boundary.
	for i := 1; i <= 4; i++ {
		current = lockoutExpiresAt.Add(time.Duration(i) * time.Second)
		got, ra := makeReq(fmt.Sprintf("post-recovery fresh attempt %d", i+1))
		if got != 401 {
			t.Fatalf("post-recovery fresh attempt %d: expected 401, got %d", i+1, got)
		}
		if ra != "" {
			t.Fatalf("post-recovery fresh attempt %d: did not expect Retry-After on 401, got %q", i+1, ra)
		}
	}
	current = lockoutExpiresAt.Add(5 * time.Second)
	got, ra = makeReq("post-recovery 6th fresh attempt — re-triggers lockout")
	if got != 429 {
		t.Fatalf("post-recovery 6th attempt: expected 429, got %d", got)
	}
	assertRetryAfterInRange(t, ra, 895, 900)

	// Phase 7: the new lockout also recovers normally.
	current = lockoutExpiresAt.Add(5*time.Second + 15*time.Minute)
	got, ra = makeReq("after second lockout")
	if got != 401 {
		t.Fatalf("after second lockout: expected 401 (recovered), got %d", got)
	}
	if ra != "" {
		t.Fatalf("after second lockout: did not expect Retry-After on 401, got %q", ra)
	}
}

// assertRetryAfterInRange parses the Retry-After header as a non-negative
// integer of seconds (RFC 7231's delta-seconds form) and asserts that it
// falls in [min, max] inclusive.
func assertRetryAfterInRange(t *testing.T, header string, min, max int) {
	t.Helper()
	if header == "" {
		t.Fatalf("expected Retry-After header in range [%d,%d], got empty", min, max)
	}
	secs, err := strconv.Atoi(header)
	if err != nil {
		t.Fatalf("Retry-After is not a delta-seconds integer: %q (%v)", header, err)
	}
	if secs < min || secs > max {
		t.Fatalf("Retry-After=%d not in range [%d,%d]", secs, min, max)
	}
}
