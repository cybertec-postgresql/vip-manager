package checker

import (
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// newTestThrottler returns a throttler writing into an observed logger, using
// a tiny interval so that the tests do not have to wait for a minute.
func newTestThrottler(t *testing.T, interval time.Duration) (*logThrottler, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	throttler := newLogThrottler(zap.New(core))
	throttler.interval = interval
	return throttler, logs
}

// TestLogThrottler_SuppressesRepeats ensures that a condition repeating within
// the interval is reported only once, as it happens while the DCS is down.
func TestLogThrottler_SuppressesRepeats(t *testing.T) {
	t.Parallel()
	throttler, logs := newTestThrottler(t, time.Minute)
	for range 10 {
		throttler.error("etcd is unreachable")
	}
	if got := logs.Len(); got != 1 {
		t.Fatalf("expected 1 log entry, got %d", got)
	}
	entry := logs.All()[0]
	if entry.Level != zapcore.ErrorLevel {
		t.Errorf("expected error level, got %s", entry.Level)
	}
	if len(entry.Context) != 0 {
		t.Errorf("expected no extra fields on the first report, got %v", entry.Context)
	}
}

// TestLogThrottler_ReportsAfterInterval ensures that a lasting condition is
// reported again once the interval has passed, including the number of
// occurrences that were suppressed in the meantime.
func TestLogThrottler_ReportsAfterInterval(t *testing.T) {
	t.Parallel()
	throttler, logs := newTestThrottler(t, time.Millisecond)
	throttler.error("etcd is unreachable")
	throttler.error("etcd is unreachable")
	time.Sleep(2 * time.Millisecond)
	throttler.error("etcd is unreachable")

	if got := logs.Len(); got != 2 {
		t.Fatalf("expected 2 log entries, got %d", got)
	}
	fields := logs.All()[1].ContextMap()
	if fields["suppressed"] != int64(1) {
		t.Errorf("expected 1 suppressed occurrence, got %v", fields["suppressed"])
	}
	if _, ok := fields["repeating_for"]; !ok {
		t.Errorf("expected the repeating_for field, got %v", fields)
	}
}

// TestLogThrottler_DifferentMessage ensures that a change of the condition is
// reported immediately instead of being hidden by the previous one.
func TestLogThrottler_DifferentMessage(t *testing.T) {
	t.Parallel()
	throttler, logs := newTestThrottler(t, time.Minute)
	throttler.error("etcd is unreachable")
	throttler.error("etcd has no leader")
	if got := logs.Len(); got != 2 {
		t.Fatalf("expected 2 log entries, got %d", got)
	}
}

// TestLogThrottler_SuccessReportsRecovery ensures that a silenced condition is
// followed by a message telling that it is over, and that success on its own
// stays silent.
func TestLogThrottler_SuccessReportsRecovery(t *testing.T) {
	t.Parallel()
	throttler, logs := newTestThrottler(t, time.Minute)
	throttler.success("etcd is reachable again") // nothing failed yet
	if got := logs.Len(); got != 0 {
		t.Fatalf("expected no log entries, got %d", got)
	}

	for range 3 {
		throttler.error("etcd is unreachable")
	}
	throttler.success("etcd is reachable again")

	entries := logs.All()
	if len(entries) != 2 {
		t.Fatalf("expected 2 log entries, got %d", len(entries))
	}
	recovery := entries[1]
	if recovery.Level != zapcore.InfoLevel {
		t.Errorf("expected info level, got %s", recovery.Level)
	}
	if got := recovery.ContextMap()["occurrences"]; got != int64(3) {
		t.Errorf("expected 3 occurrences, got %v", got)
	}

	// after the recovery the very same condition is reported anew
	throttler.error("etcd is unreachable")
	if got := logs.Len(); got != 3 {
		t.Fatalf("expected 3 log entries, got %d", got)
	}
}

// TestLogThrottler_Levels ensures that warn and info entries are throttled the
// same way and keep their level.
func TestLogThrottler_Levels(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		log   func(*logThrottler, string)
		level zapcore.Level
	}{
		{"warn", func(t *logThrottler, m string) { t.warn(m) }, zapcore.WarnLevel},
		{"info", func(t *logThrottler, m string) { t.info(m) }, zapcore.InfoLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			throttler, logs := newTestThrottler(t, time.Minute)
			tc.log(throttler, "the key is missing")
			tc.log(throttler, "the key is missing")
			if got := logs.Len(); got != 1 {
				t.Fatalf("expected 1 log entry, got %d", got)
			}
			if got := logs.All()[0].Level; got != tc.level {
				t.Errorf("expected %s level, got %s", tc.level, got)
			}
		})
	}
}
