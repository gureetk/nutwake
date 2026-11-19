package monitor

import (
	"time"

	"github.com/gureetk/nutwake/internal/nut"
)

// Sample is one point of the battery history.
type Sample struct {
	T      time.Time `json:"t"`
	Charge *float64  `json:"charge,omitempty"`
	State  nut.State `json:"state"`
}

// Snapshot is the monitor's view of the world at one moment. It is what the
// status page and the waker read.
type Snapshot struct {
	At      time.Time `json:"at"`
	Started time.Time `json:"started"`
	// ClockSet is false while the system clock has not been set yet, when
	// times may be wrong.
	ClockSet bool          `json:"clock_set"`
	UPS      UPSStatus     `json:"ups"`
	Nodes    []NodeStatus  `json:"nodes"`
	Outage   *OutageStatus `json:"outage,omitempty"`
	Samples  []Sample      `json:"samples"`
}

// UPSStatus is the UPS as last read.
type UPSStatus struct {
	Reachable bool      `json:"reachable"`
	State     nut.State `json:"state"`
	// Status is the raw ups.status, such as "OL CHRG".
	Status string    `json:"status,omitempty"`
	Since  time.Time `json:"since,omitempty"`
	Error  string    `json:"error,omitempty"`

	Model          string   `json:"model,omitempty"`
	Charge         *float64 `json:"charge,omitempty"`
	ChargeLow      *float64 `json:"charge_low,omitempty"`
	RuntimeSeconds *float64 `json:"runtime_s,omitempty"`
	Load           *float64 `json:"load,omitempty"`
	InputVoltage   *float64 `json:"input_voltage,omitempty"`
	ReplaceBattery bool     `json:"replace_battery,omitempty"`
}

// NodeStatus is one node as last probed.
type NodeStatus struct {
	Name  string    `json:"name"`
	Host  string    `json:"host"`
	Probe string    `json:"probe"`
	State NodeState `json:"state"`
	Since time.Time `json:"since,omitempty"`
}

// OutageStatus is the power cut in progress.
type OutageStatus struct {
	Started     time.Time `json:"started"`
	StartCharge float64   `json:"start_charge"`
	MinCharge   float64   `json:"min_charge"`
	NodesUp     []string  `json:"nodes_up"`
	LowBattery  bool      `json:"low_battery,omitempty"`
}

// Snapshot returns a copy of the current state, safe to keep and to encode.
func (m *Monitor) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	s := Snapshot{
		At:       m.fix(m.now()),
		Started:  m.fix(m.started),
		ClockSet: m.clock.Trusted(),
		Nodes:    make([]NodeStatus, 0, len(m.nodes)),
		Samples:  make([]Sample, len(m.samples)),
	}
	u := m.power
	s.UPS = UPSStatus{
		Reachable:      u.reachable,
		State:          u.state,
		Since:          m.fix(u.since),
		Error:          u.lastErr,
		ReplaceBattery: u.rb,
	}
	if r := u.reading; r != nil {
		s.UPS.Status = r.Vars["ups.status"]
		s.UPS.Model = r.First("device.model", "ups.model")
		s.UPS.Charge = num(r, "battery.charge")
		s.UPS.ChargeLow = num(r, "battery.charge.low")
		s.UPS.RuntimeSeconds = num(r, "battery.runtime")
		s.UPS.Load = num(r, "ups.load")
		s.UPS.InputVoltage = num(r, "input.voltage")
	}
	for _, n := range m.nodes {
		st := n.state
		if st == "" {
			st = NodeUnknown
		}
		s.Nodes = append(s.Nodes, NodeStatus{
			Name: n.cfg.Name, Host: n.cfg.Host, Probe: n.probe, State: st, Since: m.fix(n.since),
		})
	}
	// A power cut whose end is waiting for the clock has ended as far as the
	// reader is concerned.
	if o := m.outage; o != nil && m.closing == nil {
		start := o.Started
		if !o.persisted && !o.startedAt.IsZero() {
			start = o.startedAt
		}
		s.Outage = &OutageStatus{
			Started: m.fix(start), StartCharge: o.StartCharge, MinCharge: o.MinCharge,
			NodesUp: append([]string(nil), o.NodesUp...), LowBattery: o.LowBattery,
		}
	}
	for i, p := range m.samples {
		p.T = m.fix(p.T)
		s.Samples[i] = p
	}
	return s
}

func num(r *nut.Reading, name string) *float64 {
	if v, ok := r.Num(name); ok {
		return &v
	}
	return nil
}

// sample records a battery point: every poll while off mains or when the
// reading changed, otherwise every sampleEvery.
func (m *Monitor) sample(st nut.State, charge float64, haveCharge bool, now time.Time) {
	if n := len(m.samples); n > 0 {
		last := m.samples[n-1]
		changed := last.State != st
		if haveCharge && last.Charge != nil && *last.Charge != charge {
			changed = true
		}
		onBattery := st == nut.StateOnBattery || st == nut.StateLowBattery
		if !changed && !onBattery && now.Sub(last.T) < sampleEvery {
			return
		}
	}
	p := Sample{T: now, State: st}
	if haveCharge {
		p.Charge = &charge
	}
	m.samples = append(m.samples, p)
	if len(m.samples) > maxSamples {
		m.samples = append([]Sample(nil), m.samples[len(m.samples)-maxSamples:]...)
	}
}
