package monitor

import (
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/humanize"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/state"
)

type upsState struct {
	reading   *nut.Reading // last good reading
	failures  int          // consecutive failed reads
	reachable bool
	lastErr   string

	state nut.State // classification of the last good reading
	since time.Time // when state last changed
	rb    bool      // UPS has asked for a battery replacement
}

// outage is a power cut in progress.
type outage struct {
	state.Outage

	startedAt time.Time // Started as first captured, with a monotonic reading
	persisted bool      // saved to disk at least once
	fromDisk  bool      // loaded at start-up, so we may have missed its end
	lastSave  time.Time
	savedMin  float64
}

// closing is a power cut whose end has been seen but not yet recorded,
// because the system clock is not set and the duration would be wrong.
type closing struct{ at time.Time }

func (m *Monitor) applyUPS(r *nut.Reading, err error, now time.Time) {
	u := &m.power
	if err != nil {
		u.failures++
		u.lastErr = err.Error()
		if u.failures == upsDownAfter {
			u.reachable = false
			u.state = nut.StateUnreachable
			m.emit(events.LevelWarn, events.KindUPSUnreachable, "",
				fmt.Sprintf("Lost contact with the UPS service at %s.", m.cfg.NUT.Addr()),
				map[string]any{"error": u.lastErr}, now)
		}
		return
	}

	wasDown := u.failures >= upsDownAfter
	first := u.reading == nil
	u.failures, u.lastErr, u.reading, u.reachable = 0, "", r, true
	if wasDown && !first {
		m.emit(events.LevelOK, events.KindUPSReconnected, "", "Contact with the UPS is back.", nil, now)
	}

	st := r.State()
	if st != u.state || u.since.IsZero() {
		u.state, u.since = st, now
	}
	m.applyBatteryFlag(r, first, now)

	charge, haveCharge := r.Charge()
	switch st {
	case nut.StateOnBattery, nut.StateLowBattery:
		m.onBattery(r, st, charge, haveCharge, now)
	case nut.StateOnline:
		m.onMains(now)
	}
	m.sample(st, charge, haveCharge, now)
}

// applyBatteryFlag announces the UPS asking for a new battery. A flag already
// set when the daemon starts is announced only if the history has no such
// event yet, so every reboot of the Pi does not repeat it.
func (m *Monitor) applyBatteryFlag(r *nut.Reading, first bool, now time.Time) {
	rb := r.Has("RB")
	if rb && !m.power.rb {
		if _, err := m.events.Latest(events.KindReplaceBattery); first && err == nil {
			m.power.rb = rb
			return
		}
		m.emit(events.LevelWarn, events.KindReplaceBattery, "",
			"The UPS says its battery needs replacing.", nil, now)
	}
	m.power.rb = rb
}

func (m *Monitor) onBattery(r *nut.Reading, st nut.State, charge float64, haveCharge bool, now time.Time) {
	if m.closing != nil {
		// Power came back and went again before the end could be recorded:
		// treat it as one outage.
		m.closing = nil
	}
	o := m.outage
	if o == nil {
		o = &outage{startedAt: now}
		o.Started, o.LastSeen = now, now
		if haveCharge {
			o.StartCharge, o.MinCharge = charge, charge
		}
		o.NodesUp = m.upNodes()
		m.outage = o
		msg := "Power lost. Running on battery."
		if haveCharge {
			msg = fmt.Sprintf("Power lost. Running on battery at %s%%.", humanize.Percent(charge))
		}
		data := map[string]any{"nodes_up": o.NodesUp}
		if haveCharge {
			data["charge"] = charge
		}
		m.emit(events.LevelWarn, events.KindPowerLost, "", msg, data, now)
	}
	o.LastSeen = now
	if haveCharge && charge < o.MinCharge {
		o.MinCharge = charge
	}
	if st == nut.StateLowBattery && !o.LowBattery {
		o.LowBattery = true
		msg := "The UPS reports a low battery."
		if haveCharge {
			msg = fmt.Sprintf("The UPS reports a low battery (%s%%).", humanize.Percent(charge))
		}
		data := map[string]any{}
		if haveCharge {
			data["charge"] = charge
		}
		m.emit(events.LevelCrit, events.KindLowBattery, "", msg, data, now)
		m.saveOutage(now, true)
		return
	}
	m.saveOutage(now, false)
}

func (m *Monitor) onMains(now time.Time) {
	if m.outage != nil && m.closing == nil {
		m.closing = &closing{at: now}
	}
}

// settle does the work that waits for a trustworthy clock: saving a power cut
// that started before it was set, and recording the end of one.
func (m *Monitor) settle(now time.Time) {
	if m.outage != nil && !m.outage.persisted {
		m.saveOutage(now, true)
	}
	if m.closing == nil || !m.clock.Ready() {
		return
	}
	m.closeOutage()
}

func (m *Monitor) closeOutage() {
	o, c := m.outage, m.closing
	end := m.fix(c.at)
	start := o.Started
	if !o.persisted && !o.startedAt.IsZero() {
		start = m.fix(o.startedAt)
	}

	dur := end.Sub(start)
	approx := o.fromDisk
	if dur < 0 {
		dur, approx = 0, true // the clock is still wrong
	}
	data := map[string]any{
		"started":      start.Round(0).UTC().Format(time.RFC3339),
		"duration_s":   int(dur.Seconds()),
		"start_charge": o.StartCharge,
		"min_charge":   o.MinCharge,
		"nodes_up":     o.NodesUp,
	}
	msg := fmt.Sprintf("Power is back after %s.", humanize.Duration(dur))
	if approx {
		data["approximate"] = true
		msg = fmt.Sprintf("Power is back. The outage lasted about %s (this Pi was off for part of it).", humanize.Duration(dur))
	}
	if o.MinCharge > 0 || o.StartCharge > 0 {
		msg += fmt.Sprintf(" The battery fell to %s%%.", humanize.Percent(o.MinCharge))
	}
	m.emit(events.LevelOK, events.KindPowerRestored, "", msg, data, c.at)

	if err := m.store.ClearOutage(); err != nil {
		slog.Warn("monitor: cannot remove the outage record", "err", err)
	}
	m.outage, m.closing = nil, nil
}

// saveOutage writes the power cut to disk, but not on every poll.
func (m *Monitor) saveOutage(now time.Time, force bool) {
	o := m.outage
	if o == nil || !m.clock.Ready() {
		return
	}
	if !force && o.persisted && now.Sub(o.lastSave) < saveEvery && math.Abs(o.MinCharge-o.savedMin) < 1 {
		return
	}
	if !o.persisted && !o.startedAt.IsZero() {
		o.Started = m.fix(o.startedAt)
	}
	o.LastSeen = m.fix(now)
	if err := m.store.SaveOutage(&o.Outage); err != nil {
		slog.Warn("monitor: cannot save the outage record", "err", err)
		return
	}
	o.persisted, o.lastSave, o.savedMin = true, now, o.MinCharge
}
