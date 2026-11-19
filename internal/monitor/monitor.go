// Package monitor watches the UPS and the nodes, and keeps the record of
// power cuts.
//
// It polls on a timer. Each poll reads the UPS, probes every node, and turns
// the changes into events: power lost, battery low, power restored, node up,
// node down. A power cut in progress is saved to disk, so a restart, or the
// reboot of the Pi itself, does not lose it. When the daemon starts and finds
// a saved power cut but the mains is back, it closes the record then.
//
// Times use time.Now() with its monotonic reading until the system clock can
// be trusted (see package clocksync), and are corrected afterwards.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gureetk/nutwake/internal/clocksync"
	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/probe"
	"github.com/gureetk/nutwake/internal/state"
)

const (
	// upsDownAfter is how many failed reads in a row count as "cannot reach
	// the UPS". One missed read during a busy moment is not worth an event.
	upsDownAfter = 3
	// sampleEvery spaces the battery samples while nothing is happening.
	sampleEvery = 30 * time.Second
	// maxSamples bounds memory: about three and a half days at one every 30s.
	maxSamples = 10080
	// saveEvery limits how often a power cut in progress is rewritten to disk.
	saveEvery = 30 * time.Second
	// trimEvery is how often the event file is cut back to history.max_events.
	trimEvery = time.Hour
)

// Reader reads the UPS. *nut.Client satisfies it.
type Reader interface {
	Read(ctx context.Context) (*nut.Reading, error)
}

// Deps are the things a Monitor works with. Events, State are required.
type Deps struct {
	Events *events.Store
	State  *state.Store
	// Clock gates timestamps. Nil means the clock can always be trusted.
	Clock *clocksync.Clock
	// UPS defaults to a NUT client built from the config.
	UPS Reader
	// Probers are keyed by node name; missing ones are built from the config.
	Probers map[string]probe.Prober
	// Now defaults to time.Now.
	Now func() time.Time
}

// Monitor tracks the UPS and the nodes. Create one with New and call Run, or
// call Poll yourself. Snapshot may be called from any goroutine.
type Monitor struct {
	cfg    *config.Config
	reader Reader
	events *events.Store
	store  *state.Store
	clock  *clocksync.Clock
	now    func() time.Time

	mu      sync.Mutex
	started time.Time
	nodes   []*node
	power   upsState
	outage  *outage
	closing *closing
	samples []Sample
}

// New builds a Monitor and loads a power cut left over from a previous run.
func New(cfg *config.Config, d Deps) (*Monitor, error) {
	if d.Events == nil || d.State == nil {
		return nil, errors.New("monitor: Events and State are required")
	}
	m := &Monitor{
		cfg: cfg, reader: d.UPS, events: d.Events, store: d.State, clock: d.Clock, now: d.Now,
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.reader == nil {
		m.reader = &nut.Client{Addr: cfg.NUT.Addr(), UPS: cfg.NUT.UPS, Timeout: cfg.NUT.Timeout}
	}
	for _, n := range cfg.Nodes {
		p := d.Probers[n.Name]
		if p == nil {
			var err error
			if p, err = probe.Parse(cfg.ProbeSpec(n), cfg.Probe.Timeout); err != nil {
				return nil, fmt.Errorf("node %s: %w", n.Name, err)
			}
		}
		m.nodes = append(m.nodes, &node{cfg: n, probe: cfg.ProbeSpec(n), prober: p})
	}
	m.started = m.now()
	m.power.state = nut.StateUnreachable

	switch o, err := m.store.LoadOutage(); {
	case err != nil:
		slog.Warn("monitor: ignoring unreadable outage record", "err", err)
		_ = m.store.ClearOutage()
	case o != nil:
		m.outage = &outage{Outage: *o, persisted: true, fromDisk: true, lastSave: m.now()}
		slog.Info("monitor: found a power cut from before the restart", "started", o.Started)
	}
	return m, nil
}

// Run polls until ctx is cancelled.
func (m *Monitor) Run(ctx context.Context) {
	tick := time.NewTicker(m.cfg.PollInterval)
	defer tick.Stop()
	nextTrim := time.Time{}
	for {
		m.Poll(ctx)
		if now := time.Now(); now.After(nextTrim) && m.clock.Ready() {
			if err := m.events.Trim(m.cfg.History.MaxEvents); err != nil {
				slog.Warn("monitor: cannot trim history", "err", err)
			}
			nextTrim = now.Add(trimEvery)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Poll does one cycle: read the UPS, probe the nodes, record what changed.
func (m *Monitor) Poll(ctx context.Context) {
	var (
		wg      sync.WaitGroup
		reading *nut.Reading
		readErr error
		up      = make([]bool, len(m.nodes))
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		reading, readErr = m.reader.Read(ctx)
	}()
	for i, n := range m.nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			up[i] = n.prober.Up(ctx, n.cfg.Host)
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return // shutting down: do not record failures caused by the cancel
	}

	m.mu.Lock()
	now := m.now()
	for i, n := range m.nodes {
		m.applyNode(n, up[i], now)
	}
	m.applyUPS(reading, readErr, now)
	m.settle(now)
	m.mu.Unlock()

	m.events.Flush()
}

// fix returns t corrected for a clock that was set after t was captured.
func (m *Monitor) fix(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return m.clock.Fix(t)
}

func (m *Monitor) emit(level events.Level, kind, node, msg string, data map[string]any, at time.Time) {
	m.events.AddEvent(events.Event{Time: at, Kind: kind, Level: level, Node: node, Message: msg, Data: data})
}
