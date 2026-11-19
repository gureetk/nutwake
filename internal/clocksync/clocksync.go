// Package clocksync tells whether the system clock can be trusted yet.
//
// A Raspberry Pi has no battery-backed clock. After a power cut it boots with
// the time it last saved, a few hours stale, until NTP sets it. Anything
// stamped before that point would be wrong, and a "power restored" event is
// exactly what gets written in those first seconds.
//
// Go times carry a monotonic reading, so the gap between two time.Now() calls
// stays correct even when the wall clock jumps in between. Fix uses that to
// move a time captured before the clock was set onto the correct wall time.
package clocksync

import "time"

// Clock reports whether wall-clock time can be trusted. A nil *Clock always
// is, which is what short-lived commands use.
type Clock struct {
	synced  func() bool
	timeout time.Duration
	started time.Time
}

// New returns a Clock backed by the kernel's synchronised flag. If the clock
// is still unsynchronised after timeout, Ready reports true anyway, so a Pi
// with no internet still records events (flagged as unsynced).
func New(timeout time.Duration) *Clock { return NewWith(systemSynced, timeout) }

// NewWith is New with a custom sync check, for tests.
func NewWith(synced func() bool, timeout time.Duration) *Clock {
	return &Clock{synced: synced, timeout: timeout, started: time.Now()}
}

// Trusted reports whether the clock has been set.
func (c *Clock) Trusted() bool { return c == nil || c.synced() }

// TimedOut reports whether the wait for the clock has run out.
func (c *Clock) TimedOut() bool { return c != nil && time.Since(c.started) >= c.timeout }

// Ready reports whether it is time to write timestamps: the clock is trusted,
// or we have waited long enough.
func (c *Clock) Ready() bool { return c.Trusted() || c.TimedOut() }

// Fix converts a time captured earlier by time.Now() to the correct wall time.
// It only changes anything once the clock is trusted.
func (c *Clock) Fix(t time.Time) time.Time {
	if c == nil || !c.Trusted() {
		return t
	}
	return time.Now().Add(-time.Since(t))
}
