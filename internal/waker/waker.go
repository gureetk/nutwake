// Package waker wakes the nodes that a power cut shut down.
//
// The shutdown helper writes wake.json, the list of nodes it shut down, just
// before the Pi halts. After the Pi reboots, the waker reads that list and
// works through it: it waits until power is back and steady and the battery
// has recharged a little, then sends a Wake-on-LAN packet to each node in
// turn, repeats it if the node does not come up, and gives up after a while.
//
// Only nodes on the list are woken. A node someone started by hand, or one
// that never went down, is left alone.
package waker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/humanize"
	"github.com/gureetk/nutwake/internal/monitor"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/state"
	"github.com/gureetk/nutwake/internal/wol"
)

// Source says what the UPS and the nodes are doing. *monitor.Monitor does.
type Source interface {
	Brief() monitor.Brief
}

// Sender sends one magic packet. wol.Send is the default.
type Sender func(addr string, mac net.HardwareAddr) error

// Deps are the things a Waker works with. Source, Events and State are required.
type Deps struct {
	Source Source
	Events *events.Store
	State  *state.Store
	Send   Sender
	Now    func() time.Time
}

// Why the waker is holding back.
const (
	WaitOnBattery     = "on_battery"
	WaitSettling      = "settling"
	WaitBattery       = "battery"
	WaitUPSState      = "ups_state"
	WaitUPSUnreadable = "ups_unreachable"
)

// Waker works through the list of nodes waiting to be woken.
type Waker struct {
	cfg    *config.Config
	macs   map[string]net.HardwareAddr
	src    Source
	events *events.Store
	store  *state.Store
	send   Sender
	now    func() time.Time

	started time.Time

	mu         sync.Mutex
	announced  bool
	begun      bool      // sending has started; only losing mains interrupts it
	nextFirst  time.Time // earliest time for the next first packet
	waitingFor string    // last wait reason that was written to the history
	tracks     map[string]*track
	status     Status
	loadErr    string
}

// track is what has happened to one node since sending began.
type track struct {
	attempts  int
	firstSent time.Time
	lastSent  time.Time
	lastErr   string
}

// New builds a Waker.
func New(cfg *config.Config, d Deps) (*Waker, error) {
	if d.Source == nil || d.Events == nil || d.State == nil {
		return nil, fmt.Errorf("waker: Source, Events and State are required")
	}
	w := &Waker{
		cfg: cfg, macs: map[string]net.HardwareAddr{}, src: d.Source, events: d.Events,
		store: d.State, send: d.Send, now: d.Now, tracks: map[string]*track{},
	}
	if w.send == nil {
		w.send = wol.Send
	}
	if w.now == nil {
		w.now = time.Now
	}
	for _, n := range cfg.Nodes {
		mac, err := config.ParseMAC(n.MAC)
		if err != nil {
			return nil, fmt.Errorf("node %s: %w", n.Name, err)
		}
		w.macs[n.Name] = mac
	}
	w.started = w.now()
	return w, nil
}

// Run ticks once a second until ctx is cancelled.
func (w *Waker) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		w.Tick()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// WakeNow sends one packet to a node, on request, whatever the state of the
// power or the list. It is what `nutwake wake` uses.
func (w *Waker) WakeNow(name string) error {
	mac, ok := w.macs[name]
	if !ok {
		return fmt.Errorf("no node called %q in the config", name)
	}
	if err := w.send(w.cfg.Wake.Broadcast, mac); err != nil {
		return err
	}
	w.events.Add(events.LevelInfo, events.KindWakeManual, name,
		fmt.Sprintf("Sent a wake packet to %s on request.", name), nil)
	return nil
}

