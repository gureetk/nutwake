package shutdown

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/probe"
	"github.com/gureetk/nutwake/internal/state"
)

// fakeNode answers probes until its off time, by the rig's fake clock.
type fakeNode struct {
	r     *rig
	offAt time.Time // zero: never goes off
	// blip makes the node miss one probe at the given time
	blipAt time.Time
}

func (n *fakeNode) Up(context.Context, string) bool {
	now := n.r.now()
	if !n.blipAt.IsZero() && now.Equal(n.blipAt) {
		return false
	}
	return n.offAt.IsZero() || now.Before(n.offAt)
}

type rig struct {
	t      *testing.T
	h      *Helper
	cfg    config.Config
	dir    string
	events *events.Store
	state  *state.Store
	clock  time.Time
	nodes  map[string]*fakeNode
	icmp   map[string]*fakeNode
	ran    [][]string
	runErr error
	// whenSleeping is called each time the helper sleeps
	whenSleeping func()
}

func (r *rig) now() time.Time { return r.clock }

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{t: t, clock: time.Date(2026, 11, 13, 21, 0, 0, 0, time.UTC), nodes: map[string]*fakeNode{}, icmp: map[string]*fakeNode{}}
	r.dir = t.TempDir()
	r.cfg = config.Default()
	r.cfg.DataDir = r.dir
	r.cfg.Nodes = []config.Node{
		{Name: "kaios", Host: "10.0.0.10", MAC: "aa:bb:cc:dd:ee:01"},
		{Name: "node-b", Host: "10.0.0.11", MAC: "aa:bb:cc:dd:ee:02"},
	}
	r.cfg.Shutdown.Command = []string{"/sbin/shutdown", "-h", "+0"}
	for _, n := range r.cfg.Nodes {
		r.nodes[n.Name] = &fakeNode{r: r}
		r.icmp[n.Name] = &fakeNode{r: r}
	}
	var err error
	if r.events, err = events.Open(filepath.Join(r.dir, "events.jsonl"), nil); err != nil {
		t.Fatal(err)
	}
	r.state = state.New(r.dir)
	r.build()
	return r
}

type byHost struct {
	r     *rig
	nodes map[string]*fakeNode
}

func (b byHost) Up(ctx context.Context, host string) bool {
	for _, n := range b.r.cfg.Nodes {
		if n.Host == host {
			return b.nodes[n.Name].Up(ctx, host)
		}
	}
	return false
}

