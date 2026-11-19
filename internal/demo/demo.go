// Package demo builds a made-up UPS history so the status page can be tried,
// photographed and tested without a UPS or any power cuts.
//
// It tells the story of one power cut: the lights go out, the battery runs
// down, the nodes shut down, the Pi halts and later boots again, and the nodes
// are woken. Each scene is a different moment of that story.
package demo

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/monitor"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/samples"
	"github.com/gureetk/nutwake/internal/waker"
)

// Scene is a moment in the story.
type Scene string

const (
	// Online is the day after: power is on and the last outage is history.
	Online Scene = "online"
	// Battery is 17 minutes into a power cut.
	Battery Scene = "battery"
	// Waiting is just after the Pi has booted, with the battery too low to
	// wake the nodes yet.
	Waiting Scene = "waiting"
)

// Scenes lists the scenes by name.
var Scenes = []Scene{Online, Battery, Waiting}

// Demo holds the made-up history and the live state of a scene.
type Demo struct {
	Events  *events.Store
	Samples *samples.Store
	Now     time.Time

	snap monitor.Snapshot
	wake waker.Status
}

// Snapshot implements the monitor source the status page reads.
func (d *Demo) Snapshot() monitor.Snapshot { return d.snap }

// Status implements the waker source the status page reads.
func (d *Demo) Status() waker.Status { return d.wake }

// the power cut, in minutes from its start
const (
	tLow     = 27   // battery reaches 10%
	tNodeOff = 29.5 // both nodes are off
	tHalt    = 30   // the Pi halts
	tBoot    = 72   // power is back and the Pi boots
	tWake    = 83   // wake packets go out, once the battery has recharged
	tKaios   = 86.2 // kaios answers
	tNodeB   = 87.3 // node-b answers
	recharge = 0.27 // percent per minute while on mains
)

// curve is the battery during the outage: minutes in, percent.
var curve = [][2]float64{{0, 100}, {6, 86}, {12, 68}, {17.5, 43}, {22, 28}, {27, 10}, {30, 9}}

func discharge(m float64) float64 {
	for i := 1; i < len(curve); i++ {
		if m <= curve[i][0] {
			a, b := curve[i-1], curve[i]
			return a[1] + (b[1]-a[1])*(m-a[0])/(b[0]-a[0])
		}
	}
	return curve[len(curve)-1][1]
}

func charge(m float64) float64 {
	return min(100, 12+(m-tBoot)*recharge)
}

