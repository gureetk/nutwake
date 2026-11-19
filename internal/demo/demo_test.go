package demo

import (
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/monitor"
	"github.com/gureetk/nutwake/internal/nut"
)

var now = time.Date(2026, 11, 14, 11, 32, 7, 0, time.UTC)

func TestUnknownScene(t *testing.T) {
	if _, err := Build("midnight", now, t.TempDir()); err == nil {
		t.Fatal("Build accepted an unknown scene")
	}
}

func TestScenesAreConsistent(t *testing.T) {
	for _, sc := range Scenes {
		t.Run(string(sc), func(t *testing.T) {
			d, err := Build(sc, now, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			evs, err := d.Events.All()
			if err != nil || len(evs) == 0 {
				t.Fatalf("events = %d, %v", len(evs), err)
			}
			for i := 1; i < len(evs); i++ {
				if evs[i].Time.Before(evs[i-1].Time) {
					t.Fatalf("events are not in time order at %d", i)
				}
			}
			for _, e := range evs {
				if e.Time.After(now) {
					t.Fatalf("event %s is in the future: %v", e.Kind, e.Time)
				}
			}
			pts, err := d.Samples.Range(now.Add(-30*24*time.Hour), now)
			if err != nil || len(pts) == 0 {
				t.Fatalf("samples = %d, %v", len(pts), err)
			}
			for _, p := range pts {
				if p.T.After(now) {
					t.Fatalf("sample is in the future: %v", p.T)
				}
				if p.Charge != nil && (*p.Charge < 0 || *p.Charge > 100) {
					t.Fatalf("charge %v out of range", *p.Charge)
				}
			}
			snap := d.Snapshot()
			if !snap.UPS.Reachable || len(snap.Nodes) != 2 {
				t.Fatalf("snapshot = %+v", snap)
			}
		})
	}
}

func TestSceneStates(t *testing.T) {
	cases := []struct {
		scene   Scene
		ups     nut.State
		outage  bool
		waiting bool
		nodes   monitor.NodeState
		kind    string // an event kind the scene must have
	}{
		{Online, nut.StateOnline, false, false, monitor.NodeUp, events.KindWakeOK},
		{Battery, nut.StateOnBattery, true, false, monitor.NodeUp, events.KindPowerLost},
		{Waiting, nut.StateOnline, false, true, monitor.NodeDown, events.KindWakeWaiting},
	}
	for _, c := range cases {
		t.Run(string(c.scene), func(t *testing.T) {
			d, err := Build(c.scene, now, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			snap := d.Snapshot()
			if snap.UPS.State != c.ups {
				t.Errorf("ups = %v, want %v", snap.UPS.State, c.ups)
			}
			if (snap.Outage != nil) != c.outage {
				t.Errorf("outage = %v, want %v", snap.Outage != nil, c.outage)
			}
			if d.Status().Active != c.waiting {
				t.Errorf("wake active = %v, want %v", d.Status().Active, c.waiting)
			}
			for _, n := range snap.Nodes {
				if n.State != c.nodes {
					t.Errorf("node %s = %v, want %v", n.Name, n.State, c.nodes)
				}
			}
			found := false
			evs, _ := d.Events.All()
			for _, e := range evs {
				if e.Kind == c.kind {
					found = true
				}
			}
			if !found {
				t.Errorf("no %s event", c.kind)
			}
		})
	}
}

func TestDischargeCurve(t *testing.T) {
	if got := discharge(0); got != 100 {
		t.Errorf("discharge(0) = %v", got)
	}
	if got := discharge(tLow); got != 10 {
		t.Errorf("discharge at the low point = %v, want 10", got)
	}
	prev := 101.0
	for m := 0.0; m <= tHalt+5; m += 0.5 {
		c := discharge(m)
		if c > prev {
			t.Fatalf("the battery went up while on battery at minute %v", m)
		}
		prev = c
	}
	if charge(tBoot+1000) != 100 {
		t.Error("the battery does not stop at 100%")
	}
}
