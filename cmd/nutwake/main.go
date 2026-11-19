// Command nutwake keeps a homelab safe through power cuts: it shuts machines
// down cleanly when a UPS runs low, wakes them when power is back, and serves
// a status page with the history.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/gureetk/nutwake/internal/config"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `nutwake keeps your nodes safe through power cuts.

Usage:
  nutwake serve              run the daemon: watch the UPS and the nodes, wake
                             nodes when power is back, serve the status page
  nutwake shutdown           the helper for upsmon's SHUTDOWNCMD: wait for the
                             nodes to power off, then halt this machine
  nutwake shutdown --dry-run show what the helper would do, without doing it
  nutwake wake <node>...     send a wake packet now
  nutwake check              test the configuration, the UPS and the nodes
  nutwake demo               serve the status page with made-up data
  nutwake version

Every command takes -config <file> (default %s, or $NUTWAKE_CONFIG).
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is main without the process around it, so it can be tested. It returns
// the exit code: 0 for success, 1 for a failure, 2 for a mistake in how it was
// called.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, usage, config.DefaultPath)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "serve":
		return cmdServe(ctx, rest, stdout, stderr)
	case "shutdown":
		return cmdShutdown(ctx, rest, stdout, stderr)
	case "wake":
		return cmdWake(ctx, rest, stdout, stderr)
	case "check":
		return cmdCheck(ctx, rest, stdout, stderr)
	case "demo":
		return cmdDemo(ctx, rest, stdout, stderr)
	case "version", "-version", "--version":
		fmt.Fprintln(stdout, "nutwake", version)
		return 0
	case "help", "-h", "-help", "--help":
		fmt.Fprintf(stdout, usage, config.DefaultPath)
		return 0
	}
	fmt.Fprintf(stderr, "nutwake: unknown command %q\n\n", cmd)
	fmt.Fprintf(stderr, usage, config.DefaultPath)
	return 2
}

// flags makes a flag set for a command, with the -config flag every command has.
func flags(name string, stderr io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("nutwake "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	def := os.Getenv("NUTWAKE_CONFIG")
	if def == "" {
		def = config.DefaultPath
	}
	return fs, fs.String("config", def, "path to the configuration file")
}

// parse parses args; it reports whether to carry on, and the exit code if not.
func parse(fs *flag.FlagSet, args []string) (ok bool, code int) {
	switch err := fs.Parse(args); err {
	case nil:
		return true, 0
	case flag.ErrHelp:
		return false, 0
	}
	return false, 2
}

// load reads the configuration, saying what is wrong in plain words.
func load(path string, stderr io.Writer) (*config.Config, bool) {
	cfg, err := config.Load(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(stderr, "nutwake: there is no configuration at %s.\nCopy config.example.yaml there and edit it, or pass -config <file>.\n", path)
		} else {
			fmt.Fprintf(stderr, "nutwake: the configuration is not valid:\n%v\n", err)
		}
		return nil, false
	}
	return cfg, true
}
