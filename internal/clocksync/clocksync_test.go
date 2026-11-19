package clocksync

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestNilClockIsAlwaysReady(t *testing.T) {
	var c *Clock
	if !c.Trusted() || !c.Ready() || c.TimedOut() {
		t.Error("a nil clock should be trusted, ready and never timed out")
	}
	now := time.Now()
	if got := c.Fix(now); !got.Equal(now) {
		t.Errorf("Fix on a nil clock changed the time: %v", got)
	}
}

func TestTrustedFollowsTheSyncFlag(t *testing.T) {
	var synced atomic.Bool
	c := NewWith(synced.Load, time.Hour)
	if c.Trusted() || c.Ready() {
		t.Error("unsynced clock before the timeout should not be ready")
	}
	synced.Store(true)
	if !c.Trusted() || !c.Ready() {
		t.Error("synced clock should be ready")
	}
}

func TestReadyAfterTimeoutEvenIfUnsynced(t *testing.T) {
	c := NewWith(func() bool { return false }, 30*time.Millisecond)
	if c.Ready() {
		t.Fatal("ready too early")
	}
	time.Sleep(60 * time.Millisecond)
	if !c.TimedOut() || !c.Ready() {
		t.Error("should be ready after the timeout")
	}
	if c.Trusted() {
		t.Error("an unsynced clock is still not trusted after the timeout")
	}
}

func TestFixOnlyActsWhenTrusted(t *testing.T) {
	var synced atomic.Bool
	c := NewWith(synced.Load, time.Hour)
	captured := time.Now()
	if got := c.Fix(captured); !got.Equal(captured) {
		t.Error("Fix must leave times alone while the clock is untrusted")
	}
	time.Sleep(20 * time.Millisecond)
	synced.Store(true)
	got := c.Fix(captured)
	if d := got.Sub(captured); d < -5*time.Millisecond || d > 5*time.Millisecond {
		t.Errorf("with an unchanged wall clock Fix should be a no-op, moved it by %v", d)
	}
}

// Once the clock is trusted, a time captured earlier keeps its distance from
// "now". (The wall clock itself cannot be moved in a unit test; the guarantee
// that makes this work after a real jump comes from Go's monotonic readings.)
func TestFixKeepsDistanceFromNow(t *testing.T) {
	var synced atomic.Bool
	c := NewWith(synced.Load, time.Hour)
	captured := time.Now()
	time.Sleep(50 * time.Millisecond)
	synced.Store(true)

	fixed := c.Fix(captured)
	ago := time.Since(fixed)
	if ago < 40*time.Millisecond || ago > 500*time.Millisecond {
		t.Errorf("fixed time should be about 50ms ago, was %v ago", ago)
	}
}

func TestSystemSyncedDoesNotPanic(t *testing.T) {
	_ = systemSynced()
}
