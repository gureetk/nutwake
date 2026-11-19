package monitor

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/clocksync"
	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/probe"
	"github.com/gureetk/nutwake/internal/state"
)

// fakeUPS is a Reader the test controls.
type fakeUPS struct {
	mu   sync.Mutex
	vars map[string]string
	err  error
}

func newUPS(status string, charge string) *fakeUPS {
	return &fakeUPS{vars: map[string]string{"ups.status": status, "battery.charge": charge, "battery.charge.low": "10", "device.model": "Test UPS"}}
}

func (f *fakeUPS) set(status, charge string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vars["ups.status"], f.vars["battery.charge"] = status, charge
	f.err = nil
}

func (f *fakeUPS) fail(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }

func (f *fakeUPS) Read(context.Context) (*nut.Reading, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	vars := make(map[string]string, len(f.vars))
	for k, v := range f.vars {
		vars[k] = v
	}
	return &nut.Reading{At: time.Now(), Vars: vars}, nil
}

// fakeProbe is a Prober the test controls.
type fakeProbe struct{ up atomic.Bool }

func (p *fakeProbe) Up(context.Context, string) bool { return p.up.Load() }

type rig struct {
	t      *testing.T
	m      *Monitor
	ups    *fakeUPS
	nodes  map[string]*fakeProbe
	events *events.Store
	state  *state.Store
	now    time.Time
	dir    string
}

type rigOpts struct {
	dir   string
	clock *clocksync.Clock
	ups   *fakeUPS
	start time.Time
}

func newRig(t *testing.T, o rigOpts) *rig {
	t.Helper()
	if o.dir == "" {
		o.dir = t.TempDir()
	}
	if o.ups == nil {
		o.ups = newUPS("OL CHRG", "100")
	}
	if o.start.IsZero() {
		o.start = time.Date(2026, 11, 13, 19, 0, 0, 0, time.UTC)
	}
	cfg := config.Default()
	cfg.Nodes = []config.Node{
		{Name: "kaios", Host: "10.0.0.10", MAC: "aa:bb:cc:dd:ee:01"},
		{Name: "node-b", Host: "10.0.0.11", MAC: "aa:bb:cc:dd:ee:02"},
	}
	r := &rig{t: t, ups: o.ups, nodes: map[string]*fakeProbe{}, now: o.start, dir: o.dir}
	deps := Deps{UPS: o.ups, Clock: o.clock, Now: func() time.Time { return r.now }}
	deps.Probers = map[string]probe.Prober{}
	for _, n := range cfg.Nodes {
		p := &fakeProbe{}
		p.up.Store(true)
		r.nodes[n.Name] = p
		deps.Probers[n.Name] = p
	}
	var err error
	if r.events, err = events.Open(filepath.Join(o.dir, "events.jsonl"), o.clock); err != nil {
		t.Fatal(err)
	}
	r.state = state.New(o.dir)
	deps.Events, deps.State = r.events, r.state
	if r.m, err = New(&cfg, deps); err != nil {
		t.Fatal(err)
	}
	return r
}

// poll advances the fake time by step and polls once.
func (r *rig) poll(step time.Duration) {
	r.t.Helper()
	r.now = r.now.Add(step)
	r.m.Poll(context.Background())
}

