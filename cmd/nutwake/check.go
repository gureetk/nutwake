package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/humanize"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/shutdown"
	"github.com/gureetk/nutwake/internal/state"
)

func cmdCheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs, path := flags("check", stderr)
	if ok, code := parse(fs, args); !ok {
		return code
	}
	cfg, ok := load(*path, stderr)
	if !ok {
		return 1
	}

	rs := []shutdown.Result{{
		Name: "configuration", Level: shutdown.OK,
		Detail: fmt.Sprintf("%s is valid: %d nodes, page on %s", *path, len(cfg.Nodes), cfg.Listen),
	}}
	rs = append(rs, checkUPS(ctx, cfg))
	if why := untrusted(*path); why != "" {
		rs = append(rs, shutdown.Result{Name: "permissions", Level: shutdown.Warn, Detail: why +
			". The shutdown helper runs as root, so it will ignore shutdown.command and use " + strings.Join(haltFallback, " ") + " instead. " +
			"Fix with: chown root:root and chmod 644 on the file, and keep its directory root-only"})
	}

	// the shutdown helper knows how to test the rest
	h, err := shutdown.New(cfg, shutdown.Deps{State: state.New(cfg.DataDir), ICMP: secondOpinion})
	if err != nil {
		rs = append(rs, shutdown.Result{Name: "nodes", Level: shutdown.Fail, Detail: err.Error()})
	} else {
		_, more := h.Check(ctx)
		rs = append(rs, more...)
	}
	rs = append(rs, checkWake(cfg))

	fmt.Fprintln(stdout, "nutwake check")
	fmt.Fprintln(stdout)
	failed := printResults(stdout, rs)
	fmt.Fprintln(stdout)
	if failed {
		fmt.Fprintln(stdout, "Something needs fixing before nutwake can be trusted.")
		return 1
	}
	fmt.Fprintln(stdout, "Nothing failed.")
	return 0
}

func checkUPS(ctx context.Context, cfg *config.Config) shutdown.Result {
	c := nut.Client{Addr: cfg.NUT.Addr(), UPS: cfg.NUT.UPS, Timeout: cfg.NUT.Timeout}
	r, err := c.Read(ctx)
	if err != nil {
		return shutdown.Result{Name: "UPS", Level: shutdown.Fail,
			Detail: fmt.Sprintf("cannot read %q from the NUT server at %s: %v", cfg.NUT.UPS, cfg.NUT.Addr(), err)}
	}
	detail := fmt.Sprintf("%q at %s is %s", cfg.NUT.UPS, cfg.NUT.Addr(), r.State())
	if ch, ok := r.Charge(); ok {
		detail += fmt.Sprintf(", battery %s%%", humanize.Percent(ch))
	}
	level := shutdown.OK
	if r.State() != nut.StateOnline {
		level = shutdown.Warn
		detail += ". Running on battery or not on mains: is the power out?"
	}
	return shutdown.Result{Name: "UPS", Level: level, Detail: detail}
}

func checkWake(cfg *config.Config) shutdown.Result {
	w := cfg.Wake
	detail := fmt.Sprintf("wake packets go to %s once power has been steady for %s", w.Broadcast, humanize.Duration(w.Delay))
	if w.MinBattery > 0 {
		detail += fmt.Sprintf(" and the battery is at %s%%", humanize.Percent(w.MinBattery))
	}
	level := shutdown.OK
	if w.Broadcast == "255.255.255.255:9" {
		// the limited broadcast is dropped by some routers and VLAN setups
		level = shutdown.Warn
		detail += ". If nodes do not wake, set wake.broadcast to your subnet's broadcast address"
	}
	return shutdown.Result{Name: "wake", Level: level, Detail: detail}
}
