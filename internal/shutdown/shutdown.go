// Package shutdown is what upsmon runs when the UPS says the battery is
// nearly empty and this Pi must go down.
//
// The nodes shut themselves down through their own NUT clients, but upsmon
// on the Pi does not wait for them: the Pi would halt, and the UPS cut its
// output, while the nodes were still stopping their virtual machines. The
// helper waits until the nodes have powered off first, then halts the Pi.
//
// Before it waits it writes wake.json, the list of nodes to bring back after
// power returns. That way the list survives even if the battery gives out
// during the wait.
//
// Nothing the helper does to record things may stop the Pi from halting: every
// failure to write a file is logged and the shutdown goes on.
package shutdown

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/humanize"
	"github.com/gureetk/nutwake/internal/probe"
	"github.com/gureetk/nutwake/internal/state"
)

// Deps are the things a Helper works with. Everything has a default except
// State; Events may be nil, in which case nothing is written to the history.
type Deps struct {
	Events *events.Store
	State  *state.Store
	// Probers are keyed by node name; missing ones are built from the config.
	Probers map[string]probe.Prober
	// ICMP is the second opinion on whether a node is off.
	ICMP probe.Prober
	// Run executes the halt command.
	Run func(ctx context.Context, name string, args ...string) error
	// LookPath finds the halt command, for Check.
	LookPath func(string) (string, error)
	Now      func() time.Time
	// Sleep waits for d or until ctx is done.
	Sleep func(ctx context.Context, d time.Duration)
}

// Helper shuts the Pi down once the nodes are off.
type Helper struct {
	cfg     *config.Config
	events  *events.Store
	store   *state.Store
	probers map[string]probe.Prober
	icmp    probe.Prober
	run     func(ctx context.Context, name string, args ...string) error
	look    func(string) (string, error)
	now     func() time.Time
	sleep   func(ctx context.Context, d time.Duration)

	// interval is how often the nodes are probed while waiting.
	interval time.Duration
}

