package web

import (
	"time"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/monitor"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/samples"
	"github.com/gureetk/nutwake/internal/waker"
)

// Status is everything the page shows, in one response.
type Status struct {
	Now        time.Time             `json:"now"`
	Version    string                `json:"version"`
	RefreshS   int                   `json:"refresh_s"`
	UPSName    string                `json:"ups_name"`
	ConfigPath string                `json:"config_path"`
	PiUptimeS  int64                 `json:"pi_uptime_s"`
	ClockSet   bool                  `json:"clock_set"`
	Headline   Headline              `json:"headline"`
	UPS        monitor.UPSStatus     `json:"ups"`
	Nodes      []monitor.NodeStatus  `json:"nodes"`
	Outage     *monitor.OutageStatus `json:"outage,omitempty"`
	Wake       waker.Status          `json:"wake"`
	Timeline   Timeline              `json:"timeline"`
	Outages    []OutageRow           `json:"outages"`
	Events     []events.Event        `json:"events"`
	Settings   Settings              `json:"settings"`
}

const (
	maxEvents  = 60
	maxOutages = 20
	// defaultShutdownCharge is where the UPS asks for a shutdown when it does
	// not say.
	defaultShutdownCharge = 10
)

// Status builds the response for one range ("auto", "outage", "24h", "7d").
func (s *Server) Status(rng string) Status {
	now := s.now()
	snap := s.mon.Snapshot()
	var wk waker.Status
	if s.wake != nil {
		wk = s.wake.Status()
	}
	evs, err := s.events.All()
	if err != nil {
		evs = nil
	}

	low := float64(defaultShutdownCharge)
	if snap.UPS.ChargeLow != nil && *snap.UPS.ChargeLow > 0 {
		low = *snap.UPS.ChargeLow
	}
	recent, _ := s.samples.Range(now.Add(-15*time.Minute), now)
	expected := expectedShutdown(snap.UPS, recent, low, now)

	rows := buildOutages(outageInput{
		Events: evs, Active: snap.Outage, ExpectedShutdown: expected, ShutdownCharge: low,
		WakeActive: wk.Active, Now: now,
	})
	if len(rows) > maxOutages {
		rows = rows[:maxOutages]
	}

	used, from, to := chooseWindow(rng, rows, evs, now, expected)
	smp, _ := s.samples.Range(from, to)
	var prior *samples.Sample
	if p, ok := s.samples.Before(from); ok {
		prior = &p
	}
	live := liveSample(snap, now)

	var proj *Projection
	if expected != nil && snap.UPS.Charge != nil {
		proj = &Projection{From: Point{now, *snap.UPS.Charge}, To: Point{*expected, low}}
	}
	tl := buildTimeline(timelineIn{
		Range: used, From: from, To: to, Now: now,
		Samples: smp, Prior: prior, Live: &live, Events: evs, Nodes: nodeInfos(s.cfg),
		Rows: rows, WakeAt: s.cfg.Wake.MinBattery, ShutdownAt: low, Projection: proj, ExpectedShutdown: expected,
	})
	tl.Ranges = []string{Range24h, Range7d}
	if len(rows) > 0 {
		tl.Ranges = []string{RangeOutage, Range24h, Range7d}
	}
	titles(&tl, rows, now, proj != nil)

	newest, _ := s.events.Recent(maxEvents, nil)
	if newest == nil {
		newest = []events.Event{}
	}
	return Status{
		Now: now, Version: s.version, RefreshS: int(s.cfg.PollInterval.Seconds()),
		UPSName: s.cfg.NUT.UPS, ConfigPath: s.configPath, PiUptimeS: uptimeSeconds(s.started),
		ClockSet: snap.ClockSet,
		Headline: buildHeadline(headlineIn{
			Snap: snap, Wake: wk, Rows: rows, ExpectedShutdown: expected, ShutdownCharge: low,
			NUTAddr: s.cfg.NUT.Addr(), Now: now,
		}),
		UPS: snap.UPS, Nodes: snap.Nodes, Outage: snap.Outage, Wake: wk,
		Timeline: tl, Outages: rows, Events: newest, Settings: settingsOf(s.cfg),
	}
}

func nodeInfos(cfg *config.Config) []nodeInfo {
	out := make([]nodeInfo, len(cfg.Nodes))
	for i, n := range cfg.Nodes {
		out[i] = nodeInfo{n.Name, n.Host}
	}
	return out
}

// liveSample is the present moment, as a sample, so the lanes reach it.
func liveSample(snap monitor.Snapshot, now time.Time) samples.Sample {
	sm := samples.Sample{T: now, UPS: snap.UPS.State, Charge: snap.UPS.Charge}
	if !snap.UPS.Reachable {
		sm.UPS, sm.Charge = nut.StateUnreachable, nil
	}
	for _, n := range snap.Nodes {
		switch n.State {
		case monitor.NodeUp:
			sm.Up = append(sm.Up, n.Name)
		case monitor.NodeDown:
			sm.Down = append(sm.Down, n.Name)
		}
	}
	return sm
}
