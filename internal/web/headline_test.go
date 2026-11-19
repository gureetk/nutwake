package web

import (
	"strings"
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/monitor"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/samples"
	"github.com/gureetk/nutwake/internal/waker"
)

func nodesOf(states ...monitor.NodeState) []monitor.NodeStatus {
	names := []string{"kaios", "node-b", "node-c"}
	var out []monitor.NodeStatus
	for i, st := range states {
		out = append(out, monitor.NodeStatus{Name: names[i], State: st})
	}
	return out
}

func head(in headlineIn) Headline {
	in.Now = at(100)
	if in.NUTAddr == "" {
		in.NUTAddr = "127.0.0.1:3493"
	}
	return buildHeadline(in)
}

func TestHeadlineOnline(t *testing.T) {
	h := head(headlineIn{Snap: monitor.Snapshot{
		UPS:   monitor.UPSStatus{Reachable: true, State: nut.StateOnline},
		Nodes: nodesOf(monitor.NodeUp, monitor.NodeUp),
	}})
	if h.Tone != ToneOK || h.Title != "Power is on" {
		t.Fatalf("headline = %+v", h)
	}
	if !strings.Contains(h.Text, "Both nodes are up") || !strings.Contains(h.Text, "No power cuts recorded yet") {
		t.Fatalf("text = %q", h.Text)
	}
}

func TestHeadlineOnlineNodeDown(t *testing.T) {
	h := head(headlineIn{Snap: monitor.Snapshot{
		UPS:   monitor.UPSStatus{Reachable: true, State: nut.StateOnline},
		Nodes: nodesOf(monitor.NodeUp, monitor.NodeDown),
	}})
	if h.Tone != ToneWarn || !strings.Contains(h.Text, "Node-b is down") {
		t.Fatalf("headline = %+v", h)
	}
}

func TestHeadlineLastOutage(t *testing.T) {
	end := at(40)
	h := head(headlineIn{
		Snap: monitor.Snapshot{
			UPS:   monitor.UPSStatus{Reachable: true, State: nut.StateOnline},
			Nodes: nodesOf(monitor.NodeUp, monitor.NodeUp),
		},
		Rows: []OutageRow{{Start: at(0), End: &end}},
	})
	if !strings.Contains(h.Text, "The last outage was 1 h") {
		t.Fatalf("text = %q", h.Text)
	}
}

func TestHeadlineUnreachable(t *testing.T) {
	h := head(headlineIn{Snap: monitor.Snapshot{
		UPS:   monitor.UPSStatus{Reachable: false, State: nut.StateUnreachable},
		Nodes: nodesOf(monitor.NodeUp),
	}, NUTAddr: "192.0.2.5:3493"})
	if h.Tone != ToneCrit || !strings.Contains(h.Title, "reach the UPS") || !strings.Contains(h.Text, "192.0.2.5:3493") {
		t.Fatalf("headline = %+v", h)
	}
}

func TestHeadlineOnBatteryWithExpectedShutdown(t *testing.T) {
	exp := at(110)
	h := head(headlineIn{
		Snap: monitor.Snapshot{
			UPS:    monitor.UPSStatus{Reachable: true, State: nut.StateOnBattery},
			Nodes:  nodesOf(monitor.NodeUp, monitor.NodeUp),
			Outage: &monitor.OutageStatus{Started: at(83)},
		},
		ExpectedShutdown: &exp, ShutdownCharge: 10,
	})
	if h.Tone != ToneWarn || h.Title != "Running on battery" {
		t.Fatalf("headline = %+v", h)
	}
	for _, want := range []string{"On battery for 17 min", "kaios and node-b shut down at 10%", "{{time:"} {
		if !strings.Contains(h.Text, want) {
			t.Fatalf("text %q lacks %q", h.Text, want)
		}
	}
}

func TestHeadlineOnBatteryAfterNodesShutDown(t *testing.T) {
	h := head(headlineIn{Snap: monitor.Snapshot{
		UPS:    monitor.UPSStatus{Reachable: true, State: nut.StateOnBattery},
		Nodes:  nodesOf(monitor.NodeDown, monitor.NodeDown),
		Outage: &monitor.OutageStatus{Started: at(60)},
	}})
	if !strings.Contains(h.Text, "Kaios, node-b are down") && !strings.Contains(h.Text, "Kaios and node-b are down") {
		t.Fatalf("text = %q", h.Text)
	}
}

func TestHeadlineLowBattery(t *testing.T) {
	h := head(headlineIn{Snap: monitor.Snapshot{
		UPS:   monitor.UPSStatus{Reachable: true, State: nut.StateLowBattery},
		Nodes: nodesOf(monitor.NodeUp),
	}})
	if h.Tone != ToneCrit || h.Title != "Battery is low" {
		t.Fatalf("headline = %+v", h)
	}
}

