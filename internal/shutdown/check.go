package shutdown

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gureetk/nutwake/internal/probe"
)

// Level is how a check turned out.
type Level string

const (
	OK   Level = "ok"
	Warn Level = "warn"
	Fail Level = "fail"
)

// Result is one line of a preflight report.
type Result struct {
	Name   string `json:"name"`
	Level  Level  `json:"level"`
	Detail string `json:"detail"`
}

// Check tests, without changing anything, everything the shutdown depends on.
// `nutwake shutdown --dry-run` prints it.
func (h *Helper) Check(ctx context.Context) (Plan, []Result) {
	var rs []Result
	add := func(name string, l Level, format string, a ...any) {
		rs = append(rs, Result{Name: name, Level: l, Detail: fmt.Sprintf(format, a...)})
	}

	if err := dataDirWritable(h.cfg.DataDir); err != nil {
		add("data directory", Fail, "%s cannot be written: %v. The wake list could not be saved.", h.cfg.DataDir, err)
	} else {
		add("data directory", OK, "%s is writable", h.cfg.DataDir)
	}

	cmd := h.cfg.Shutdown.Command
	if len(cmd) == 0 {
		add("halt command", Fail, "shutdown.command is empty")
	} else if path, err := h.look(cmd[0]); err != nil {
		add("halt command", Fail, "%q was not found", cmd[0])
	} else {
		add("halt command", OK, "%s", strings.Join(append([]string{path}, cmd[1:]...), " "))
	}

	if ic, ok := h.icmp.(probe.ICMP); ok {
		if err := ic.Check(); err != nil {
			add("ping", Warn, "%v. Without it a node's own probe alone decides when it is off.", err)
		} else {
			add("ping", OK, "the ping command is installed")
		}
	}

	plan := h.Plan(ctx)
	for _, n := range h.cfg.Nodes {
		if slices.Contains(plan.Up, n.Name) {
			add("node "+n.Name, OK, "answering now (%s), so the shutdown would wait for it", h.cfg.ProbeSpec(n))
		} else {
			add("node "+n.Name, Warn, "not answering (%s). Fine if it is meant to be off; otherwise check the address and the probe", h.cfg.ProbeSpec(n))
		}
	}
	if w := h.cfg.Shutdown.WaitForNodes; w > 0 {
		add("wait time", OK, "up to %s. It must fit in the UPS runtime after the low-battery signal (often 5 min)", w.Round(time.Second))
	} else {
		add("wait time", Warn, "shutdown.wait_for_nodes is 0, so the Pi will not wait for the nodes")
	}
	return plan, rs
}