func (r *rig) kinds() []string {
	r.t.Helper()
	evs, err := r.events.All()
	if err != nil {
		r.t.Fatal(err)
	}
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

func (r *rig) last(kind string) events.Event {
	r.t.Helper()
	e, err := r.events.Latest(kind)
	if err != nil {
		r.t.Fatalf("no %s event (have %v)", kind, r.kinds())
	}
	return e
}

func wantKinds(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestOutageFromStartToEnd(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.poll(0)
	r.poll(5 * time.Second)
	wantKinds(t, r.kinds()) // quiet while all is well

	r.ups.set("OB DISCHRG", "96")
	r.poll(5 * time.Second)
	if o, _ := r.state.LoadOutage(); o == nil || o.StartCharge != 96 || !slices.Equal(o.NodesUp, []string{"kaios", "node-b"}) {
		t.Fatalf("outage record: %+v", o)
	}
	if snap := r.m.Snapshot(); snap.Outage == nil || snap.UPS.State != nut.StateOnBattery {
		t.Fatalf("snapshot during outage: %+v", snap.Outage)
	}

	r.ups.set("OB DISCHRG", "52")
	r.poll(30 * time.Minute)
	r.ups.set("OB DISCHRG LB", "9")
	r.poll(10 * time.Minute)
	r.poll(5 * time.Second) // low battery is announced once
	r.ups.set("OL CHRG", "9")
	r.poll(5 * time.Second)

	wantKinds(t, r.kinds(), "power_lost", "low_battery", "power_restored")
	e := r.last("power_restored")
	if got := e.Data["duration_s"]; got != float64(40*60+10) {
		t.Errorf("duration_s = %v", got)
	}
	if e.Data["min_charge"] != float64(9) || e.Data["start_charge"] != float64(96) {
		t.Errorf("charges: %+v", e.Data)
	}
	if e.Data["approximate"] != nil {
		t.Error("an outage watched from start to end is not approximate")
	}
	if o, _ := r.state.LoadOutage(); o != nil {
		t.Error("the outage record should be removed")
	}
	if r.m.Snapshot().Outage != nil {
		t.Error("no outage should be shown any more")
	}
}

func TestStartedWhileOnBatteryRecordsAnOutage(t *testing.T) {
	r := newRig(t, rigOpts{ups: newUPS("OB DISCHRG", "80")})
	r.poll(0)
	wantKinds(t, r.kinds(), "power_lost")
}

func TestRestartDuringOutageContinuesIt(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, rigOpts{dir: dir})
	r.poll(0)
	r.ups.set("OB DISCHRG", "100")
	r.poll(5 * time.Second)
	r.ups.set("OB DISCHRG LB", "8")
	r.poll(time.Minute)
	wantKinds(t, r.kinds(), "power_lost", "low_battery")

	// the daemon restarts while still on battery
	r2 := newRig(t, rigOpts{dir: dir, ups: r.ups, start: r.now.Add(10 * time.Second)})
	r2.poll(0)
	r2.poll(time.Minute)
	wantKinds(t, r2.kinds(), "power_lost", "low_battery") // nothing repeated

	r2.ups.set("OL CHRG", "8")
	r2.poll(time.Minute)
	e := r2.last("power_restored")
	if e.Data["approximate"] != true {
		t.Errorf("an outage that spans a restart is approximate: %+v", e.Data)
	}
	if e.Data["start_charge"] != float64(100) {
		t.Errorf("start charge lost across the restart: %+v", e.Data)
	}
}

func TestOutageEndingWhilePiWasOffIsClosedAfterReboot(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, rigOpts{dir: dir})
	r.poll(0)
	r.ups.set("OB DISCHRG", "70")
	r.poll(5 * time.Second)

	// the Pi halts, power returns, the Pi boots two hours later
	r2 := newRig(t, rigOpts{dir: dir, start: r.now.Add(2 * time.Hour)})
	r2.poll(0)
	wantKinds(t, r2.kinds(), "power_lost", "power_restored")
	e := r2.last("power_restored")
	if e.Data["approximate"] != true {
		t.Errorf("want approximate: %+v", e.Data)
	}
	if got := e.Data["duration_s"].(float64); got < 7200 {
		t.Errorf("duration_s = %v, want about two hours", got)
	}
	if o, _ := r2.state.LoadOutage(); o != nil {
		t.Error("record should be cleared")
	}
}