// New builds a Helper.
func New(cfg *config.Config, d Deps) (*Helper, error) {
	if d.State == nil {
		return nil, errors.New("shutdown: State is required")
	}
	h := &Helper{
		cfg: cfg, events: d.Events, store: d.State, probers: map[string]probe.Prober{},
		icmp: d.ICMP, run: d.Run, look: d.LookPath, now: d.Now, sleep: d.Sleep, interval: 2 * time.Second,
	}
	if h.icmp == nil {
		h.icmp = probe.ICMP{Timeout: cfg.Probe.Timeout}
	}
	if h.run == nil {
		h.run = func(ctx context.Context, name string, args ...string) error {
			out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
			if err != nil {
				return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	if h.look == nil {
		h.look = exec.LookPath
	}
	if h.now == nil {
		h.now = time.Now
	}
	if h.sleep == nil {
		h.sleep = func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		}
	}
	for _, n := range cfg.Nodes {
		p := d.Probers[n.Name]
		if p == nil {
			var err error
			if p, err = probe.Parse(cfg.ProbeSpec(n), cfg.Probe.Timeout); err != nil {
				return nil, fmt.Errorf("node %s: %w", n.Name, err)
			}
		}
		h.probers[n.Name] = p
	}
	return h, nil
}

// Plan is what a shutdown would do right now.
type Plan struct {
	// Up are the nodes answering now. The helper waits for them to go off.
	Up []string
	// Record are the nodes that will be written to wake.json: those that were
	// up when power was lost, and those up now.
	Record []string
	// OutageRecord is false when the daemon had no record of the power cut,
	// so only the nodes up right now are known.
	OutageRecord bool
	WaitFor      time.Duration
	Command      []string
}

// Plan works out what Run would do, writing nothing.
func (h *Helper) Plan(ctx context.Context) Plan {
	p := Plan{
		Up: []string{}, Record: []string{},
		WaitFor: h.cfg.Shutdown.WaitForNodes, Command: h.cfg.Shutdown.Command,
	}
	up := h.probeAll(ctx, h.names())
	for _, n := range h.cfg.Nodes {
		if up[n.Name] {
			p.Up = append(p.Up, n.Name)
		}
	}
	record := map[string]bool{}
	for _, n := range p.Up {
		record[n] = true
	}
	switch o, err := h.store.LoadOutage(); {
	case err != nil:
		slog.Warn("shutdown: cannot read the outage record", "err", err)
	case o != nil:
		p.OutageRecord = true
		for _, n := range o.NodesUp {
			if _, ok := h.probers[n]; ok {
				record[n] = true
			}
		}
	}
	for _, n := range h.cfg.Nodes { // config order
		if record[n.Name] {
			p.Record = append(p.Record, n.Name)
		}
	}
	return p
}

// Run waits for the nodes to power off and then halts the Pi. It returns an
// error only for things the caller should see; failing to write a file is
// not one of them. If ctx is cancelled the shutdown is abandoned and the Pi
// is not halted.
func (h *Helper) Run(ctx context.Context) error {
	start := h.now()
	plan := h.Plan(ctx)
	h.record(plan, start)

	var waited waitResult
	if plan.WaitFor > 0 {
		waited = h.wait(ctx, plan, start)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("shutdown abandoned: %w", ctx.Err())
	}
	left := waited.still
	switch {
	case len(plan.Up) == 0:
		h.note(events.LevelInfo, events.KindNodesOff, "The nodes were already off.", nil)
	case plan.WaitFor <= 0:
		// shutdown.wait_for_nodes is 0: the person asked not to wait
	case len(left) == 0:
		h.note(events.LevelOK, events.KindNodesOff,
			fmt.Sprintf("%s powered off after %s.", strings.Join(plan.Up, ", "), humanize.Duration(waited.took)),
			map[string]any{"nodes": plan.Up, "waited_s": int(waited.took.Seconds())})
	default:
		h.note(events.LevelCrit, events.KindNodesStuck,
			fmt.Sprintf("Gave up waiting after %s. Still up: %s.", humanize.Duration(waited.took), strings.Join(left, ", ")),
			map[string]any{"nodes": left, "waited_s": int(waited.took.Seconds())})
	}

	h.note(events.LevelWarn, events.KindPiHalting, "Halting this Pi now.", map[string]any{"command": plan.Command})
	if h.events != nil {
		h.events.FlushNow()
	}
	if len(plan.Command) == 0 {
		return errors.New("shutdown.command is empty")
	}
	if err := h.run(ctx, plan.Command[0], plan.Command[1:]...); err != nil {
		return fmt.Errorf("halt command %q failed: %w", strings.Join(plan.Command, " "), err)
	}
	return nil
}

// record writes the list of nodes to wake and says the shutdown began. Both
// are best effort.
func (h *Helper) record(plan Plan, start time.Time) {
	if len(plan.Record) > 0 {
		p, err := h.store.LoadPending()
		if err != nil {
			slog.Warn("shutdown: ignoring an unreadable wake list", "err", err)
		}
		if p == nil {
			p = &state.Pending{Since: start.Round(0), Reason: "power cut"}
		}
		p.Add(plan.Record...)
		if err := h.store.SavePending(p); err != nil {
			slog.Error("shutdown: cannot write the wake list: the nodes will not be woken automatically", "err", err)
		}
	}
	msg := "Shutting down this Pi."
	switch {
	case len(plan.Up) > 0 && plan.WaitFor > 0:
		msg = fmt.Sprintf("Shutting down this Pi. Waiting up to %s for %s to power off.",
			humanize.Duration(plan.WaitFor), strings.Join(plan.Up, ", "))
	case len(plan.Up) > 0:
		msg = fmt.Sprintf("Shutting down this Pi without waiting for %s, which still answers.", strings.Join(plan.Up, ", "))
	}
	h.note(events.LevelWarn, events.KindShutdownBegan, msg, map[string]any{
		"nodes_up": plan.Up, "to_wake": plan.Record, "outage_record": plan.OutageRecord,
	})
}

type waitResult struct {
	took  time.Duration
	still []string
}

// wait polls until every node in plan.Up is off, or the time runs out. A node
// is off after probe.down_after failed probes in a row.
func (h *Helper) wait(ctx context.Context, plan Plan, start time.Time) waitResult {
	remaining := slices.Clone(plan.Up)
	fails := map[string]int{}
	deadline := start.Add(plan.WaitFor)
	for len(remaining) > 0 && h.now().Before(deadline) && ctx.Err() == nil {
		h.sleep(ctx, min(h.interval, deadline.Sub(h.now())))
		if ctx.Err() != nil {
			break
		}
		up := h.probeAll(ctx, remaining)
		next := remaining[:0:0]
		for _, name := range remaining {
			if up[name] {
				fails[name] = 0
				next = append(next, name)
				continue
			}
			if fails[name]++; fails[name] < h.cfg.Probe.DownAfter {
				next = append(next, name)
			}
		}
		remaining = next
	}
	return waitResult{took: h.now().Sub(start), still: remaining}
}

// probeAll reports which of names answer. A node counts as up if its own
// probe or a plain ping gets an answer, so "off" means nothing answers at all.
func (h *Helper) probeAll(ctx context.Context, names []string) map[string]bool {
	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = make(map[string]bool, len(names))
	)
	for _, name := range names {
		node, ok := h.node(name)
		if !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			up := h.probers[name].Up(ctx, node.Host) || h.icmp.Up(ctx, node.Host)
			mu.Lock()
			out[name] = up
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func (h *Helper) node(name string) (config.Node, bool) {
	for _, n := range h.cfg.Nodes {
		if n.Name == name {
			return n, true
		}
	}
	return config.Node{}, false
}

func (h *Helper) names() []string {
	out := make([]string, len(h.cfg.Nodes))
	for i, n := range h.cfg.Nodes {
		out[i] = n.Name
	}
	return out
}

// note writes to the history if there is one.
func (h *Helper) note(level events.Level, kind, msg string, data map[string]any) {
	if h.events == nil {
		return
	}
	h.events.AddEvent(events.Event{Time: h.now(), Kind: kind, Level: level, Message: msg, Data: data})
}

// dataDirWritable reports whether files can be created in dir.
func dataDirWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".nutwake-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}
