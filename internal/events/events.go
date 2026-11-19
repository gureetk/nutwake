// Package events keeps nutwake's history: power cuts, shutdowns, wake-ups.
//
// Events are appended to a JSON-lines file, one per line. The file is
// append-only so the daemon and the shutdown helper (a separate process run by
// upsmon) can both write to it, and a crash can lose at most the last line.
package events

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/gureetk/nutwake/internal/clocksync"
	"github.com/gureetk/nutwake/internal/fsutil"
)

// Level says how an event should be shown.
type Level string

const (
	LevelOK   Level = "ok"   // something recovered
	LevelInfo Level = "info" // neutral progress
	LevelWarn Level = "warn" // something is going wrong
	LevelCrit Level = "crit" // action was forced
)

// Event kinds.
const (
	KindStart          = "daemon_start"
	KindStop           = "daemon_stop"
	KindPowerLost      = "power_lost"
	KindPowerRestored  = "power_restored"
	KindLowBattery     = "low_battery"
	KindReplaceBattery = "replace_battery"
	KindUPSUnreachable = "ups_unreachable"
	KindUPSReconnected = "ups_reconnected"
	KindNodeUp         = "node_up"
	KindNodeDown       = "node_down"
	KindShutdownBegan  = "shutdown_began"
	KindNodesOff       = "nodes_off"
	KindNodesStuck     = "nodes_still_up"
	KindPiHalting      = "pi_halting"
	KindWakePending    = "wake_pending"
	KindWakeWaiting    = "wake_waiting"
	KindWakeSent       = "wake_sent"
	KindWakeOK         = "wake_ok"
	KindWakeSkipped    = "wake_skipped"
	KindWakeFailed     = "wake_failed"
	KindWakeManual     = "wake_manual"
)

// Event is one line of history.
type Event struct {
	Time    time.Time      `json:"time"`
	Kind    string         `json:"kind"`
	Level   Level          `json:"level"`
	Node    string         `json:"node,omitempty"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
}

// maxQueue bounds memory if the disk is unwritable for a long time.
const maxQueue = 1000

// Store reads and writes the event file.
type Store struct {
	path  string
	clock *clocksync.Clock

	mu    sync.Mutex
	queue []Event

	// cache of the parsed file, valid while size and mtime are unchanged
	cacheEvents []Event
	cacheSize   int64
	cacheMod    time.Time
	cacheOK     bool
}

// Open prepares the event file at path. If clock is non-nil, events are held
// back until the clock can be trusted (see package clocksync). Pass nil to
// write immediately, as a short-lived command should.
func Open(path string, clock *clocksync.Clock) (*Store, error) {
	if err := fsutil.AppendFile(path, nil, 0o644); err != nil {
		return nil, err
	}
	return &Store{path: path, clock: clock}, nil
}

// Add records an event stamped with the current time.
func (s *Store) Add(level Level, kind, node, message string, data map[string]any) {
	s.AddEvent(Event{Time: time.Now(), Kind: kind, Level: level, Node: node, Message: message, Data: data})
}

// AddEvent records e. A zero Time means now.
func (s *Store) AddEvent(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, e)
	if len(s.queue) > maxQueue {
		s.queue = s.queue[len(s.queue)-maxQueue:]
	}
	s.flushLocked(false)
}

// Flush writes held-back events if the clock is now ready. The daemon calls it
// on every poll.
func (s *Store) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked(false)
}

// FlushNow writes everything held back, whatever the clock says. Used when the
// daemon stops.
func (s *Store) FlushNow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked(true)
}

// Pending reports how many events are waiting for the clock.
func (s *Store) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

func (s *Store) flushLocked(force bool) {
	if len(s.queue) == 0 {
		return
	}
	if !force && s.clock != nil && !s.clock.Ready() {
		return
	}
	trusted := s.clock.Trusted() // a nil clock is trusted
	var buf bytes.Buffer
	for _, e := range s.queue {
		if s.clock != nil {
			if trusted {
				e.Time = s.clock.Fix(e.Time)
			} else {
				e.Data = withFlag(e.Data, "clock_unsynced", true)
			}
		}
		e.Time = e.Time.Round(0).UTC() // drop the monotonic reading, store UTC
		line, err := json.Marshal(e)
		if err != nil {
			slog.Error("events: cannot encode event", "kind", e.Kind, "err", err)
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := fsutil.AppendFile(s.path, buf.Bytes(), 0o644); err != nil {
		slog.Error("events: cannot write history, will retry", "path", s.path, "err", err)
		return // keep the queue and try again on the next flush
	}
	s.queue = nil
}

func withFlag(m map[string]any, k string, v any) map[string]any {
	out := make(map[string]any, len(m)+1)
	for key, val := range m {
		out[key] = val
	}
	out[k] = v
	return out
}

// All returns every event in the file, oldest first. Lines that cannot be
// parsed are skipped.
func (s *Store) All() ([]Event, error) {
	fi, err := os.Stat(s.path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cacheOK && fi.Size() == s.cacheSize && fi.ModTime().Equal(s.cacheMod) {
		return s.cacheEvents, nil
	}
	evs, err := readFile(s.path)
	if err != nil {
		return nil, err
	}
	s.cacheEvents, s.cacheSize, s.cacheMod, s.cacheOK = evs, fi.Size(), fi.ModTime(), true
	return evs, nil
}

func readFile(path string) ([]Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.Kind == "" {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// Recent returns up to limit events, newest first, that pass keep (nil keeps
// all). Events are ordered by their timestamp, so ones written late (held
// back for the clock) still land in the right place.
func (s *Store) Recent(limit int, keep func(Event) bool) ([]Event, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, e := range all {
		if keep == nil || keep(e) {
			out = append(out, e)
		}
	}
	slices.Reverse(out) // file order is oldest first; equal timestamps then keep write order
	sortNewestFirst(out)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Trim keeps only the newest max lines of the file.
func (s *Store) Trim(max int) error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	if len(lines) <= max {
		return nil
	}
	keep := bytes.Join(lines[len(lines)-max:], nil)
	if err := fsutil.WriteFileAtomic(s.path, keep, 0o644); err != nil {
		return err
	}
	return nil
}

// ErrNone is returned by Latest when no event of that kind exists.
var ErrNone = errors.New("no such event")

// Latest returns the newest event of the given kind.
func (s *Store) Latest(kind string) (Event, error) {
	evs, err := s.Recent(1, func(e Event) bool { return e.Kind == kind })
	if err != nil {
		return Event{}, err
	}
	if len(evs) == 0 {
		return Event{}, ErrNone
	}
	return evs[0], nil
}