func TestEndOfOutageWaitsForTheClock(t *testing.T) {
	dir := t.TempDir()
	// a power cut saved 90 minutes ago, before the Pi went down
	st := state.New(dir)
	if err := st.SaveOutage(&state.Outage{Started: time.Now().Add(-90 * time.Minute), StartCharge: 100, MinCharge: 40}); err != nil {
		t.Fatal(err)
	}
	var synced atomic.Bool
	clock := clocksync.NewWith(synced.Load, time.Hour)
	r := newRig(t, rigOpts{dir: dir, clock: clock})
	r.m.now = time.Now // real time: the clock fix works on real monotonic readings

	r.m.Poll(context.Background())
	if o, _ := st.LoadOutage(); o == nil {
		t.Fatal("the outage record must stay until the end can be timed")
	}
	if r.events.Pending() != 0 {
		t.Fatal("nothing should be recorded yet")
	}
	if r.m.Snapshot().Outage != nil {
		t.Error("power is back, so no outage should be shown")
	}

	time.Sleep(20 * time.Millisecond)
	synced.Store(true)
	r.m.Poll(context.Background())
	e := r.last("power_restored")
	if d := e.Data["duration_s"].(float64); d < 90*60-5 || d > 90*60+60 {
		t.Errorf("duration_s = %v, want about 5400", d)
	}
	if o, _ := st.LoadOutage(); o != nil {
		t.Error("record should be cleared once the end is recorded")
	}
}

func TestPowerFlickerWhileWaitingForClockIsOneOutage(t *testing.T) {
	var synced atomic.Bool
	clock := clocksync.NewWith(synced.Load, time.Hour)
	r := newRig(t, rigOpts{clock: clock, ups: newUPS("OB DISCHRG", "90")})
	r.m.now = time.Now
	r.m.Poll(context.Background())
	r.ups.set("OL CHRG", "90")
	r.m.Poll(context.Background())
	r.ups.set("OB DISCHRG", "89")
	r.m.Poll(context.Background())
	synced.Store(true)
	r.ups.set("OL CHRG", "89")
	r.m.Poll(context.Background())
	wantKinds(t, r.kinds(), "power_lost", "power_restored")
}

func TestOutageStartedBeforeClockWasSetGetsTheRightStart(t *testing.T) {
	var synced atomic.Bool
	clock := clocksync.NewWith(synced.Load, time.Hour)
	r := newRig(t, rigOpts{dir: t.TempDir(), clock: clock, ups: newUPS("OB DISCHRG", "90")})
	r.m.now = time.Now
	r.m.Poll(context.Background())
	if o, _ := r.state.LoadOutage(); o != nil {
		t.Fatal("a start time that may be wrong must not be saved yet")
	}
	time.Sleep(50 * time.Millisecond)
	synced.Store(true)
	before := time.Now()
	r.m.Poll(context.Background())
	o, _ := r.state.LoadOutage()
	if o == nil {
		t.Fatal("outage should be saved once the clock is set")
	}
	if !o.Started.Before(before.Add(-40 * time.Millisecond)) {
		t.Errorf("Started %v should be about 50ms before %v", o.Started, before)
	}
}

func TestUPSUnreachableIsDebounced(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.poll(0)
	r.ups.fail(errors.New("connection refused"))
	r.poll(5 * time.Second)
	r.poll(5 * time.Second)
	wantKinds(t, r.kinds())
	if !r.m.Snapshot().UPS.Reachable {
		t.Error("two missed reads should not mark the UPS unreachable")
	}
	r.poll(5 * time.Second)
	wantKinds(t, r.kinds(), "ups_unreachable")
	if s := r.m.Snapshot(); s.UPS.Reachable || s.UPS.State != nut.StateUnreachable || s.UPS.Error == "" {
		t.Errorf("snapshot: %+v", s.UPS)
	}
	r.poll(5 * time.Second)
	wantKinds(t, r.kinds(), "ups_unreachable") // not repeated
	r.ups.set("OL CHRG", "100")
	r.poll(5 * time.Second)
	wantKinds(t, r.kinds(), "ups_unreachable", "ups_reconnected")
}

