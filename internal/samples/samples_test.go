package samples

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/clocksync"
	"github.com/gureetk/nutwake/internal/nut"
)

func f(v float64) *float64 { return &v }

var t0 = time.Date(2026, 11, 13, 19, 0, 0, 0, time.UTC)

func open(t *testing.T, clock *clocksync.Clock) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	s, err := Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestRoundTripAndRange(t *testing.T) {
	s, _ := open(t, nil)
	s.Add(Sample{T: t0, UPS: nut.StateOnline, Charge: f(100), Up: []string{"a", "b"}})
	s.Add(Sample{T: t0.Add(time.Hour), UPS: nut.StateOnBattery, Charge: f(80), Up: []string{"a"}, Down: []string{"b"}})
	s.Add(Sample{T: t0.Add(2 * time.Hour), UPS: nut.StateOnline})
	s.FlushNow()

	got, err := s.Range(t0.Add(30*time.Minute), t0.Add(2*time.Hour))
	if err != nil || len(got) != 2 {
		t.Fatalf("got %d samples, err %v", len(got), err)
	}
	if got[0].UPS != nut.StateOnBattery || *got[0].Charge != 80 || got[0].Down[0] != "b" {
		t.Errorf("sample lost data: %+v", got[0])
	}
	if got[1].Charge != nil {
		t.Errorf("a missing charge must stay missing: %+v", got[1])
	}
	all, _ := s.Range(time.Time{}, t0.Add(24*time.Hour))
	if len(all) != 3 {
		t.Errorf("full range has %d", len(all))
	}
}

func TestFileIsCompactJSONLinesInUTCSeconds(t *testing.T) {
	s, path := open(t, nil)
	s.Add(Sample{T: time.Date(2026, 11, 13, 12, 0, 5, 123456789, time.FixedZone("x", -7*3600)), UPS: nut.StateOnline, Charge: f(97)})
	s.FlushNow()
	raw, _ := os.ReadFile(path)
	want := `{"t":"2026-11-13T19:00:05Z","ups":"online","charge":97}` + "\n"
	if string(raw) != want {
		t.Errorf("file = %q, want %q", raw, want)
	}
}

func TestQuietSamplesWaitButOffMainsOnesDoNot(t *testing.T) {
	s, path := open(t, nil)
	s.Add(Sample{T: t0, UPS: nut.StateOnline, Charge: f(100)})
	s.Flush()
	if raw, _ := os.ReadFile(path); len(raw) != 0 {
		t.Error("a quiet sample should wait in memory")
	}
	// ...but it is still visible to readers
	if got, _ := s.Range(time.Time{}, t0.Add(time.Hour)); len(got) != 1 {
		t.Errorf("queued sample not visible: %d", len(got))
	}
	s.Add(Sample{T: t0.Add(5 * time.Second), UPS: nut.StateOnBattery, Charge: f(99)})
	s.Flush()
	if raw, _ := os.ReadFile(path); strings.Count(string(raw), "\n") != 2 {
		t.Errorf("an off-mains sample should flush everything queued: %q", raw)
	}
}

func TestHeldBackUntilTheClockIsSet(t *testing.T) {
	var synced atomic.Bool
	s, path := open(t, clocksync.NewWith(synced.Load, time.Hour))
	before := time.Now()
	s.Add(Sample{T: time.Now(), UPS: nut.StateOnBattery, Charge: f(70)})
	s.Flush()
	if raw, _ := os.ReadFile(path); len(raw) != 0 {
		t.Fatal("written before the clock was set")
	}
	if got, _ := s.Range(time.Time{}, time.Now().Add(time.Hour)); len(got) != 0 {
		t.Error("a sample with an unknown time must not be shown")
	}
	time.Sleep(20 * time.Millisecond)
	synced.Store(true)
	s.Flush()
	got, _ := s.Range(time.Time{}, time.Now().Add(time.Hour))
	if len(got) != 1 {
		t.Fatalf("got %d samples", len(got))
	}
	if got[0].T.Before(before.Add(-2*time.Second)) || got[0].T.After(time.Now()) {
		t.Errorf("time %v is not about now", got[0].T)
	}
}

