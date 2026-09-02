package checker

import (
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// repeatInterval is the minimal delay between two reports of the same
// repeating condition. While the DCS is unreachable the checkers run into the
// very same failure once per scan interval, which used to fill up the disk.
const repeatInterval = time.Minute

// logThrottler collapses a repeating message into a single report plus a
// reminder every repeatInterval, telling how many occurrences were suppressed
// in between. Every call site gets its own throttler, so that a message which
// changes within one site is reported immediately.
type logThrottler struct {
	// both loggers write to the same destination, they only differ in the
	// caller skip matching the call depth of the method using them, so that
	// verbose logging reports the call site instead of this file
	logger        *zap.Logger // used by log(), reached via error/warn/info
	successLogger *zap.Logger // used by success()

	interval time.Duration // minimal delay between two reports of the same message

	mu         sync.Mutex
	msg        string    // message of the currently repeating condition
	first      time.Time // when the current streak started
	lastLogged time.Time
	count      int // occurrences in the current streak
	suppressed int // occurrences not logged since the last report
}

func newLogThrottler(logger *zap.Logger) *logThrottler {
	return &logThrottler{
		logger:        logger.WithOptions(zap.AddCallerSkip(2)),
		successLogger: logger.WithOptions(zap.AddCallerSkip(1)),
		interval:      repeatInterval,
	}
}

// error reports msg at the error level unless the same message has already
// been reported less than repeatInterval ago.
func (t *logThrottler) error(msg string, fields ...zap.Field) {
	t.log(zapcore.ErrorLevel, msg, fields...)
}

// warn reports msg at the warning level unless the same message has already
// been reported less than repeatInterval ago.
func (t *logThrottler) warn(msg string, fields ...zap.Field) {
	t.log(zapcore.WarnLevel, msg, fields...)
}

// info reports msg at the info level unless the same message has already been
// reported less than repeatInterval ago.
func (t *logThrottler) info(msg string, fields ...zap.Field) {
	t.log(zapcore.InfoLevel, msg, fields...)
}

func (t *logThrottler) log(level zapcore.Level, msg string, fields ...zap.Field) {
	t.mu.Lock()
	now := time.Now()
	if msg != t.msg { // a different condition, start a new streak
		t.msg, t.first, t.count, t.suppressed = msg, now, 0, 0
	}
	t.count++
	if t.count > 1 && now.Sub(t.lastLogged) < t.interval {
		t.suppressed++
		t.mu.Unlock()
		return
	}
	suppressed, since := t.suppressed, now.Sub(t.first)
	t.lastLogged, t.suppressed = now, 0
	t.mu.Unlock()

	if suppressed > 0 {
		fields = append(fields,
			zap.Int("suppressed", suppressed),
			zap.Duration("repeating_for", since))
	}
	if ce := t.logger.Check(level, msg); ce != nil {
		ce.Write(fields...)
	}
}

// success ends the current streak. If anything was reported before, the
// recovery is logged, so that a silenced condition is always followed by a
// visible message telling that it is over.
func (t *logThrottler) success(msg string, fields ...zap.Field) {
	t.mu.Lock()
	if t.count == 0 {
		t.mu.Unlock()
		return
	}
	count, since := t.count, time.Since(t.first)
	t.msg, t.count, t.suppressed, t.lastLogged = "", 0, 0, time.Time{}
	t.mu.Unlock()

	t.successLogger.Info(msg, append(fields,
		zap.Int("occurrences", count),
		zap.Duration("lasted", since))...)
}