func (r *rig) build() {
	probers := map[string]probe.Prober{}
	for name, n := range r.nodes {
		probers[name] = n
	}
	h, err := New(&r.cfg, Deps{
		Events: r.events, State: r.state, Probers: probers, ICMP: byHost{r, r.icmp},
		Now: r.now,
		Run: func(_ context.Context, name string, args ...string) error {
			r.ran = append(r.ran, append([]string{name}, args...))
			return r.runErr
		},
		Sleep: func(_ context.Context, d time.Duration) {
			r.clock = r.clock.Add(d)
			if r.whenSleeping != nil {
				r.whenSleeping()
			}
		},
		LookPath: func(s string) (string, error) {
			if strings.HasPrefix(s, "/sbin/") {
				return s, nil
			}
			return "", errors.New("not found")
		},
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.h = h
}

// offAfter makes both the node's probe and its ping stop answering d from now.
func (r *rig) offAfter(name string, d time.Duration) {
	r.nodes[name].offAt = r.clock.Add(d)
	r.icmp[name].offAt = r.clock.Add(d)
}

// alreadyOff makes a node be off before the helper starts.
func (r *rig) alreadyOff(name string) {
	r.nodes[name].offAt = r.clock.Add(-time.Hour)
	r.icmp[name].offAt = r.clock.Add(-time.Hour)
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

func (r *rig) saveOutage(nodesUp ...string) {
	r.t.Helper()
	if err := r.state.SaveOutage(&state.Outage{Started: r.clock.Add(-time.Hour), StartCharge: 100, MinCharge: 10, NodesUp: nodesUp}); err != nil {
		r.t.Fatal(err)
	}
}

func wantKinds(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestWaitsForNodesThenHalts(t *testing.T) {
	r := newRig(t)
	r.saveOutage("kaios", "node-b")
	r.offAfter("kaios", 20*time.Second)
	r.offAfter("node-b", 70*time.Second)

	if err := r.h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantKinds(t, r.kinds(), "shutdown_began", "nodes_off", "pi_halting")
	if len(r.ran) != 1 || strings.Join(r.ran[0], " ") != "/sbin/shutdown -h +0" {
		t.Errorf("ran %v", r.ran)
	}
	if got := r.pending(); !slices.Equal(got, []string{"kaios", "node-b"}) {
		t.Errorf("wake list = %v", got)
	}
	e, _ := r.events.Latest("nodes_off")
	if w := e.Data["waited_s"].(float64); w < 70 || w > 76 {
		t.Errorf("waited_s = %v, want a little over 70", w)
	}
	if !strings.Contains(e.Message, "kaios, node-b") {
		t.Errorf("message: %q", e.Message)
	}
}

func TestWakeListIsWrittenBeforeWaiting(t *testing.T) {
	r := newRig(t)
	r.alreadyOff("node-b")
	r.saveOutage("kaios")
	r.offAfter("kaios", time.Minute)
	checked := false
	r.whenSleeping = func() {
		if !checked {
			checked = true
			if got := r.pending(); !slices.Equal(got, []string{"kaios"}) {
				t.Errorf("the wake list must exist before the wait starts, have %v", got)
			}
		}
	}
	if err := r.h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !checked {
		t.Fatal("never waited")
	}
}

func TestGivesUpWaitingAndStillHalts(t *testing.T) {
	r := newRig(t)
	r.saveOutage("kaios", "node-b")
	r.offAfter("kaios", 10*time.Second)
	// node-b never goes off
	if err := r.h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantKinds(t, r.kinds(), "shutdown_began", "nodes_still_up", "pi_halting")
	e, _ := r.events.Latest("nodes_still_up")
	if e.Level != events.LevelCrit || !strings.Contains(e.Message, "node-b") || strings.Contains(e.Message, "kaios") {
		t.Errorf("event: %+v", e)
	}
	if len(r.ran) != 1 {
		t.Error("the Pi must halt even when a node will not go off")
	}
	// a node that stayed up is still on the list: the waker skips it if it is up
	if got := r.pending(); !slices.Equal(got, []string{"kaios", "node-b"}) {
		t.Errorf("wake list = %v", got)
	}
	if took := r.clock.Sub(time.Date(2026, 11, 13, 21, 0, 0, 0, time.UTC)); took != 5*time.Minute {
		t.Errorf("waited %v, want exactly wait_for_nodes", took)
	}
}

func TestOnlyNodesFromThePowerCutAreRecorded(t *testing.T) {
	r := newRig(t)
	// node-b was already off before the power cut, and stays off
	r.alreadyOff("node-b")
	r.saveOutage("kaios")
	r.offAfter("kaios", 5*time.Second)
	if err := r.h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.pending(); !slices.Equal(got, []string{"kaios"}) {
		t.Errorf("wake list = %v, want only kaios", got)
	}
}

func TestNodesAlreadyOffWhenTheHelperStartsAreStillRecorded(t *testing.T) {
	r := newRig(t)
	// their own upsmon shut them down before the Pi's helper ran
	for _, n := range []string{"kaios", "node-b"} {
		r.nodes[n].offAt = r.clock.Add(-time.Minute)
		r.icmp[n].offAt = r.clock.Add(-time.Minute)
	}
	r.saveOutage("kaios", "node-b")
	if err := r.h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.pending(); !slices.Equal(got, []string{"kaios", "node-b"}) {
		t.Errorf("wake list = %v", got)
	}
	wantKinds(t, r.kinds(), "shutdown_began", "nodes_off", "pi_halting")
	if took := r.clock.Sub(time.Date(2026, 11, 13, 21, 0, 0, 0, time.UTC)); took != 0 {
		t.Errorf("waited %v for nodes that were already off", took)
	}
}

func TestWithoutAnOutageRecordUsesNodesThatAreUp(t *testing.T) {
	r := newRig(t)
	r.offAfter("kaios", 10*time.Second)
	r.alreadyOff("node-b")
	if err := r.h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := r.pending(); !slices.Equal(got, []string{"kaios"}) {
		t.Errorf("wake list = %v", got)
	}
	e, _ := r.events.Latest("shutdown_began")
	if e.Data["outage_record"] != false {
		t.Errorf("data: %+v", e.Data)
	}
}

func TestKeepsNodesFromAnEarlierShutdown(t *testing.T) {
	r := newRig(t)
	since := time.Date(2026, 11, 13, 18, 0, 0, 0, time.UTC)
	if err := r.state.SavePending(&state.Pending{Since: since, Nodes: []string{"node-b"}, Reason: "power cut"}); err != nil {
		t.Fatal(err)
	}
	r.nodes["node-b"].offAt = r.clock.Add(-time.Hour) // still waiting to be woken
	r.icmp["node-b"].offAt = r.clock.Add(-time.Hour)
	r.saveOutage("kaios")
	r.offAfter("kaios", 5*time.Second)
	if err := r.h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, _ := r.state.LoadPending()
	if p == nil || !slices.Equal(p.Nodes, []string{"node-b", "kaios"}) || !p.Since.Equal(since) {
		t.Errorf("wake list = %+v", p)
	}
}

func TestOneMissedProbeIsNotPoweredOff(t *testing.T) {
	r := newRig(t)
	r.alreadyOff("node-b")
	r.saveOutage("kaios")
	r.nodes["kaios"].blipAt = r.clock.Add(2 * time.Second) // first probe misses
	r.icmp["kaios"].blipAt = r.clock.Add(2 * time.Second)
	r.offAfter("kaios", 30*time.Second)
	if err := r.h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	e, _ := r.events.Latest("nodes_off")
	if w := e.Data["waited_s"].(float64); w < 30 {
		t.Errorf("waited %v s: a single missed probe was taken for power-off", w)
	}
}

func TestAPingAnswerKeepsTheWaitGoing(t *testing.T) {
	r := newRig(t)
	r.alreadyOff("node-b")
	r.saveOutage("kaios")
	// the Proxmox web port is closed early, but the machine still answers ping
	r.nodes["kaios"].offAt = r.clock.Add(10 * time.Second)
	r.icmp["kaios"].offAt = r.clock.Add(90 * time.Second)
	if err := r.h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	e, _ := r.events.Latest("nodes_off")
	if w := e.Data["waited_s"].(float64); w < 90 {
		t.Errorf("waited %v s, but the node answered ping until 90 s", w)
	}
}

func TestZeroWaitDoesNotWait(t *testing.T) {
	r := newRig(t)
	r.cfg.Shutdown.WaitForNodes = 0
	r.saveOutage("kaios", "node-b")
	if err := r.h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantKinds(t, r.kinds(), "shutdown_began", "pi_halting")
	if len(r.ran) != 1 {
		t.Error("should halt straight away")
	}
	if got := r.pending(); !slices.Equal(got, []string{"kaios", "node-b"}) {
		t.Errorf("wake list = %v", got)
	}
}

func TestHaltFailureIsReturnedAfterRecording(t *testing.T) {
	r := newRig(t)
	r.runErr = errors.New("permission denied")
	r.saveOutage()
	err := r.h.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v", err)
	}
	if got := r.kinds(); got[len(got)-1] != "pi_halting" {
		t.Errorf("events = %v", got)
	}
}

func TestStillHaltsWithoutAHistoryOrWritableState(t *testing.T) {
	r := newRig(t)
	r.events = nil
	r.saveOutage("kaios")
	r.offAfter("kaios", 5*time.Second)
	r.build()
	// the data directory disappears: nothing can be written any more
	if err := os.RemoveAll(r.dir); err != nil {
		t.Fatal(err)
	}
	if err := r.h.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.ran) != 1 {
		t.Error("a failure to write must never stop the Pi halting")
	}
}

func TestCancelledShutdownDoesNotHalt(t *testing.T) {
	r := newRig(t)
	r.saveOutage("kaios")
	ctx, cancel := context.WithCancel(context.Background())
	r.whenSleeping = cancel
	err := r.h.Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if len(r.ran) != 0 {
		t.Error("an abandoned shutdown must not halt the Pi")
	}
}

func TestPlanWritesNothing(t *testing.T) {
	r := newRig(t)
	r.saveOutage("kaios")
	r.alreadyOff("node-b")
	before := r.clock
	p := r.h.Plan(context.Background())
	if !slices.Equal(p.Up, []string{"kaios"}) || !slices.Equal(p.Record, []string{"kaios"}) || !p.OutageRecord {
		t.Errorf("plan: %+v", p)
	}
	if p.WaitFor != 5*time.Minute || strings.Join(p.Command, " ") != "/sbin/shutdown -h +0" {
		t.Errorf("plan: %+v", p)
	}
	if r.pending() != nil || len(r.kinds()) != 0 || len(r.ran) != 0 || !r.clock.Equal(before) {
		t.Error("Plan must not write, run or wait")
	}
}

func TestPlanIgnoresUnknownNodesInTheOutageRecord(t *testing.T) {
	r := newRig(t)
	r.saveOutage("ghost")
	r.nodes["kaios"].offAt = r.clock.Add(-time.Hour)
	r.icmp["kaios"].offAt = r.clock.Add(-time.Hour)
	r.alreadyOff("node-b")
	if p := r.h.Plan(context.Background()); len(p.Record) != 0 {
		t.Errorf("record = %v", p.Record)
	}
}

func TestCheckReportsProblems(t *testing.T) {
	r := newRig(t)
	r.alreadyOff("node-b")
	_, rs := r.h.Check(context.Background())
	by := map[string]Result{}
	for _, x := range rs {
		by[x.Name] = x
	}
	for name, want := range map[string]Level{
		"data directory": OK, "halt command": OK, "node kaios": OK, "node node-b": Warn, "wait time": OK,
	} {
		if by[name].Level != want {
			t.Errorf("%s: %+v, want %s", name, by[name], want)
		}
	}

	r.cfg.DataDir = filepath.Join(r.dir, "missing")
	r.cfg.Shutdown.Command = []string{"/usr/bin/nothing"}
	r.cfg.Shutdown.WaitForNodes = 0
	r.build()
	_, rs = r.h.Check(context.Background())
	by = map[string]Result{}
	for _, x := range rs {
		by[x.Name] = x
	}
	for name, want := range map[string]Level{"data directory": Fail, "halt command": Fail, "wait time": Warn} {
		if by[name].Level != want {
			t.Errorf("%s: %+v, want %s", name, by[name], want)
		}
	}
}

func TestNewValidates(t *testing.T) {
	cfg := config.Default()
	if _, err := New(&cfg, Deps{}); err == nil {
		t.Error("want an error without a state store")
	}
}

func TestConcurrentProbingIsRaceFree(t *testing.T) {
	r := newRig(t)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.h.Plan(context.Background()) }()
	}
	wg.Wait()
}
