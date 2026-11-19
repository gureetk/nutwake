package main

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/state"
)

// never says no machine answers a ping, so only the nodes' own probes count.
type never struct{}

func (never) Up(context.Context, string) bool { return false }

func TestMain(m *testing.M) {
	secondOpinion = never{}
	os.Exit(m.Run())
}

type daemon struct {
	base string
	stop func()
}

func startDaemon(t *testing.T, r *rig) *daemon {
	t.Helper()
	cfg, err := config.Load(r.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback TCP:", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg, r.cfgPath, ln) }()
	d := &daemon{base: "http://" + ln.Addr().String()}
	d.stop = func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("the daemon stopped with: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("the daemon did not stop")
		}
	}
	t.Cleanup(func() { cancel() })
	return d
}

type apiStatus struct {
	Headline struct{ Title string } `json:"headline"`
	Nodes    []struct {
		Name, State string
	} `json:"nodes"`
	Outages []struct {
		Result string
	} `json:"outages"`
	Wake struct{ Active bool } `json:"wake"`
}

func (d *daemon) status(t *testing.T) apiStatus {
	t.Helper()
	var s apiStatus
	if err := json.Unmarshal([]byte(fetch(t, d.base+"/api/status")), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (d *daemon) waitTitle(t *testing.T, title string) {
	t.Helper()
	var last string
	eventually(t, 30*time.Second, "the page to say "+title+" (last said "+last+")", func() bool {
		last = d.status(t).Headline.Title
		return last == title
	})
}

func eventKinds(t *testing.T, r *rig) []string {
	t.Helper()
	cfg, _ := config.Load(r.cfgPath)
	st, err := events.Open(cfg.EventsFile(), nil)
	if err != nil {
		t.Fatal(err)
	}
	all, err := st.All()
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range all {
		kinds = append(kinds, e.Kind)
	}
	return kinds
}

func has(kinds []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, k := range kinds {
			if k == w {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// TestPowerCutFromStartToFinish plays the whole story against a fake UPS and
// fake nodes: the power goes, the helper waits for the nodes and halts the Pi,
// the Pi boots when power is back, and only then are the nodes woken.
func TestPowerCutFromStartToFinish(t *testing.T) {
	nutd := startFakeNUT(t)
	r := newRig(t, nutd.Addr(), "a", "b")

	// packets that reach the wake address, by the last byte of the MAC
	packets := make(chan byte, 8)
	go func() {
		buf := make([]byte, 256)
		for {
			n, _, err := r.udp.ReadFrom(buf)
			if err != nil {
				return
			}
			if n == 102 {
				packets <- buf[n-1]
			}
		}
	}()

	// 1. all is well
	d1 := startDaemon(t, r)
	d1.waitTitle(t, "Power is on")
	eventually(t, 15*time.Second, "both nodes to be seen up", func() bool {
		s := d1.status(t)
		return len(s.Nodes) == 2 && s.Nodes[0].State == "up" && s.Nodes[1].State == "up"
	})

	// 2. the power goes
	nutd.SetStatus("OB DISCHRG")
	d1.waitTitle(t, "Running on battery")

	// 3. upsmon runs the helper; the nodes shut down on their own
	var out, errOut syncBuf
	helper := make(chan int, 1)
	go func() { helper <- run(context.Background(), []string{"shutdown", "-config", r.cfgPath}, &out, &errOut) }()
	eventually(t, 10*time.Second, "the helper to write the wake list", func() bool {
		p, _ := state.New(filepath.Join(r.dir, "data")).LoadPending()
		return p != nil && len(p.Nodes) == 2
	})
	if fileExists(r.halted) {
		t.Fatal("the helper halted before the nodes were off")
	}
	r.nodes["a"].down()
	r.nodes["b"].down()
	select {
	case code := <-helper:
		if code != 0 {
			t.Fatalf("the helper exited %d\n%s", code, errOut.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("the helper did not finish\n%s", errOut.String())
	}
	if !fileExists(r.halted) {
		t.Fatal("the Pi was not halted")
	}

	// 4. the Pi halts: the daemon stops with it
	d1.stop()
	kinds := eventKinds(t, r)
	if !has(kinds, "daemon_start", "power_lost", "shutdown_began", "nodes_off", "pi_halting", "daemon_stop") {
		t.Fatalf("the history after the halt is %v", kinds)
	}
	select {
	case b := <-packets:
		t.Fatalf("a wake packet (for %d) went out while the power was still out", b)
	default:
	}

	// 5. power is back and the Pi boots: the nodes are woken, once
	nutd.SetStatus("OL CHRG")
	d2 := startDaemon(t, r)
	got := map[byte]bool{}
	deadline := time.After(40 * time.Second)
	for len(got) < 2 {
		select {
		case b := <-packets:
			got[b] = true
		case <-deadline:
			t.Fatalf("only these nodes were sent a wake packet: %v\nhistory: %v", got, eventKinds(t, r))
		}
	}
	if !got[1] || !got[2] {
		t.Fatalf("packets went to %v, want both nodes", got)
	}
	if !d2.status(t).Wake.Active && !has(eventKinds(t, r), "wake_sent") {
		t.Fatal("the daemon sent packets without saying so")
	}

	// 6. the nodes answer: the list is cleared and the page tells the story
	r.nodes["a"].up(t)
	r.nodes["b"].up(t)
	eventually(t, 30*time.Second, "both nodes to be recorded as woken", func() bool {
		return has(eventKinds(t, r), "wake_ok")
	})
	eventually(t, 30*time.Second, "the wake list to be cleared", func() bool {
		p, _ := state.New(filepath.Join(r.dir, "data")).LoadPending()
		return p == nil || len(p.Nodes) == 0
	})
	d2.waitTitle(t, "Power is on")
	s := d2.status(t)
	if len(s.Outages) == 0 || !strings.Contains(s.Outages[0].Result, "woke again") {
		t.Fatalf("the outage table says %+v", s.Outages)
	}
	d2.stop()
}
