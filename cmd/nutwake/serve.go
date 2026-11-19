package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"

	"github.com/gureetk/nutwake/internal/clocksync"
	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/monitor"
	"github.com/gureetk/nutwake/internal/samples"
	"github.com/gureetk/nutwake/internal/state"
	"github.com/gureetk/nutwake/internal/waker"
	"github.com/gureetk/nutwake/internal/web"
)

func cmdServe(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs, path := flags("serve", stderr)
	if ok, code := parse(fs, args); !ok {
		return code
	}
	cfg, ok := load(*path, stderr)
	if !ok {
		return 1
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// listen first: a port that is taken is the commonest mistake, and it is
	// better to say so before anything has started
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		fmt.Fprintf(stderr, "nutwake: cannot listen on %s: %v\n", cfg.Listen, err)
		return 1
	}
	if err := serve(ctx, cfg, *path, ln); err != nil {
		fmt.Fprintf(stderr, "nutwake: %v\n", err)
		return 1
	}
	return 0
}

// serve runs the daemon until ctx is cancelled.
func serve(ctx context.Context, cfg *config.Config, cfgPath string, ln net.Listener) error {
	defer ln.Close()
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("cannot create the data directory: %w", err)
	}
	clock := clocksync.New(cfg.History.ClockSyncTimeout)
	ev, err := events.Open(cfg.EventsFile(), clock)
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", cfg.DataDir, err)
	}
	sm, err := samples.Open(cfg.SamplesFile(), clock)
	if err != nil {
		return fmt.Errorf("cannot write to %s: %w", cfg.DataDir, err)
	}
	st := state.New(cfg.DataDir)

	mon, err := monitor.New(cfg, monitor.Deps{Events: ev, State: st, Samples: sm, Clock: clock})
	if err != nil {
		return err
	}
	wk, err := waker.New(cfg, waker.Deps{Source: mon, Events: ev, State: st})
	if err != nil {
		return err
	}
	srv, err := web.New(cfg, web.Deps{
		Monitor: mon, Waker: wk, Events: ev, Samples: sm, Version: version, ConfigPath: cfgPath,
	})
	if err != nil {
		return err
	}

	ev.Add(events.LevelInfo, events.KindStart, "", "nutwake started.", map[string]any{"version": version})
	slog.Info("nutwake started", "version", version, "nodes", len(cfg.Nodes), "page", ln.Addr().String())

	var wg sync.WaitGroup
	webErr := make(chan error, 1)
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	wg.Add(3)
	go func() { defer wg.Done(); mon.Run(run) }()
	go func() { defer wg.Done(); wk.Run(run) }()
	go func() {
		defer wg.Done()
		if err := srv.Serve(run, ln); err != nil {
			webErr <- err
			cancel() // a page that cannot be served is not worth running without
		}
	}()
	<-run.Done()
	wg.Wait()

	ev.Add(events.LevelInfo, events.KindStop, "", "nutwake stopped.", nil)
	ev.FlushNow()
	sm.FlushNow()
	slog.Info("nutwake stopped")

	select {
	case err := <-webErr:
		return fmt.Errorf("the status page stopped: %w", err)
	default:
	}
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