func TestUPSNeverReachable(t *testing.T) {
	ups := newUPS("OL", "100")
	ups.fail(errors.New("refused"))
	r := newRig(t, rigOpts{ups: ups})
	for i := 0; i < 3; i++ {
		r.poll(5 * time.Second)
	}
	wantKinds(t, r.kinds(), "ups_unreachable")
	ups.set("OL CHRG", "100")
	r.poll(5 * time.Second)
	// reaching it for the first time is not a "reconnection" unless we had it before
	if got := r.kinds(); len(got) != 1 {
		t.Errorf("events = %v", got)
	}
	if !r.m.Snapshot().UPS.Reachable {
		t.Error("UPS should be reachable now")
	}
}

func TestReplaceBatteryIsAnnouncedOnce(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.poll(0)
	r.ups.set("OL RB", "100")
	r.poll(5 * time.Second)
	r.poll(5 * time.Second)
	wantKinds(t, r.kinds(), "replace_battery")
	if !r.m.Snapshot().UPS.ReplaceBattery {
		t.Error("snapshot should show the flag")
	}
}

func TestReplaceBatterySetAtStartIsNotRepeatedAfterEveryRestart(t *testing.T) {
	dir := t.TempDir()
	ups := newUPS("OL RB", "100")
	r := newRig(t, rigOpts{dir: dir, ups: ups})
	r.poll(0)
	wantKinds(t, r.kinds(), "replace_battery")
	r2 := newRig(t, rigOpts{dir: dir, ups: ups})
	r2.poll(0)
	wantKinds(t, r2.kinds(), "replace_battery")
}

func TestNodeStatesAndDebounce(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.poll(0)
	wantKinds(t, r.kinds()) // first answers are silent
	for _, n := range r.m.Snapshot().Nodes {
		if n.State != NodeUp {
			t.Errorf("%s = %s", n.Name, n.State)
		}
	}

	r.nodes["kaios"].up.Store(false)
	r.poll(5 * time.Second) // one failure: down_after is 2
	wantKinds(t, r.kinds())
	r.poll(5 * time.Second)
	wantKinds(t, r.kinds(), "node_down")
	if e := r.last("node_down"); e.Node != "kaios" {
		t.Errorf("node = %q", e.Node)
	}

	r.nodes["kaios"].up.Store(true)
	r.poll(4 * time.Minute)
	wantKinds(t, r.kinds(), "node_down", "node_up")
	if e := r.last("node_up"); e.Data["down_for_s"].(float64) < 240 {
		t.Errorf("down_for_s = %v", e.Data["down_for_s"])
	}
}

func TestNodeDownAtStartIsSilentAndUnknownUntilProven(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.nodes["kaios"].up.Store(false)
	r.poll(0)
	if got := r.m.Snapshot().Nodes[0].State; got != NodeUnknown {
		t.Errorf("after one failed probe = %s, want unknown", got)
	}
	r.poll(5 * time.Second)
	if got := r.m.Snapshot().Nodes[0].State; got != NodeDown {
		t.Errorf("after two failed probes = %s, want down", got)
	}
	wantKinds(t, r.kinds()) // the first state of a node is not an event
}

func TestNodesUpWhenPowerWasLostAreRecorded(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.nodes["node-b"].up.Store(false)
	r.poll(0)
	r.poll(5 * time.Second)
	r.ups.set("OB DISCHRG", "99")
	r.poll(5 * time.Second)
	if o, _ := r.state.LoadOutage(); o == nil || !slices.Equal(o.NodesUp, []string{"kaios"}) {
		t.Errorf("outage: %+v", o)
	}
	r.nodes["kaios"].up.Store(false)
	r.poll(5 * time.Second)
	r.poll(5 * time.Second)
	if e := r.last("node_down"); e.Data["during_outage"] != true {
		t.Errorf("node_down during an outage should say so: %+v", e.Data)
	}
}

func TestOtherUPSStateIsNotAnOutage(t *testing.T) {
	r := newRig(t, rigOpts{ups: newUPS("OFF", "100")})
	r.poll(0)
	r.poll(time.Minute)
	wantKinds(t, r.kinds())
	if got := r.m.Snapshot().UPS.State; got != nut.StateOther {
		t.Errorf("state = %s", got)
	}
}