// Build writes the history for scene into dir and returns it. The story is laid
// out so that now falls in the right place for the scene.
func Build(scene Scene, now time.Time, dir string) (*Demo, error) {
	var at float64 // minutes after the power cut at which "now" falls
	switch scene {
	case Online:
		at = 15*60 + 40
	case Battery:
		at = 17.5
	case Waiting:
		at = tBoot + 4
	default:
		return nil, fmt.Errorf("unknown scene %q", scene)
	}
	start := now.Add(-time.Duration(at * float64(time.Minute)))
	tm := func(m float64) time.Time { return start.Add(time.Duration(m * float64(time.Minute))) }

	ev, err := events.Open(filepath.Join(dir, "events.jsonl"), nil)
	if err != nil {
		return nil, err
	}
	sm, err := samples.Open(filepath.Join(dir, "samples.jsonl"), nil)
	if err != nil {
		return nil, err
	}
	d := &Demo{Events: ev, Samples: sm, Now: now}

	// older, shorter power cuts
	old := func(daysAgo float64, secs int, cmin float64, note string) {
		t := now.Add(-time.Duration(daysAgo * 24 * float64(time.Hour)))
		ev.AddEvent(events.Event{Time: t, Kind: events.KindPowerLost, Level: events.LevelWarn,
			Message: "Power lost. Running on battery at 100%.", Data: map[string]any{"charge": 100.0}})
		ev.AddEvent(events.Event{Time: t.Add(time.Duration(secs) * time.Second), Kind: events.KindPowerRestored, Level: events.LevelOK,
			Message: note, Data: map[string]any{"duration_s": float64(secs), "start_charge": 100.0, "min_charge": cmin}})
	}
	old(36, 185, 93, "Power is back after 3 min. The battery fell to 93%.")
	old(21, 9, 100, "Power is back after 9 s. The battery fell to 100%.")

	bootAt := start.Add(-6 * time.Hour)
	if scene != Online {
		bootAt = start.Add(-5 * 24 * time.Hour)
	}
	ev.AddEvent(events.Event{Time: bootAt, Kind: events.KindStart, Level: events.LevelInfo, Message: "nutwake started."})

	// samples before the power cut: a heartbeat every five minutes
	both := []string{"kaios", "node-b"}
	f := func(v float64) *float64 { return &v }
	for m := -3 * 60.0; m < 0; m += 5 {
		sm.Add(samples.Sample{T: tm(m), UPS: nut.StateOnline, Charge: f(100), Up: both})
	}

	// the power cut
	if at >= 0 {
		ev.AddEvent(events.Event{Time: tm(0), Kind: events.KindPowerLost, Level: events.LevelWarn,
			Message: "Power lost. Running on battery at 100%.", Data: map[string]any{"charge": 100.0, "nodes_up": both}})
	}
	lastOnBattery := min(at, tHalt)
	for m := 0.0; m <= lastOnBattery; m += 5.0 / 60 {
		s := samples.Sample{T: tm(m), UPS: nut.StateOnBattery, Charge: f(discharge(m)), Up: both}
		switch {
		case m >= tLow:
			s.UPS = nut.StateLowBattery
		}
		if m >= tNodeOff-1 {
			s.Up, s.Down = nil, both
		}
		sm.Add(s)
	}
	if at > tLow {
		ev.AddEvent(events.Event{Time: tm(tLow), Kind: events.KindLowBattery, Level: events.LevelCrit,
			Message: "The UPS reports a low battery (10%).", Data: map[string]any{"charge": 10.0}})
		ev.AddEvent(events.Event{Time: tm(tLow + 0.05), Kind: events.KindShutdownBegan, Level: events.LevelWarn,
			Message: "Shutting down this Pi. Waiting up to 5 min for kaios, node-b to power off.",
			Data:    map[string]any{"nodes_up": both, "to_wake": both, "outage_record": true}})
		ev.AddEvent(events.Event{Time: tm(tNodeOff - 1), Kind: events.KindNodeDown, Level: events.LevelWarn, Node: "kaios",
			Message: "kaios stopped responding.", Data: map[string]any{"during_outage": true}})
		ev.AddEvent(events.Event{Time: tm(tNodeOff - 0.9), Kind: events.KindNodeDown, Level: events.LevelWarn, Node: "node-b",
			Message: "node-b stopped responding.", Data: map[string]any{"during_outage": true}})
		ev.AddEvent(events.Event{Time: tm(tNodeOff), Kind: events.KindNodesOff, Level: events.LevelOK,
			Message: "kaios, node-b powered off after 1 min 38 s.", Data: map[string]any{"nodes": both, "waited_s": 98.0}})
		ev.AddEvent(events.Event{Time: tm(tHalt), Kind: events.KindPiHalting, Level: events.LevelWarn, Message: "Halting this Pi now."})
		ev.AddEvent(events.Event{Time: tm(tHalt + 0.2), Kind: events.KindStop, Level: events.LevelInfo, Message: "nutwake stopped."})
	}

	// the Pi is off from tHalt to tBoot, then everything resumes
	if at >= tBoot {
		ev.AddEvent(events.Event{Time: tm(tBoot), Kind: events.KindStart, Level: events.LevelInfo, Message: "nutwake started."})
		ev.AddEvent(events.Event{Time: tm(tBoot + 0.1), Kind: events.KindPowerRestored, Level: events.LevelOK,
			Message: "Power is back. The outage lasted about 1 h 12 min (this Pi was off for part of it). The battery fell to 9%.",
			Data:    map[string]any{"duration_s": 72.0 * 60, "approximate": true, "start_charge": 100.0, "min_charge": 9.0, "nodes_up": both}})
		ev.AddEvent(events.Event{Time: tm(tBoot + 0.2), Kind: events.KindWakePending, Level: events.LevelInfo,
			Message: "2 nodes are waiting to be woken once power is steady: kaios, node-b.", Data: map[string]any{"nodes": both}})
	}
	until := min(at, tWake)
	for m := float64(tBoot); m <= until; m += 5 {
		sm.Add(samples.Sample{T: tm(m), UPS: nut.StateOnline, Charge: f(charge(m)), Down: both})
	}
	if at > tBoot+1 && at < tWake {
		ev.AddEvent(events.Event{Time: tm(tBoot + 1.2), Kind: events.KindWakeWaiting, Level: events.LevelInfo,
			Message: "Waiting for the battery to reach 15% (now 12%).", Data: map[string]any{"reason": "battery", "charge": 12.0, "needed": 15.0}})
	}
	if at >= tWake {
		ev.AddEvent(events.Event{Time: tm(tWake), Kind: events.KindWakeSent, Level: events.LevelInfo, Node: "kaios",
			Message: "Sent a wake packet to kaios.", Data: map[string]any{"attempt": 1.0}})
		ev.AddEvent(events.Event{Time: tm(tWake + 0.1), Kind: events.KindWakeSent, Level: events.LevelInfo, Node: "node-b",
			Message: "Sent a wake packet to node-b.", Data: map[string]any{"attempt": 1.0}})
		ev.AddEvent(events.Event{Time: tm(tKaios), Kind: events.KindNodeUp, Level: events.LevelOK, Node: "kaios",
			Message: "kaios is up again after 1 h 25 min.", Data: map[string]any{"down_for_s": 5100.0}})
		ev.AddEvent(events.Event{Time: tm(tKaios), Kind: events.KindWakeOK, Level: events.LevelOK, Node: "kaios",
			Message: "kaios is back 3 min 12 s after the first wake packet.", Data: map[string]any{"after_s": 192.0, "packets": 1.0}})
		ev.AddEvent(events.Event{Time: tm(tNodeB), Kind: events.KindNodeUp, Level: events.LevelOK, Node: "node-b",
			Message: "node-b is up again after 1 h 26 min.", Data: map[string]any{"down_for_s": 5160.0}})
		ev.AddEvent(events.Event{Time: tm(tNodeB), Kind: events.KindWakeOK, Level: events.LevelOK, Node: "node-b",
			Message: "node-b is back 4 min 14 s after the first wake packet.", Data: map[string]any{"after_s": 254.0, "packets": 1.0}})
		for m := float64(tWake); m <= at; m += 5 {
			s := samples.Sample{T: tm(m), UPS: nut.StateOnline, Charge: f(charge(m)), Down: both}
			if m >= tKaios {
				s.Up, s.Down = []string{"kaios"}, []string{"node-b"}
			}
			if m >= tNodeB {
				s.Up, s.Down = both, nil
			}
			sm.Add(s)
		}
	}
	ev.FlushNow()
	sm.FlushNow()

	d.snap = d.live(scene, at, start, bootAt, tm)
	return d, nil
}

