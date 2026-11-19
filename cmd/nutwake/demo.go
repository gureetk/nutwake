package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/demo"
	"github.com/gureetk/nutwake/internal/web"
)

func cmdDemo(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nutwake demo", flag.ContinueOnError)
	fs.SetOutput(stderr)
	names := make([]string, len(demo.Scenes))
	for i, s := range demo.Scenes {
		names[i] = string(s)
	}
	scene := fs.String("scene", string(demo.Online), "moment to show: "+strings.Join(names, ", "))
	addr := fs.String("addr", "127.0.0.1:8080", "where to serve the page")
	if ok, code := parse(fs, args); !ok {
		return code
	}

	dir, err := os.MkdirTemp("", "nutwake-demo-")
	if err != nil {
		fmt.Fprintf(stderr, "nutwake: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	d, err := demo.Build(demo.Scene(*scene), time.Now(), dir)
	if err != nil {
		fmt.Fprintf(stderr, "nutwake: %v (scenes: %s)\n", err, strings.Join(names, ", "))
		return 2
	}

	cfg := config.Default()
	cfg.Nodes = []config.Node{
		{Name: "kaios", Host: "10.10.0.10", MAC: "aa:bb:cc:dd:ee:01"},
		{Name: "node-b", Host: "10.10.0.11", MAC: "aa:bb:cc:dd:ee:02"},
	}
	srv, err := web.New(&cfg, web.Deps{
		Monitor: d, Waker: d, Events: d.Events, Samples: d.Samples,
		Version: version + " (demo)", ConfigPath: "(demo)", Now: func() time.Time { return d.Now }, // the story stands still, so the page matches it
	})
	if err != nil {
		fmt.Fprintf(stderr, "nutwake: %v\n", err)
		return 1
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "nutwake: cannot listen on %s: %v\n", *addr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Showing the %q scene with made-up data at http://%s/\nPress Ctrl-C to stop.\n", *scene, ln.Addr())
	if err := srv.Serve(ctx, ln); err != nil {
		fmt.Fprintf(stderr, "nutwake: %v\n", err)
		return 1
	}
	return 0
}