func TestSnapshotContents(t *testing.T) {
	ups := newUPS("OL CHRG", "97")
	ups.vars["ups.load"] = "23"
	ups.vars["battery.runtime"] = "1500"
	ups.vars["input.voltage"] = "121.5"
	r := newRig(t, rigOpts{ups: ups})
	r.poll(0)
	s := r.m.Snapshot()
	if !s.UPS.Reachable || s.UPS.State != nut.StateOnline || s.UPS.Status != "OL CHRG" || s.UPS.Model != "Test UPS" {
		t.Errorf("ups: %+v", s.UPS)
	}
	for name, got := range map[string]*float64{"charge": s.UPS.Charge, "low": s.UPS.ChargeLow, "runtime": s.UPS.RuntimeSeconds, "load": s.UPS.Load, "volts": s.UPS.InputVoltage} {
		if got == nil {
			t.Errorf("%s missing", name)
		}
	}
	if *s.UPS.Charge != 97 || *s.UPS.ChargeLow != 10 || *s.UPS.Load != 23 {
		t.Errorf("values: %v %v %v", *s.UPS.Charge, *s.UPS.ChargeLow, *s.UPS.Load)
	}
	if len(s.Nodes) != 2 || s.Nodes[0].Name != "kaios" || s.Nodes[0].Probe != "icmp" {
		t.Errorf("nodes: %+v", s.Nodes)
	}
	if len(s.Samples) != 1 || s.Samples[0].Charge == nil || *s.Samples[0].Charge != 97 {
		t.Errorf("samples: %+v", s.Samples)
	}
	// the copy must be independent
	s.Samples[0].State = "changed"
	if r.m.Snapshot().Samples[0].State == "changed" {
		t.Error("Snapshot shares memory with the monitor")
	}
}

func TestSamplesAreThinnedWhenNothingChanges(t *testing.T) {
	r := newRig(t, rigOpts{})
	for i := 0; i < 10; i++ {
		r.poll(5 * time.Second)
	}
	if n := len(r.m.Snapshot().Samples); n != 2 {
		t.Errorf("%d samples over 50 s of steady state, want 2", n)
	}
	r.ups.set("OB DISCHRG", "99")
	for i := 0; i < 5; i++ {
		r.poll(5 * time.Second)
	}
	if n := len(r.m.Snapshot().Samples); n != 7 {
		t.Errorf("%d samples, want every poll while on battery", n)
	}
}

func TestSampleRingIsBounded(t *testing.T) {
	r := newRig(t, rigOpts{ups: newUPS("OB DISCHRG", "100")})
	for i := 0; i < maxSamples+50; i++ {
		r.poll(time.Second)
	}
	if n := len(r.m.Snapshot().Samples); n != maxSamples {
		t.Errorf("%d samples, want %d", n, maxSamples)
	}
}

func TestSnapshotDuringPollsIsRaceFree(t *testing.T) {
	r := newRig(t, rigOpts{})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = r.m.Snapshot()
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		if i == 20 {
			r.ups.set("OB DISCHRG", "90")
		}
		r.m.Poll(context.Background())
	}
	close(stop)
	wg.Wait()
}

func TestCancelledPollRecordsNothing(t *testing.T) {
	r := newRig(t, rigOpts{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.ups.fail(errors.New("context canceled"))
	for i := 0; i < 5; i++ {
		r.m.Poll(ctx)
	}
	wantKinds(t, r.kinds())
}

func TestNewRejectsMissingStores(t *testing.T) {
	cfg := config.Default()
	if _, err := New(&cfg, Deps{}); err == nil {
		t.Error("want an error")
	}
}

func TestRunStopsWhenCancelled(t *testing.T) {
	r := newRig(t, rigOpts{})
	r.m.cfg.PollInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.m.Run(ctx); close(done) }()
	time.Sleep(60 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
	if len(r.m.Snapshot().Samples) == 0 {
		t.Error("Run should have polled")
	}
}
