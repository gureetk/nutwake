// Package samples keeps the flight-recorder data: what the UPS and the nodes
// were doing, over time, kept across reboots of the Pi.
//
// A sample is written when something changes (the UPS state, the charge, a
// node going up or down) and every few minutes as a heartbeat. Between two
// samples the state is held, so a long quiet stretch costs one line. A gap
// longer than a few heartbeats means nothing was recording, which on this
// machine means the Pi was off.
//
// As with events, nothing is written until the system clock can be trusted.
package samples

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/gureetk/nutwake/internal/clocksync"
	"github.com/gureetk/nutwake/internal/fsutil"
	"github.com/gureetk/nutwake/internal/nut"
)

const (
	// flushEvery bounds how long a sample sits in memory in quiet times.
	flushEvery = 30 * time.Second
	// maxQueue bounds memory if the disk is unwritable for a long time.
	maxQueue = 2000
)

// Sample is one reading of the UPS and the nodes.
type Sample struct {
	T      time.Time `json:"t"`
	UPS    nut.State `json:"ups"`
	Charge *float64  `json:"charge,omitempty"`
	// Up and Down list the nodes by name. A node in neither list was not
	// known yet.
	Up   []string `json:"up,omitempty"`
	Down []string `json:"down,omitempty"`
}

// Store reads and writes the sample file.
type Store struct {
	path  string
	clock *clocksync.Clock

	mu        sync.Mutex
	queue     []Sample
	urgent    bool
	lastFlush time.Time

	cache     []Sample
	cacheSize int64
	cacheMod  time.Time
	cacheOK   bool
}

// Open prepares the sample file at path. A nil clock writes without waiting.
func Open(path string, clock *clocksync.Clock) (*Store, error) {
	if err := fsutil.AppendFile(path, nil, 0o644); err != nil {
		return nil, err
	}
	return &Store{path: path, clock: clock, lastFlush: time.Now()}, nil
}

// Add queues a sample. Samples taken off mains are written at the next Flush
// whatever the time; in quiet times they wait up to flushEvery.
func (s *Store) Add(sm Sample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, sm)
	if len(s.queue) > maxQueue {
		s.queue = s.queue[len(s.queue)-maxQueue:]
	}
	if sm.UPS != nut.StateOnline {
		s.urgent = true
	}
}

// Flush writes queued samples if the clock is ready and it is time.
func (s *Store) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 || !s.clock.Ready() {
		return
	}
	if !s.urgent && time.Since(s.lastFlush) < flushEvery {
		return
	}
	s.flushLocked()
}

// FlushNow writes everything, whatever the clock says. Used when stopping.
func (s *Store) FlushNow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked()
}

func (s *Store) flushLocked() {
	if len(s.queue) == 0 {
		return
	}
	trusted := s.clock.Trusted()
	var buf bytes.Buffer
	for _, sm := range s.queue {
		line, err := json.Marshal(s.stamp(sm, trusted))
		if err != nil {
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	if err := fsutil.AppendFile(s.path, buf.Bytes(), 0o644); err != nil {
		slog.Error("samples: cannot write the recorder file, will retry", "path", s.path, "err", err)
		return
	}
	s.queue, s.urgent, s.lastFlush = nil, false, time.Now()
}

// stamp gives a sample the time it is stored with: corrected for a clock set
// after it was taken, in UTC, to the second.
func (s *Store) stamp(sm Sample, trusted bool) Sample {
	if trusted {
		sm.T = s.clock.Fix(sm.T)
	}
	sm.T = sm.T.Round(0).UTC().Truncate(time.Second)
	return sm
}

// Range returns the samples from from to to inclusive, oldest first,
// including ones not yet written. Samples still waiting for the clock are
// left out, since their times would be wrong.
func (s *Store) Range(from, to time.Time) ([]Sample, error) {
	all, err := s.all()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	queued := append([]Sample(nil), s.queue...)
	trusted := s.clock.Trusted()
	s.mu.Unlock()

	var out []Sample
	for _, sm := range all {
		if !sm.T.Before(from) && !sm.T.After(to) {
			out = append(out, sm)
		}
	}
	if trusted {
		for _, sm := range queued {
			sm = s.stamp(sm, true)
			if !sm.T.Before(from) && !sm.T.After(to) {
				out = append(out, sm)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	return out, nil
}

// Before returns the last sample before t, so a chart can start from the state
// that was already in force.
func (s *Store) Before(t time.Time) (Sample, bool) {
	all, err := s.all()
	if err != nil {
		return Sample{}, false
	}
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].T.Before(t) {
			return all[i], true
		}
	}
	return Sample{}, false
}

// all reads the whole file, cached until it changes. Lines that cannot be
// parsed are skipped.
func (s *Store) all() ([]Sample, error) {
	fi, err := os.Stat(s.path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cacheOK && fi.Size() == s.cacheSize && fi.ModTime().Equal(s.cacheMod) {
		return s.cache, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var out []Sample
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var sm Sample
		if json.Unmarshal(line, &sm) != nil || sm.T.IsZero() {
			continue
		}
		out = append(out, sm)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	s.cache, s.cacheSize, s.cacheMod, s.cacheOK = out, fi.Size(), fi.ModTime(), true
	return out, nil
}

// Trim drops samples older than keep before now.
func (s *Store) Trim(keep time.Duration, now time.Time) error {
	all, err := s.all()
	if err != nil {
		return err
	}
	cut := now.Add(-keep)
	i := sort.Search(len(all), func(i int) bool { return !all[i].T.Before(cut) })
	if i == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, sm := range all[i:] {
		line, err := json.Marshal(sm)
		if err != nil {
			continue
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cacheOK = false
	return fsutil.WriteFileAtomic(s.path, buf.Bytes(), 0o644)
}