func TestHeadlineOtherStatus(t *testing.T) {
	h := head(headlineIn{Snap: monitor.Snapshot{
		UPS: monitor.UPSStatus{Reachable: true, State: nut.StateOther, Status: "OFF"},
	}})
	if h.Tone != ToneWarn || !strings.Contains(h.Text, `"OFF"`) {
		t.Fatalf("headline = %+v", h)
	}
}

func TestHeadlineWaitingToWake(t *testing.T) {
	h := head(headlineIn{
		Snap: monitor.Snapshot{UPS: monitor.UPSStatus{Reachable: true, State: nut.StateOnline}},
		Wake: waker.Status{
			Active: true, Waiting: waker.WaitBattery, Message: "Waiting for the battery to reach 15% (now 13%).",
			Nodes: []waker.NodeProgress{{Name: "kaios"}, {Name: "node-b"}},
		},
	})
	if h.Tone != ToneWait || h.Title != "Power is back" {
		t.Fatalf("headline = %+v", h)
	}
	if !strings.Contains(h.Text, "Kaios and node-b will be woken then.") {
		t.Fatalf("text = %q", h.Text)
	}
}

func TestHeadlineSendingWake(t *testing.T) {
	h := head(headlineIn{
		Snap: monitor.Snapshot{UPS: monitor.UPSStatus{Reachable: true, State: nut.StateOnline}},
		Wake: waker.Status{Active: true, Nodes: []waker.NodeProgress{{Name: "kaios"}}},
	})
	if !strings.Contains(h.Text, "Sending wake packets") || !strings.Contains(h.Text, "kaios") {
		t.Fatalf("text = %q", h.Text)
	}
}

func onBat(charge float64, runtimeS float64) monitor.UPSStatus {
	return monitor.UPSStatus{Reachable: true, State: nut.StateOnBattery, Charge: &charge, RuntimeSeconds: &runtimeS}
}

func TestExpectedShutdownFromTheDischargeSlope(t *testing.T) {
	now := at(10)
	// 100% to 80% in 10 minutes: 2% a minute, so from 80% to 10% is 35 minutes
	recent := []samples.Sample{
		smp(0, nut.StateOnBattery, 100, nil, nil),
		smp(10, nut.StateOnBattery, 80, nil, nil),
	}
	got := expectedShutdown(onBat(80, 3000), recent, 10, now)
	if got == nil {
		t.Fatal("no estimate")
	}
	if d := got.Sub(now); d < 34*time.Minute || d > 36*time.Minute {
		t.Fatalf("estimate is %v away, want about 35 minutes", d)
	}
}

func TestExpectedShutdownFallsBackToRuntime(t *testing.T) {
	now := at(1)
	// 50% charge with 40 minutes to empty: 80% of that is spent getting to 10%
	got := expectedShutdown(onBat(50, 2400), nil, 10, now)
	if got == nil {
		t.Fatal("no estimate")
	}
	if d := got.Sub(now); d < 31*time.Minute || d > 33*time.Minute {
		t.Fatalf("estimate is %v away, want about 32 minutes", d)
	}
}

func TestExpectedShutdownNone(t *testing.T) {
	now := at(10)
	online := monitor.UPSStatus{Reachable: true, State: nut.StateOnline}
	if expectedShutdown(online, nil, 10, now) != nil {
		t.Fatal("estimate while on mains")
	}
	if expectedShutdown(onBat(8, 100), nil, 10, now) != nil {
		t.Fatal("estimate when already below the shutdown level")
	}
	c := 50.0
	noRuntime := monitor.UPSStatus{Reachable: true, State: nut.StateOnBattery, Charge: &c}
	if expectedShutdown(noRuntime, nil, 10, now) != nil {
		t.Fatal("estimate with nothing to go on")
	}
	// a flat battery (no discharge seen) and a silly runtime is not believed
	if got := expectedShutdown(onBat(50, 10*24*3600), nil, 10, now); got != nil {
		t.Fatalf("estimate = %v for a ten day runtime", got)
	}
}

func TestExpectedShutdownIgnoresOldReadings(t *testing.T) {
	now := at(100)
	// readings from an earlier cut must not make a rate
	recent := []samples.Sample{
		smp(0, nut.StateOnBattery, 100, nil, nil),
		smp(30, nut.StateOnBattery, 10, nil, nil),
	}
	got := expectedShutdown(onBat(60, 1800), recent, 10, now)
	if got == nil {
		t.Fatal("no estimate")
	}
	// falls back to the runtime: 30 min * 50/60 = 25 min
	if d := got.Sub(now); d < 24*time.Minute || d > 26*time.Minute {
		t.Fatalf("estimate is %v away, want about 25 minutes", d)
	}
}
