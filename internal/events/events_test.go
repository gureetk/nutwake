package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/clocksync"
)

func open(t *testing.T, clock *clocksync.Clock) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "events.jsonl")
	s, err := Open(path, clock)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestAddAndRecentNewestFirst(t *testing.T) {
	s, _ := open(t, nil)
	base := time.Date(2026, 11, 13, 19, 52, 0, 0, time.UTC)
	s.AddEvent(Event{Time: base, Kind: KindPowerLost, Level: LevelWarn, Message: "lost"})
	s.AddEvent(Event{Time: base.Add(27 * time.Minute), Kind: KindLowBattery, Level: LevelCrit, Message: "low"})
	s.AddEvent(Event{Time: base.Add(72 * time.Minute), Kind: KindPowerRestored, Level: LevelOK, Message: "back", Node: "", Data: map[string]any{"duration_s": 4320}})

	got, err := s.Recent(0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Kind != KindPowerRestored || got[2].Kind != KindPowerLost {
		t.Fatalf("order wrong: %+v", got)
	}
	if got[0].Data["duration_s"] != float64(4320) {
		t.Errorf("data lost: %+v", got[0].Data)
	}
	if !got[0].Time.Equal(base.Add(72 * time.Minute)) {
		t.Errorf("time changed: %v", got[0].Time)
	}
	if two, _ := s.Recent(2, nil); len(two) != 2 {
		t.Errorf("limit ignored: %d", len(two))
	}
	if only, _ := s.Recent(10, func(e Event) bool { return e.Level == LevelCrit }); len(only) != 1 || only[0].Kind != KindLowBattery {
		t.Errorf("filter wrong: %+v", only)
	}
}

func TestSameTimestampKeepsWriteOrder(t *testing.T) {
	s, _ := open(t, nil)
	at := time.Date(2026, 11, 13, 21, 4, 0, 0, time.UTC)
	s.AddEvent(Event{Time: at, Kind: KindStart, Level: LevelInfo, Message: "first written"})
	s.AddEvent(Event{Time: at, Kind: KindPowerRestored, Level: LevelOK, Message: "second written"})
	got, _ := s.Recent(0, nil)
	if got[0].Kind != KindPowerRestored {
		t.Errorf("the later write should come first: %+v", got)
	}
}

func TestEventsAreStoredInUTCWithoutMonotonicReading(t *testing.T) {
	s, path := open(t, nil)
	s.Add(LevelInfo, KindStart, "", "hello", nil)
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), "Z\"") || strings.Contains(string(raw), "m=") {
		t.Errorf("unexpected time encoding: %s", raw)
	}
}

func TestHeldBackUntilClockIsSet(t *testing.T) {
	var synced atomic.Bool
	s, path := open(t, clocksync.NewWith(synced.Load, time.Hour))

	before := time.Now()
	s.Add(LevelOK, KindPowerRestored, "", "back", nil)
	s.Add(LevelInfo, KindStart, "", "started", nil)
	if raw, _ := os.ReadFile(path); len(raw) != 0 {
		t.Fatalf("events written before the clock was set: %s", raw)
	}
	if s.Pending() != 2 {
		t.Fatalf("pending = %d", s.Pending())
	}

	time.Sleep(30 * time.Millisecond)
	synced.Store(true)
	s.Flush()
	if s.Pending() != 0 {
		t.Fatalf("still pending: %d", s.Pending())
	}
	got, _ := s.Recent(0, nil)
	if len(got) != 2 {
		t.Fatalf("got %d events", len(got))
	}
	for _, e := range got {
		if e.Time.Before(before.Add(-time.Second)) || e.Time.After(time.Now().Add(time.Second)) {
			t.Errorf("timestamp %v is not about now", e.Time)
		}
		if _, flagged := e.Data["clock_unsynced"]; flagged {
			t.Error("events written with a trusted clock must not be flagged")
		}
	}
}

func TestWrittenFlaggedWhenTheWaitTimesOut(t *testing.T) {
	s, _ := open(t, clocksync.NewWith(func() bool { return false }, 40*time.Millisecond))
	s.Add(LevelInfo, KindStart, "", "started", map[string]any{"keep": "me"})
	if s.Pending() != 1 {
		t.Fatal("should be held back at first")
	}
	time.Sleep(80 * time.Millisecond)
	s.Flush()
	got, _ := s.Recent(0, nil)
	if len(got) != 1 || got[0].Data["clock_unsynced"] != true || got[0].Data["keep"] != "me" {
		t.Errorf("want a flagged event with its data: %+v", got)
	}
}