// Tick does one step. It is cheap when nothing is waiting.
func (w *Waker) Tick() {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()

	p, err := w.store.LoadPending()
	if err != nil {
		if msg := err.Error(); msg != w.loadErr {
			slog.Warn("waker: cannot read the wake list", "err", err)
			w.loadErr = msg
		}
		return
	}
	w.loadErr = ""
	if p == nil || len(p.Nodes) == 0 {
		w.reset()
		w.status = Status{}
		return
	}
	if !w.announced {
		w.announced = true
		w.events.AddEvent(events.Event{
			Time: now, Kind: events.KindWakePending, Level: events.LevelInfo,
			Message: fmt.Sprintf("%s to be woken once power is steady: %s.", countNodes(len(p.Nodes)), joinNames(p.Nodes)),
			Data:    map[string]any{"nodes": p.Nodes},
		})
	}

	b := w.src.Brief()
	g := w.gate(b, now)
	w.status = Status{Active: true, Since: p.Since, Waiting: g.reason, Message: g.text}
	if !g.ok {
		w.interrupt()
		w.announceWait(g, b, now)
		w.fillNodes(p)
		return
	}
	w.waitingFor = ""
	if !w.begun {
		w.begun, w.nextFirst = true, now
	}

	changed := false
	for _, name := range slices.Clone(p.Nodes) {
		if w.step(name, g, b, now) {
			p.Remove(name)
			delete(w.tracks, name)
			changed = true
		}
	}
	if changed {
		if err := w.store.SavePending(p); err != nil {
			slog.Warn("waker: cannot update the wake list", "err", err)
		}
	}
	w.status = Status{Active: len(p.Nodes) > 0, Since: p.Since}
	w.fillNodes(p)
}

func (w *Waker) reset() {
	w.begun, w.waitingFor, w.announced = false, "", false
	clear(w.tracks)
}

// interrupt forgets the sending that was under way. The nodes stay on the
// list and start again when the gate opens.
func (w *Waker) interrupt() {
	w.begun = false
	clear(w.tracks)
}

// step moves one node forward and reports whether it is finished, so it can
// come off the list.
func (w *Waker) step(name string, g gate, b monitor.Brief, now time.Time) (done bool) {
	if _, ok := w.macs[name]; !ok {
		w.events.AddEvent(events.Event{
			Time: now, Kind: events.KindWakeFailed, Level: events.LevelWarn, Node: name,
			Message: fmt.Sprintf("%s is on the wake list but not in the config any more, so it was dropped.", name),
		})
		return true
	}
	t := w.tracks[name]
	if t == nil {
		t = &track{}
		w.tracks[name] = t
	}

	switch nodeState(b, name) {
	case monitor.NodeUp:
		if t.attempts == 0 {
			w.events.AddEvent(events.Event{
				Time: now, Kind: events.KindWakeSkipped, Level: events.LevelInfo, Node: name,
				Message: fmt.Sprintf("%s is already running, so it was not woken.", name),
			})
		} else {
			took := now.Sub(t.firstSent)
			w.events.AddEvent(events.Event{
				Time: now, Kind: events.KindWakeOK, Level: events.LevelOK, Node: name,
				Message: fmt.Sprintf("%s is back %s after the first wake packet.", name, humanize.Duration(took)),
				Data:    map[string]any{"after_s": int(took.Seconds()), "packets": t.attempts},
			})
		}
		return true
	case monitor.NodeUnknown:
		return false // the monitor has not decided yet
	}

	// The node is down.
	if t.attempts > 0 && now.Sub(t.firstSent) >= w.cfg.Wake.GiveUpAfter {
		msg := fmt.Sprintf("%s did not come back within %s of the first wake packet (tried %d times).",
			name, humanize.Duration(w.cfg.Wake.GiveUpAfter), t.attempts)
		if t.lastErr != "" {
			msg += " The last error was: " + t.lastErr
		}
		w.events.AddEvent(events.Event{
			Time: now, Kind: events.KindWakeFailed, Level: events.LevelCrit, Node: name,
			Message: msg, Data: map[string]any{"packets": t.attempts},
		})
		return true
	}
	due := false
	if t.attempts == 0 {
		due = !now.Before(w.nextFirst)
	} else {
		due = !now.Before(t.lastSent.Add(w.cfg.Wake.RetryInterval))
	}
	if !due {
		return false
	}

	t.attempts++
	t.lastSent = now
	if t.attempts == 1 {
		t.firstSent = now
		w.nextFirst = now.Add(w.cfg.Wake.Stagger)
	}
	if err := w.send(w.cfg.Wake.Broadcast, w.macs[name]); err != nil {
		t.lastErr = err.Error()
		slog.Warn("waker: cannot send the wake packet", "node", name, "err", err)
		return false
	}
	t.lastErr = ""

	msg := fmt.Sprintf("Sent a wake packet to %s.", name)
	if t.attempts > 1 {
		msg = fmt.Sprintf("%s has not come up yet. Sent wake packet %d.", name, t.attempts)
	} else if g.override != "" {
		msg = g.overrideText + " " + msg
	}
	data := map[string]any{"attempt": t.attempts}
	if g.override != "" {
		data["override"] = g.override
	}
	w.events.AddEvent(events.Event{
		Time: now, Kind: events.KindWakeSent, Level: events.LevelInfo, Node: name, Message: msg, Data: data,
	})
	return false
}