func TestTrimDropsOldSamples(t *testing.T) {
	s, _ := open(t, nil)
	for i := 0; i < 10; i++ {
		s.Add(Sample{T: t0.Add(time.Duration(i) * 24 * time.Hour), UPS: nut.StateOnline})
	}
	s.FlushNow()
	if err := s.Trim(3*24*time.Hour, t0.Add(9*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Range(time.Time{}, t0.Add(100*24*time.Hour))
	if len(got) != 4 || !got[0].T.Equal(t0.Add(6*24*time.Hour)) {
		t.Errorf("after trim: %d samples, first %v", len(got), got[0].T)
	}
	if err := s.Trim(1000*24*time.Hour, t0.Add(9*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Range(time.Time{}, t0.Add(100*24*time.Hour)); len(got) != 4 {
		t.Error("nothing should be dropped by a long keep")
	}
}

func TestBefore(t *testing.T) {
	s, _ := open(t, nil)
	if _, ok := s.Before(t0); ok {
		t.Error("empty store")
	}
	s.Add(Sample{T: t0, UPS: nut.StateOnline, Charge: f(100)})
	s.Add(Sample{T: t0.Add(time.Hour), UPS: nut.StateOnBattery})
	s.FlushNow()
	if got, ok := s.Before(t0.Add(30 * time.Minute)); !ok || got.UPS != nut.StateOnline {
		t.Errorf("Before = %+v, %v", got, ok)
	}
	if _, ok := s.Before(t0); ok {
		t.Error("nothing is before the first sample")
	}
}

func TestMalformedLinesAreSkippedAndOrderIsRestored(t *testing.T) {
	s, path := open(t, nil)
	os.WriteFile(path, []byte(`{"t":"2026-11-13T21:00:00Z","ups":"online"}
not json

{"ups":"online"}
{"t":"2026-11-13T19:00:00Z","ups":"online"}
`), 0o644)
	got, err := s.Range(time.Time{}, t0.Add(24*time.Hour))
	if err != nil || len(got) != 2 || !got[0].T.Before(got[1].T) {
		t.Errorf("got %+v, err %v", got, err)
	}
}

func TestOtherWritersAreSeen(t *testing.T) {
	s, path := open(t, nil)
	s.Add(Sample{T: t0, UPS: nut.StateOnline})
	s.FlushNow()
	if got, _ := s.Range(time.Time{}, t0.Add(time.Hour)); len(got) != 1 {
		t.Fatal("setup")
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`{"t":"2026-11-13T19:30:00Z","ups":"online"}` + "\n")
	f.Close()
	if got, _ := s.Range(time.Time{}, t0.Add(time.Hour)); len(got) != 2 {
		t.Errorf("cache not refreshed: %d samples", len(got))
	}
}

func TestQueueIsBounded(t *testing.T) {
	s, _ := open(t, clocksync.NewWith(func() bool { return false }, time.Hour))
	for i := 0; i < maxQueue+100; i++ {
		s.Add(Sample{T: t0, UPS: nut.StateOnline})
	}
	s.mu.Lock()
	n := len(s.queue)
	s.mu.Unlock()
	if n != maxQueue {
		t.Errorf("queue = %d", n)
	}
}

func TestConcurrentUse(t *testing.T) {
	s, _ := open(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.Add(Sample{T: t0, UPS: nut.StateOnBattery})
			s.Flush()
		}()
		go func() {
			defer wg.Done()
			_, _ = s.Range(time.Time{}, t0.Add(time.Hour))
		}()
	}
	wg.Wait()
	s.FlushNow()
	if got, _ := s.Range(time.Time{}, t0.Add(time.Hour)); len(got) != 20 {
		t.Errorf("got %d samples, want 20", len(got))
	}
}
