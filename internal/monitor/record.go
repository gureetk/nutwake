package monitor

import (
	"slices"
	"time"

	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/samples"
)

// recorder remembers what was last written, to write again only on a change.
type recorder struct {
	last samples.Sample
	at   time.Time
	have bool
}

// record writes a sample when something changed since the last one, every poll
// while off mains (for the discharge curve), and otherwise once per
// heartbeatEvery. It does nothing until the UPS has been read once.
func (m *Monitor) record(now time.Time) {
	if m.smp == nil || (m.power.reading == nil && m.power.state != nut.StateUnreachable) {
		return
	}
	sm := samples.Sample{T: now, UPS: m.power.state}
	if m.power.reachable && m.power.reading != nil {
		if c, ok := m.power.reading.Charge(); ok {
			sm.Charge = &c
		}
	}
	for _, n := range m.nodes {
		switch n.state {
		case NodeUp:
			sm.Up = append(sm.Up, n.cfg.Name)
		case NodeDown:
			sm.Down = append(sm.Down, n.cfg.Name)
		}
	}

	r := &m.rec
	onBattery := sm.UPS == nut.StateOnBattery || sm.UPS == nut.StateLowBattery
	if r.have && !onBattery && now.Sub(r.at) < heartbeatEvery && sameSample(r.last, sm) {
		return
	}
	m.smp.Add(sm)
	r.last, r.at, r.have = sm, now, true
}

func sameSample(a, b samples.Sample) bool {
	if a.UPS != b.UPS || !slices.Equal(a.Up, b.Up) || !slices.Equal(a.Down, b.Down) {
		return false
	}
	if (a.Charge == nil) != (b.Charge == nil) {
		return false
	}
	return a.Charge == nil || *a.Charge == *b.Charge
}