func nodeState(b monitor.Brief, name string) monitor.NodeState {
	for _, n := range b.Nodes {
		if n.Name == name {
			return n.State
		}
	}
	return monitor.NodeUnknown
}

// gate says whether it is time to wake nodes.
type gate struct {
	ok     bool
	reason string // why not, when !ok; one of the Wait constants
	text   string
	// override is set when the nodes are woken although the battery or the
	// UPS says otherwise, because waiting any longer would leave them off for
	// good.
	override     string
	overrideText string
}

func (w *Waker) gate(b monitor.Brief, now time.Time) gate {
	cfg := w.cfg.Wake
	u := b.UPS

	// Once sending has begun, only losing the mains stops it. The battery
	// level is not asked again: it only rises while power is on.
	if w.begun {
		if u.State == nut.StateOnBattery || u.State == nut.StateLowBattery {
			return gate{reason: WaitOnBattery, text: "Power was lost again. Waiting for it to return."}
		}
		return gate{ok: true}
	}

	switch u.State {
	case nut.StateOnBattery, nut.StateLowBattery:
		return gate{reason: WaitOnBattery, text: "Still on battery. The nodes will be woken once power is back."}
	case nut.StateUnreachable:
		if cfg.MaxWait > 0 && now.Sub(w.started) >= cfg.MaxWait {
			return gate{ok: true, override: WaitUPSUnreadable,
				overrideText: fmt.Sprintf("The UPS could not be read for %s, so the nodes are being woken anyway.", humanize.Duration(cfg.MaxWait))}
		}
		return gate{reason: WaitUPSUnreadable, text: "Cannot read the UPS, so the nodes are not being woken yet."}
	case nut.StateOnline:
	default:
		return gate{reason: WaitUPSState, text: fmt.Sprintf("The UPS is not reporting mains power (status %q).", u.Status)}
	}

	stable := now.Sub(u.Since)
	if stable < cfg.Delay {
		return gate{reason: WaitSettling,
			text: fmt.Sprintf("Power is back. Waiting %s to be sure it stays on.", humanize.Duration(cfg.Delay-stable))}
	}
	if u.Charge != nil && cfg.MinBattery > 0 && *u.Charge < cfg.MinBattery {
		if cfg.MaxWait > 0 && stable >= cfg.MaxWait {
			return gate{ok: true, override: "max_wait",
				overrideText: fmt.Sprintf("The battery is only at %s%%, but power has been steady for %s, so the nodes are being woken anyway.",
					humanize.Percent(*u.Charge), humanize.Duration(stable))}
		}
		return gate{reason: WaitBattery,
			text: fmt.Sprintf("Waiting for the battery to reach %s%% (now %s%%).", humanize.Percent(cfg.MinBattery), humanize.Percent(*u.Charge))}
	}
	return gate{ok: true}
}

// announceWait writes the reason for waiting to the history, once per reason.
// Waiting for power to settle is short and is not worth a line.
func (w *Waker) announceWait(g gate, b monitor.Brief, now time.Time) {
	if g.reason == WaitSettling || g.reason == w.waitingFor {
		return
	}
	// upsd often starts a little after the daemon does: not worth a line
	// unless the UPS stays unreadable.
	if g.reason == WaitUPSUnreadable && now.Sub(w.started) < time.Minute {
		return
	}
	w.waitingFor = g.reason
	data := map[string]any{"reason": g.reason}
	if b.UPS.Charge != nil {
		data["charge"] = *b.UPS.Charge
	}
	if g.reason == WaitBattery {
		data["needed"] = w.cfg.Wake.MinBattery
	}
	w.events.AddEvent(events.Event{
		Time: now, Kind: events.KindWakeWaiting, Level: events.LevelInfo, Message: g.text, Data: data,
	})
}

func countNodes(n int) string {
	if n == 1 {
		return "1 node is waiting"
	}
	return fmt.Sprintf("%d nodes are waiting", n)
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
