package waker

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/monitor"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/state"
)

type fakeSource struct {
	mu sync.Mutex
	b  monitor.Brief
}

func (f *fakeSource) Brief() monitor.Brief {
	f.mu.Lock()
	defer f.mu.Unlock()
	b := f.b
	b.Nodes = slices.Clone(f.b.Nodes)
	return b
}

type packet struct {
	addr string
	mac  string
}

type rig struct {
	t       *testing.T
	w       *Waker
	src     *fakeSource
	events  *events.Store
	state   *state.Store
	now     time.Time
	sent    []packet
	sendErr error
	dir     string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	cfg := config.Default()
	cfg.Nodes = []config.Node{
		{Name: "kaios", Host: "10.0.0.10", MAC: "aa:bb:cc:dd:ee:01"},
		{Name: "node-b", Host: "10.0.0.11", MAC: "aa:bb:cc:dd:ee:02"},
	}
	r := &rig{t: t, now: time.Date(2026, 11, 13, 21, 0, 0, 0, time.UTC)}
	r.src = &fakeSource{}
	dir := t.TempDir()
	r.dir = dir
	var err error
	if r.events, err = events.Open(filepath.Join(dir, "events.jsonl"), nil); err != nil {
		t.Fatal(err)
	}
	r.state = state.New(dir)
	r.w, err = New(&cfg, Deps{
		Source: r.src, Events: r.events, State: r.state, Now: func() time.Time { return r.now },
		Send: func(addr string, mac net.HardwareAddr) error {
			if r.sendErr != nil {
				return r.sendErr
			}
			r.sent = append(r.sent, packet{addr, mac.String()})
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	r.setUPS(nut.StateOnline, 100, 10*time.Minute)
	r.setNodes(monitor.NodeDown, monitor.NodeDown)
	return r
}

// setUPS sets the UPS state, its charge (negative: unknown), and how long ago
// it entered that state.
func (r *rig) setUPS(st nut.State, charge float64, stableFor time.Duration) {
	r.src.mu.Lock()
	defer r.src.mu.Unlock()
	u := monitor.UPSStatus{Reachable: st != nut.StateUnreachable, State: st, Since: r.now.Add(-stableFor), Status: "OL"}
	if charge >= 0 {
		u.Charge = &charge
	}
	r.src.b.UPS = u
}

func (r *rig) setNodes(kaios, nodeB monitor.NodeState) {
	r.src.mu.Lock()
	defer r.src.mu.Unlock()
	r.src.b.Nodes = []monitor.NodeStatus{{Name: "kaios", State: kaios}, {Name: "node-b", State: nodeB}}
}

func (r *rig) pend(names ...string) {
	r.t.Helper()
	if err := r.state.SavePending(&state.Pending{Since: r.now.Add(-time.Hour), Nodes: names}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *rig) tick(step time.Duration) {
	r.now = r.now.Add(step)
	r.w.Tick()
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

func (r *rig) pending() []string {
	p, err := r.state.LoadPending()
	if err != nil {
		r.t.Fatal(err)
	}
	if p == nil {
		return nil
	}
	return p.Nodes
}

func (r *rig) macs() []string {
	var out []string
	for _, p := range r.sent {
		out = append(out, p.mac)
	}
	return out
}

func wantKinds(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestNothingPendingDoesNothing(t *testing.T) {
	r := newRig(t)
	r.tick(time.Second)
	r.tick(time.Minute)
	wantKinds(t, r.kinds())
	if len(r.sent) != 0 || r.w.Status().Active {
		t.Error("nothing should happen")
	}
}

func TestWakesPendingNodesInTurnThenFinishes(t *testing.T) {
	r := newRig(t)
	r.pend("kaios", "node-b")
	r.tick(0)
	if !slices.Equal(r.macs(), []string{"aa:bb:cc:dd:ee:01"}) {
		t.Fatalf("first tick sent %v, want only the first node", r.macs())
	}
	if r.sent[0].addr != "255.255.255.255:9" {
		t.Errorf("broadcast address = %s", r.sent[0].addr)
	}
	r.tick(time.Second) // stagger is 5s
	if len(r.sent) != 1 {
		t.Fatalf("second node woken too early: %v", r.macs())
	}
	r.tick(5 * time.Second)
	if !slices.Equal(r.macs(), []string{"aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:02"}) {
		t.Fatalf("packets: %v", r.macs())
	}
	wantKinds(t, r.kinds(), "wake_pending", "wake_sent", "wake_sent")

	r.setNodes(monitor.NodeUp, monitor.NodeDown)
	r.tick(40 * time.Second)
	wantKinds(t, r.kinds(), "wake_pending", "wake_sent", "wake_sent", "wake_ok")
	if got := r.pending(); !slices.Equal(got, []string{"node-b"}) {
		t.Errorf("pending = %v", got)
	}
	r.setNodes(monitor.NodeUp, monitor.NodeUp)
	r.tick(10 * time.Second)
	if got := r.pending(); got != nil {
		t.Errorf("pending = %v, want none", got)
	}
	if r.w.Status().Active {
		t.Error("should be idle now")
	}
	// no further packets once everything is up
	n := len(r.sent)
	r.tick(10 * time.Minute)
	if len(r.sent) != n {
		t.Error("sent packets after finishing")
	}
}

func TestOnlyNodesOnTheListAreWoken(t *testing.T) {
	r := newRig(t)
	r.pend("node-b")
	r.tick(0)
	if !slices.Equal(r.macs(), []string{"aa:bb:cc:dd:ee:02"}) {
		t.Errorf("packets: %v", r.macs())
	}
}

func TestNodeAlreadyUpIsSkipped(t *testing.T) {
	r := newRig(t)
	r.setNodes(monitor.NodeUp, monitor.NodeDown)
	r.pend("kaios", "node-b")
	r.tick(0)
	// kaios is skipped, and node-b does not wait out a stagger for it
	if !slices.Equal(r.macs(), []string{"aa:bb:cc:dd:ee:02"}) {
		t.Errorf("packets: %v", r.macs())
	}
	wantKinds(t, r.kinds(), "wake_pending", "wake_skipped", "wake_sent")
}

func TestUnknownNodeStateWaitsForTheMonitor(t *testing.T) {
	r := newRig(t)
	r.setNodes(monitor.NodeUnknown, monitor.NodeUnknown)
	r.pend("kaios")
	r.tick(0)
	r.tick(time.Minute)
	if len(r.sent) != 0 {
		t.Fatal("must not send before the node state is known")
	}
	r.setNodes(monitor.NodeDown, monitor.NodeDown)
	r.tick(time.Second)
	if len(r.sent) != 1 {
		t.Error("should send once the node is known to be down")
	}
}

func TestWaitsWhileOnBatteryAndSaysSoOnce(t *testing.T) {
	r := newRig(t)
	r.setUPS(nut.StateOnBattery, 80, time.Minute)
	r.pend("kaios")
	r.tick(0)
	r.tick(time.Minute)
	r.tick(time.Minute)
	if len(r.sent) != 0 {
		t.Fatal("woke a node while on battery")
	}
	wantKinds(t, r.kinds(), "wake_pending", "wake_waiting")
	s := r.w.Status()
	if !s.Active || s.Waiting != WaitOnBattery || len(s.Nodes) != 1 || s.Nodes[0].State != "waiting" {
		t.Errorf("status: %+v", s)
	}
}

func TestWaitsForPowerToSettleWithoutNoise(t *testing.T) {
	r := newRig(t)
	r.setUPS(nut.StateOnline, 100, 10*time.Second) // delay is 1 minute
	r.pend("kaios")
	r.tick(0)
	if len(r.sent) != 0 || r.w.Status().Waiting != WaitSettling {
		t.Fatalf("sent %v, status %+v", r.macs(), r.w.Status())
	}
	wantKinds(t, r.kinds(), "wake_pending") // settling is not written to the history
	r.setUPS(nut.StateOnline, 100, 61*time.Second)
	r.tick(time.Second)
	if len(r.sent) != 1 {
		t.Error("should wake once power has been steady for the delay")
	}
}

func TestWaitsForTheBatteryThenWakes(t *testing.T) {
	r := newRig(t)
	r.setUPS(nut.StateOnline, 9, 5*time.Minute)
	r.pend("kaios")
	r.tick(0)
	r.setUPS(nut.StateOnline, 11, 6*time.Minute)
	r.tick(time.Minute)
	if len(r.sent) != 0 {
		t.Fatal("woke below the threshold")
	}
	wantKinds(t, r.kinds(), "wake_pending", "wake_waiting")
	e, _ := r.events.Latest("wake_waiting")
	// written once, when the wait began, so it shows the level at that moment
	if !strings.Contains(e.Message, "15%") || !strings.Contains(e.Message, "9%") {
		t.Errorf("message: %q", e.Message)
	}
	r.setUPS(nut.StateOnline, 15, 7*time.Minute)
	r.tick(time.Minute)
	if len(r.sent) != 1 {
		t.Error("should wake at the threshold")
	}
}

func TestUnknownChargeDoesNotBlock(t *testing.T) {
	r := newRig(t)
	r.setUPS(nut.StateOnline, -1, 5*time.Minute)
	r.pend("kaios")
	r.tick(0)
	if len(r.sent) != 1 {
		t.Error("a UPS that does not report charge must not hold the nodes off")
	}
}

func TestZeroMinBatteryDisablesTheCheck(t *testing.T) {
	r := newRig(t)
	r.w.cfg.Wake.MinBattery = 0
	r.setUPS(nut.StateOnline, 1, 5*time.Minute)
	r.pend("kaios")
	r.tick(0)
	if len(r.sent) != 1 {
		t.Error("check should be off")
	}
}

func TestMaxWaitWakesAnywayWithAWornBattery(t *testing.T) {
	r := newRig(t)
	r.setUPS(nut.StateOnline, 9, 2*time.Hour+time.Minute)
	r.pend("kaios")
	r.tick(0)
	if len(r.sent) != 1 {
		t.Fatal("max_wait should override the battery check")
	}
	e, _ := r.events.Latest("wake_sent")
	if e.Data["override"] != "max_wait" || !strings.Contains(e.Message, "9%") {
		t.Errorf("event: %+v", e)
	}
}

func TestMaxWaitZeroNeverOverrides(t *testing.T) {
	r := newRig(t)
	r.w.cfg.Wake.MaxWait = 0
	r.setUPS(nut.StateOnline, 9, 48*time.Hour)
	r.pend("kaios")
	r.tick(0)
	if len(r.sent) != 0 {
		t.Error("with max_wait off, a low battery holds the nodes")
	}
}

func TestUnreadableUPS(t *testing.T) {
	r := newRig(t)
	r.setUPS(nut.StateUnreachable, -1, 0)
	r.pend("kaios")
	r.tick(10 * time.Second) // upsd may still be starting
	wantKinds(t, r.kinds(), "wake_pending")
	r.tick(time.Minute)
	wantKinds(t, r.kinds(), "wake_pending", "wake_waiting")
	r.tick(time.Minute)
	wantKinds(t, r.kinds(), "wake_pending", "wake_waiting") // once
	if len(r.sent) != 0 {
		t.Fatal("woke without knowing the power state")
	}
	r.tick(2 * time.Hour)
	if len(r.sent) != 1 {
		t.Fatal("after max_wait the nodes should be woken anyway")
	}
	if e, _ := r.events.Latest("wake_sent"); e.Data["override"] != WaitUPSUnreadable {
		t.Errorf("event: %+v", e)
	}
}

func TestOtherUPSStateWaits(t *testing.T) {
	r := newRig(t)
	r.setUPS(nut.StateOther, 100, time.Hour)
	r.pend("kaios")
	r.tick(0)
	if len(r.sent) != 0 || r.w.Status().Waiting != WaitUPSState {
		t.Errorf("status: %+v", r.w.Status())
	}
}

func TestRetriesThenGivesUp(t *testing.T) {
	r := newRig(t)
	r.pend("kaios")
	r.tick(0)
	if len(r.sent) != 1 {
		t.Fatal("first packet")
	}
	r.tick(60 * time.Second) // retry interval is 90s
	if len(r.sent) != 1 {
		t.Fatal("retried too early")
	}
	r.tick(31 * time.Second)
	if len(r.sent) != 2 {
		t.Fatalf("want a retry, have %d packets", len(r.sent))
	}
	for i := 0; i < 20; i++ {
		r.tick(90 * time.Second)
	}
	// 20 minutes after the first packet it stops
	if got := len(r.sent); got < 3 || got > 15 {
		t.Errorf("sent %d packets", got)
	}
	n := len(r.sent)
	r.tick(10 * time.Minute)
	if len(r.sent) != n {
		t.Error("kept sending after giving up")
	}
	kinds := r.kinds()
	if kinds[len(kinds)-1] != "wake_failed" {
		t.Errorf("events = %v", kinds)
	}
	if r.pending() != nil {
		t.Error("a node that was given up on should come off the list")
	}
	if e, _ := r.events.Latest("wake_failed"); e.Level != events.LevelCrit || e.Node != "kaios" {
		t.Errorf("event: %+v", e)
	}
}

func TestComesUpAfterARetry(t *testing.T) {
	r := newRig(t)
	r.pend("kaios")
	r.tick(0)
	r.tick(91 * time.Second)
	r.setNodes(monitor.NodeUp, monitor.NodeDown)
	r.tick(30 * time.Second)
	e, err := r.events.Latest("wake_ok")
	if err != nil {
		t.Fatalf("events = %v", r.kinds())
	}
	if e.Data["packets"] != float64(2) || e.Data["after_s"] != float64(121) {
		t.Errorf("data: %+v", e.Data)
	}
}

func TestSendErrorsAreRetriedAndReported(t *testing.T) {
	r := newRig(t)
	r.sendErr = errors.New("network is unreachable")
	r.pend("kaios")
	r.tick(0)
	if len(r.sent) != 0 {
		t.Fatal("nothing was sent")
	}
	wantKinds(t, r.kinds(), "wake_pending") // no "sent" event for a failed send
	r.sendErr = nil
	r.tick(91 * time.Second)
	if len(r.sent) != 1 {
		t.Fatal("should retry")
	}

	r2 := newRig(t)
	r2.sendErr = errors.New("network is unreachable")
	r2.pend("kaios")
	for i := 0; i < 20; i++ {
		r2.tick(90 * time.Second)
	}
	e, err := r2.events.Latest("wake_failed")
	if err != nil || !strings.Contains(e.Message, "network is unreachable") {
		t.Errorf("event %+v, err %v", e, err)
	}
}

func TestPowerLostAgainInterruptsAndRestarts(t *testing.T) {
	r := newRig(t)
	r.pend("kaios", "node-b")
	r.tick(0)
	if len(r.sent) != 1 {
		t.Fatal("setup")
	}
	r.setUPS(nut.StateOnBattery, 95, time.Second)
	r.tick(10 * time.Second)
	r.tick(10 * time.Minute)
	if len(r.sent) != 1 {
		t.Error("kept sending on battery")
	}
	if r.w.Status().Waiting != WaitOnBattery {
		t.Errorf("status: %+v", r.w.Status())
	}
	if kinds := r.kinds(); kinds[len(kinds)-1] != "wake_waiting" {
		t.Errorf("events = %v", kinds)
	}
	r.setUPS(nut.StateOnline, 95, 2*time.Minute)
	r.tick(time.Second)
	if len(r.sent) != 2 || r.macs()[1] != "aa:bb:cc:dd:ee:01" {
		t.Errorf("should start again from the first node: %v", r.macs())
	}
}

func TestBatteryIsNotRecheckedOnceSendingBegan(t *testing.T) {
	r := newRig(t)
	r.pend("kaios", "node-b")
	r.tick(0)
	r.setUPS(nut.StateOnline, 14, 11*time.Minute) // dips below 15 while charging
	r.tick(6 * time.Second)
	if len(r.sent) != 2 {
		t.Error("the second node should still be woken")
	}
}

func TestNodeMissingFromConfigIsDropped(t *testing.T) {
	r := newRig(t)
	r.pend("ghost")
	r.tick(0)
	wantKinds(t, r.kinds(), "wake_pending", "wake_failed")
	if r.pending() != nil || len(r.sent) != 0 {
		t.Error("ghost should be dropped without sending")
	}
}

func TestStatusProgress(t *testing.T) {
	r := newRig(t)
	r.pend("kaios", "node-b")
	r.tick(0)
	s := r.w.Status()
	if len(s.Nodes) != 2 || s.Nodes[0].State != "sent" || s.Nodes[0].Attempts != 1 || s.Nodes[1].State != "queued" {
		t.Errorf("status: %+v", s)
	}
	if s.Waiting != "" || s.MinBattery != 15 || !s.Active {
		t.Errorf("status: %+v", s)
	}
}

func TestPendingAnnouncedOncePerRun(t *testing.T) {
	r := newRig(t)
	r.setUPS(nut.StateOnBattery, 90, time.Second)
	r.pend("kaios", "node-b")
	for i := 0; i < 5; i++ {
		r.tick(time.Second)
	}
	n := 0
	for _, k := range r.kinds() {
		if k == "wake_pending" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("wake_pending written %d times", n)
	}
}

func TestCorruptListDoesNotCrashOrSend(t *testing.T) {
	r := newRig(t)
	if err := os.WriteFile(filepath.Join(r.dir, "wake.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		r.tick(time.Second)
	}
	if len(r.sent) != 0 {
		t.Error("sent on a corrupt list")
	}
}

func TestWakeNow(t *testing.T) {
	r := newRig(t)
	if err := r.w.WakeNow("node-b"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.macs(), []string{"aa:bb:cc:dd:ee:02"}) {
		t.Errorf("packets: %v", r.macs())
	}
	wantKinds(t, r.kinds(), "wake_manual")
	if err := r.w.WakeNow("nope"); err == nil {
		t.Error("want an error for an unknown node")
	}
	r.sendErr = errors.New("boom")
	if err := r.w.WakeNow("kaios"); err == nil {
		t.Error("want the send error")
	}
}

func TestNewValidates(t *testing.T) {
	cfg := config.Default()
	if _, err := New(&cfg, Deps{}); err == nil {
		t.Error("want an error without a source")
	}
	cfg.Nodes = []config.Node{{Name: "a", Host: "h", MAC: "nonsense"}}
	r := newRig(t)
	if _, err := New(&cfg, Deps{Source: r.src, Events: r.events, State: r.state}); err == nil {
		t.Error("want an error for a bad MAC")
	}
}
