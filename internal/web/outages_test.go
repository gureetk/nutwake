package web

import (
	"strings"
	"testing"

	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/monitor"
)

func ev(min float64, kind string, node string, data map[string]any) events.Event {
	return events.Event{Time: at(min), Kind: kind, Node: node, Data: data}
}

func TestOutageRodeThrough(t *testing.T) {
	rows := buildOutages(outageInput{
		Now: at(60),
		Events: []events.Event{
			ev(0, events.KindPowerLost, "", map[string]any{"charge": 100.0}),
			ev(3, events.KindPowerRestored, "", map[string]any{"duration_s": 180.0, "min_charge": 93.0, "start_charge": 100.0}),
		},
	})
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	r := rows[0]
	if r.Tone != ToneOK || !strings.Contains(r.Result, "Rode through") {
		t.Fatalf("row = %+v", r)
	}
	if r.DurationS != 180 || r.MinCharge == nil || *r.MinCharge != 93 {
		t.Fatalf("duration/min = %d/%v", r.DurationS, r.MinCharge)
	}
}

func TestOutageNodesShutDownAndWoke(t *testing.T) {
	rows := buildOutages(outageInput{
		Now: at(200),
		Events: []events.Event{
			ev(0, events.KindPowerLost, "", map[string]any{"charge": 100.0}),
			ev(25, events.KindShutdownBegan, "", nil),
			ev(70, events.KindPowerRestored, "", map[string]any{"duration_s": 4200.0, "min_charge": 9.0}),
			ev(80, events.KindWakeSent, "a", nil),
			ev(85, events.KindWakeOK, "a", nil),
			ev(86, events.KindWakeOK, "b", nil),
		},
	})
	r := rows[0]
	if r.Tone != ToneWarn || !strings.Contains(r.Result, "woke again by") {
		t.Fatalf("row = %+v", r)
	}
	if !strings.Contains(r.Result, "{{time:") {
		t.Fatalf("the time must be a token the page formats: %q", r.Result)
	}
}

func TestOutageWakeFailedIsCritical(t *testing.T) {
	rows := buildOutages(outageInput{
		Now: at(500),
		Events: []events.Event{
			ev(0, events.KindPowerLost, "", nil),
			ev(25, events.KindShutdownBegan, "", nil),
			ev(70, events.KindPowerRestored, "", map[string]any{"duration_s": 4200.0}),
			ev(400, events.KindWakeFailed, "b", nil),
		},
	})
	r := rows[0]
	if r.Tone != ToneCrit || !strings.Contains(r.Result, "b did not wake") {
		t.Fatalf("row = %+v", r)
	}
}

func TestOutageWaitingToWake(t *testing.T) {
	rows := buildOutages(outageInput{
		Now: at(75), WakeActive: true,
		Events: []events.Event{
			ev(0, events.KindPowerLost, "", nil),
			ev(25, events.KindShutdownBegan, "", nil),
			ev(70, events.KindPowerRestored, "", map[string]any{"duration_s": 4200.0}),
		},
	})
	if got := rows[0].Result; got != "The nodes shut down. Waiting to wake them." {
		t.Fatalf("result = %q", got)
	}
}

func TestOutageInProgress(t *testing.T) {
	active := &monitor.OutageStatus{Started: at(0), StartCharge: 100, MinCharge: 62}
	rows := buildOutages(outageInput{
		Now: at(10), Active: active, ShutdownCharge: 10,
		Events: []events.Event{ev(0, events.KindPowerLost, "", map[string]any{"charge": 100.0})},
	})
	r := rows[0]
	if !r.InProgress || r.End != nil {
		t.Fatalf("row = %+v", r)
	}
	if r.DurationS != 600 {
		t.Fatalf("duration = %d, want 600", r.DurationS)
	}
	if !strings.Contains(r.Result, "10%") {
		t.Fatalf("result = %q, want it to say the level the nodes shut down at", r.Result)
	}
	if r.MinCharge == nil || *r.MinCharge != 62 {
		t.Fatalf("min = %v", r.MinCharge)
	}
}

func TestOutageInProgressAfterShutdown(t *testing.T) {
	active := &monitor.OutageStatus{Started: at(0), StartCharge: 100, MinCharge: 9}
	rows := buildOutages(outageInput{
		Now: at(28), Active: active,
		Events: []events.Event{
			ev(0, events.KindPowerLost, "", nil),
			ev(26, events.KindShutdownBegan, "", nil),
		},
	})
	if got := rows[0].Result; !strings.HasPrefix(got, "In progress. The nodes shut down at {{time:") {
		t.Fatalf("result = %q", got)
	}
}

func TestOutageEndNotRecorded(t *testing.T) {
	// the Pi lost power with the cut underway and no later event closed it
	rows := buildOutages(outageInput{
		Now: at(500),
		Events: []events.Event{
			ev(0, events.KindPowerLost, "", nil),
			ev(100, events.KindPowerLost, "", nil),
			ev(105, events.KindPowerRestored, "", map[string]any{"duration_s": 300.0}),
		},
	})
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	// newest first
	if !rows[0].Start.Equal(at(100)) || !rows[1].Start.Equal(at(0)) {
		t.Fatalf("order = %v, %v", rows[0].Start, rows[1].Start)
	}
	if rows[1].End != nil || rows[1].Tone != ToneWarn || !strings.Contains(rows[1].Result, "not recorded") {
		t.Fatalf("older row = %+v", rows[1])
	}
}

func TestOutageApproximate(t *testing.T) {
	rows := buildOutages(outageInput{
		Now: at(500),
		Events: []events.Event{
			ev(0, events.KindPowerLost, "", nil),
			ev(90, events.KindPowerRestored, "", map[string]any{"duration_s": 5400.0, "approximate": true}),
		},
	})
	if !rows[0].Approximate {
		t.Fatal("approximate flag lost")
	}
}

func TestOutageNoEvents(t *testing.T) {
	if rows := buildOutages(outageInput{Now: at(1)}); len(rows) != 0 {
		t.Fatalf("rows = %d", len(rows))
	}
}

func TestExpiredEventsDoNotLeakIntoTheNextOutage(t *testing.T) {
	// the shutdown at minute 25 belongs to the first cut only
	rows := buildOutages(outageInput{
		Now: at(300),
		Events: []events.Event{
			ev(0, events.KindPowerLost, "", nil),
			ev(25, events.KindShutdownBegan, "", nil),
			ev(70, events.KindPowerRestored, "", map[string]any{"duration_s": 4200.0}),
			ev(200, events.KindPowerLost, "", nil),
			ev(205, events.KindPowerRestored, "", map[string]any{"duration_s": 300.0}),
		},
	})
	if !strings.Contains(rows[0].Result, "Rode through") {
		t.Fatalf("second cut = %q", rows[0].Result)
	}
	if strings.Contains(rows[1].Result, "Rode through") {
		t.Fatalf("first cut = %q", rows[1].Result)
	}
}
