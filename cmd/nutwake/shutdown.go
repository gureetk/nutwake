package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/probe"
	"github.com/gureetk/nutwake/internal/shutdown"
	"github.com/gureetk/nutwake/internal/state"
)

// haltFallback halts the machine when the configuration cannot say how. It is a
// variable so that tests do not halt the machine they run on.
var haltFallback = config.Default().Shutdown.Command

// secondOpinion is how the helper double-checks that a node is off. Nil means
// a plain ping. Tests replace it, because every address on loopback answers.
var secondOpinion probe.Prober

// isRoot says whether this process runs as root. A variable so tests can say.
var isRoot = func() bool { return os.Geteuid() == 0 }

func cmdShutdown(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs, path := flags("shutdown", stderr)
	dry := fs.Bool("dry-run", false, "show what would happen and change nothing")
	if ok, code := parse(fs, args); !ok {
		return code
	}
	cfg, err := config.Load(*path)
	if err != nil {
		if *dry {
			fmt.Fprintf(stderr, "nutwake: %v\n", err)
			return 1
		}
		// A broken configuration must not keep the Pi running: the UPS has
		// asked for a shutdown, and its battery is what is left.
		fmt.Fprintf(stderr, "nutwake: cannot use the configuration (%v).\nHalting with the default command anyway: %s\n",
			err, strings.Join(haltFallback, " "))
		return halt(ctx, haltFallback, stderr)
	}

	// The helper runs as root and runs the command in the configuration, so
	// that file must not be something another user could have changed.
	if isRoot() && !*dry {
		if why := untrusted(*path); why != "" {
			fmt.Fprintf(stderr, "nutwake: not using shutdown.command from %s: %s.\nHalting with the default command instead: %s\n",
				*path, why, strings.Join(haltFallback, " "))
			cfg.Shutdown.Command = haltFallback
		}
	}

	var ev *events.Store
	if !*dry {
		// no clock gate: the history is written now, because the Pi is about to stop
		if ev, err = events.Open(cfg.EventsFile(), nil); err != nil {
			fmt.Fprintf(stderr, "nutwake: cannot write the history (%v). Shutting down anyway.\n", err)
			ev = nil
		}
	}
	h, err := shutdown.New(cfg, shutdown.Deps{Events: ev, State: state.New(cfg.DataDir), ICMP: secondOpinion})
	if err != nil {
		fmt.Fprintf(stderr, "nutwake: %v\n", err)
		if *dry {
			return 1
		}
		return halt(ctx, cfg.Shutdown.Command, stderr)
	}

	if *dry {
		return dryRun(ctx, h, stdout)
	}
	if err := h.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "nutwake: %v\n", err)
		if ctx.Err() != nil {
			return 1 // asked to stop: do not halt behind the caller's back
		}
		if !slices.Equal(cfg.Shutdown.Command, haltFallback) {
			fmt.Fprintf(stderr, "Trying the default halt command: %s\n", strings.Join(haltFallback, " "))
			return halt(ctx, haltFallback, stderr)
		}
		return 1
	}
	return 0
}

// halt runs a halt command and reports whether it worked.
func halt(ctx context.Context, cmd []string, stderr io.Writer) int {
	if len(cmd) == 0 {
		fmt.Fprintln(stderr, "nutwake: there is no command to halt with")
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, cmd[0], cmd[1:]...).CombinedOutput(); err != nil {
		fmt.Fprintf(stderr, "nutwake: %s failed: %v: %s\n", strings.Join(cmd, " "), err, strings.TrimSpace(string(out)))
		return 1
	}
	return 0
}

func dryRun(ctx context.Context, h *shutdown.Helper, w io.Writer) int {
	plan, results := h.Check(ctx)
	fmt.Fprintln(w, "nutwake shutdown --dry-run")
	fmt.Fprintln(w)
	failed := printResults(w, results)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "If power ran low now, the helper would:")
	switch {
	case len(plan.Up) > 0 && plan.WaitFor > 0:
		fmt.Fprintf(w, "  1. wait up to %s for %s to power off\n", plan.WaitFor.Round(time.Second), strings.Join(plan.Up, ", "))
	case len(plan.Up) > 0:
		fmt.Fprintf(w, "  1. not wait for %s, because shutdown.wait_for_nodes is 0\n", strings.Join(plan.Up, ", "))
	default:
		fmt.Fprintln(w, "  1. find every node already off, so not wait")
	}
	if len(plan.Record) > 0 {
		note := ""
		if !plan.OutageRecord {
			note = " (the daemon has no record of a power cut, so these are the nodes answering now)"
		}
		fmt.Fprintf(w, "  2. write down %s, to be woken when power is back%s\n", strings.Join(plan.Record, ", "), note)
	} else {
		fmt.Fprintln(w, "  2. have no node to wake afterwards")
	}
	fmt.Fprintf(w, "  3. run: %s\n", strings.Join(plan.Command, " "))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Nothing was changed.")
	if failed {
		return 1
	}
	return 0
}

// printResults prints a report and says whether any line failed.
func printResults(w io.Writer, rs []shutdown.Result) (failed bool) {
	width := 0
	for _, r := range rs {
		width = max(width, len(r.Name))
	}
	for _, r := range rs {
		tag := "ok  "
		switch r.Level {
		case shutdown.Warn:
			tag = "warn"
		case shutdown.Fail:
			tag, failed = "FAIL", true
		}
		fmt.Fprintf(w, "  %s  %-*s  %s\n", tag, width, r.Name, r.Detail)
	}
	return failed
}
