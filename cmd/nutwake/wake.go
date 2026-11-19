package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/monitor"
	"github.com/gureetk/nutwake/internal/state"
	"github.com/gureetk/nutwake/internal/waker"
)

// idle stands in for the monitor: a manual wake does not look at the UPS.
type idle struct{}

func (idle) Brief() monitor.Brief { return monitor.Brief{} }

func cmdWake(_ context.Context, args []string, stdout, stderr io.Writer) int {
	fs, path := flags("wake", stderr)
	all := fs.Bool("all", false, "wake every node in the configuration")
	if ok, code := parse(fs, args); !ok {
		return code
	}
	cfg, ok := load(*path, stderr)
	if !ok {
		return 1
	}
	names := fs.Args()
	if *all {
		for _, n := range cfg.Nodes {
			names = append(names, n.Name)
		}
	}
	if len(names) == 0 {
		fmt.Fprintln(stderr, "nutwake: say which node to wake, or use -all. Nodes in the configuration:")
		for _, n := range cfg.Nodes {
			fmt.Fprintf(stderr, "  %s\n", n.Name)
		}
		return 2
	}

	ev, err := events.Open(cfg.EventsFile(), nil)
	if err != nil {
		// not being able to write the history is no reason not to send the packet
		fmt.Fprintf(stderr, "nutwake: cannot write the history (%v); sending anyway.\n", err)
		dir, derr := os.MkdirTemp("", "nutwake-wake-")
		if derr != nil {
			fmt.Fprintf(stderr, "nutwake: %v\n", derr)
			return 1
		}
		defer os.RemoveAll(dir)
		if ev, err = events.Open(dir+"/events.jsonl", nil); err != nil {
			fmt.Fprintf(stderr, "nutwake: %v\n", err)
			return 1
		}
	}
	w, err := waker.New(cfg, waker.Deps{Source: idle{}, Events: ev, State: state.New(cfg.DataDir)})
	if err != nil {
		fmt.Fprintf(stderr, "nutwake: %v\n", err)
		return 1
	}
	code := 0
	for _, name := range names {
		if err := w.WakeNow(name); err != nil {
			fmt.Fprintf(stderr, "nutwake: %v\n", err)
			code = 1
			continue
		}
		fmt.Fprintf(stdout, "Sent a wake packet to %s. It can take a few minutes to start.\n", name)
	}
	ev.FlushNow()
	return code
}