func TestFlushNowWritesEverything(t *testing.T) {
	s, _ := open(t, clocksync.NewWith(func() bool { return false }, time.Hour))
	s.Add(LevelInfo, KindStop, "", "stopping", nil)
	s.FlushNow()
	if got, _ := s.Recent(0, nil); len(got) != 1 || got[0].Data["clock_unsynced"] != true {
		t.Errorf("FlushNow result: %+v", got)
	}
}

func TestCallerDataIsNotMutated(t *testing.T) {
	s, _ := open(t, clocksync.NewWith(func() bool { return false }, time.Hour))
	data := map[string]any{"a": 1}
	s.Add(LevelInfo, KindStart, "", "x", data)
	s.FlushNow()
	if _, ok := data["clock_unsynced"]; ok {
		t.Error("the caller's map was modified")
	}
}

func TestOtherProcessAppendsAreSeen(t *testing.T) {
	s, path := open(t, nil)
	s.Add(LevelInfo, KindStart, "", "from the daemon", nil)
	if got, _ := s.All(); len(got) != 1 {
		t.Fatal("setup")
	}
	// the shutdown helper is a separate process with its own Store
	other, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	other.Add(LevelWarn, KindShutdownBegan, "", "from the helper", nil)
	got, _ := s.All()
	if len(got) != 2 {
		t.Errorf("the daemon's view did not pick up the helper's event: %d events", len(got))
	}
}

func TestMalformedLinesAreSkipped(t *testing.T) {
	s, path := open(t, nil)
	s.Add(LevelInfo, KindStart, "", "good", nil)
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("{not json\n\n{\"time\":\"2026-01-01T00:00:00Z\"}\n")
	f.Close()
	s.Add(LevelInfo, KindStop, "", "also good", nil)
	got, err := s.Recent(0, nil)
	if err != nil || len(got) != 2 {
		t.Errorf("got %d events, err %v", len(got), err)
	}
}

func TestTrimKeepsTheNewest(t *testing.T) {
	s, _ := open(t, nil)
	for i := 0; i < 10; i++ {
		s.AddEvent(Event{Time: time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC), Kind: KindStart, Level: LevelInfo, Message: fmt.Sprint(i)})
	}
	if err := s.Trim(4); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Recent(0, nil)
	if len(got) != 4 || got[0].Message != "9" || got[3].Message != "6" {
		t.Errorf("after trim: %+v", got)
	}
	if err := s.Trim(100); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Recent(0, nil); len(got) != 4 {
		t.Error("trim to a larger number should change nothing")
	}
}

func TestConcurrentAddsStayWholeLines(t *testing.T) {
	s, path := open(t, nil)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Add(LevelInfo, KindNodeUp, "n", fmt.Sprintf("event %d", i), nil)
		}(i)
	}
	wg.Wait()
	raw, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 40 {
		t.Fatalf("got %d lines", len(lines))
	}
	for _, l := range lines {
		var e Event
		if err := json.Unmarshal([]byte(l), &e); err != nil {
			t.Fatalf("bad line %q: %v", l, err)
		}
	}
}

func TestLatest(t *testing.T) {
	s, _ := open(t, nil)
	if _, err := s.Latest(KindPowerLost); err != ErrNone {
		t.Errorf("want ErrNone, got %v", err)
	}
	base := time.Now().Add(-time.Hour)
	s.AddEvent(Event{Time: base, Kind: KindPowerLost, Level: LevelWarn, Message: "old"})
	s.AddEvent(Event{Time: base.Add(time.Minute), Kind: KindPowerLost, Level: LevelWarn, Message: "new"})
	if e, err := s.Latest(KindPowerLost); err != nil || e.Message != "new" {
		t.Errorf("Latest = %+v, %v", e, err)
	}
}

func TestUnwritablePathKeepsEventsQueued(t *testing.T) {
	s, path := open(t, nil)
	_ = os.Remove(path)
	_ = os.Mkdir(path, 0o755) // a directory where the file should be
	s.Add(LevelInfo, KindStart, "", "cannot be written", nil)
	if s.Pending() != 1 {
		t.Errorf("a failed write should leave the event queued, pending=%d", s.Pending())
	}
}