func (d *Demo) live(scene Scene, at float64, start, bootAt time.Time, tm func(float64) time.Time) monitor.Snapshot {
	f := func(v float64) *float64 { return &v }
	up := func(name, host string, st monitor.NodeState, since time.Time) monitor.NodeStatus {
		return monitor.NodeStatus{Name: name, Host: host, Probe: "icmp", State: st, Since: since}
	}
	u := monitor.UPSStatus{
		Reachable: true, Manufacturer: "CyberPower", Model: "ST425", ChargeLow: f(10),
		Load: f(14), InputVoltage: f(121),
	}
	s := monitor.Snapshot{At: d.Now, Started: bootAt, ClockSet: true}
	switch scene {
	case Battery:
		c := discharge(at)
		u.State, u.Status, u.Since = nut.StateOnBattery, "OB DISCHRG", start
		u.Charge, u.RuntimeSeconds, u.InputVoltage = f(c), f(11*60), f(0)
		s.UPS = u
		s.Nodes = []monitor.NodeStatus{up("kaios", "10.10.0.10", monitor.NodeUp, bootAt), up("node-b", "10.10.0.11", monitor.NodeUp, bootAt)}
		s.Outage = &monitor.OutageStatus{Started: start, StartCharge: 100, MinCharge: c, NodesUp: []string{"kaios", "node-b"}}
	case Waiting:
		c := charge(at)
		u.State, u.Status, u.Since = nut.StateOnline, "OL CHRG", tm(tBoot)
		u.Charge, u.RuntimeSeconds = f(c), f(4*60)
		s.UPS = u
		s.Started = tm(tBoot)
		s.Nodes = []monitor.NodeStatus{up("kaios", "10.10.0.10", monitor.NodeDown, tm(tBoot)), up("node-b", "10.10.0.11", monitor.NodeDown, tm(tBoot))}
		d.wake = waker.Status{
			Active: true, Since: tm(tLow), Waiting: waker.WaitBattery, MinBattery: 15,
			Message: fmt.Sprintf("Waiting for the battery to reach 15%% (now %.0f%%).", c),
			Nodes:   []waker.NodeProgress{{Name: "kaios", State: "waiting"}, {Name: "node-b", State: "waiting"}},
		}
	default:
		u.State, u.Status, u.Since = nut.StateOnline, "OL", tm(tBoot)
		u.Charge, u.RuntimeSeconds = f(100), f(49*60)
		s.UPS = u
		s.Nodes = []monitor.NodeStatus{up("kaios", "10.10.0.10", monitor.NodeUp, tm(tKaios)), up("node-b", "10.10.0.11", monitor.NodeUp, tm(tNodeB))}
		d.wake = waker.Status{MinBattery: 15}
	}
	return s
}
