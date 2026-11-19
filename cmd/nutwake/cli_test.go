package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/fakenut"
)

func runCmd(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e syncBuf
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	code = run(ctx, args, &o, &e)
	return code, o.String(), e.String()
}

func startFakeNUT(t *testing.T) *fakenut.Server {
	t.Helper()
	f, err := fakenut.Start("127.0.0.1:0", "ups")
	if err != nil {
		t.Skip("no loopback TCP:", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestNoArgumentsAndUnknownCommand(t *testing.T) {
	if code, _, errOut := runCmd(t); code != 2 || !strings.Contains(errOut, "Usage:") {
		t.Fatalf("no args: %d %q", code, errOut)
	}
	if code, _, errOut := runCmd(t, "frobnicate"); code != 2 || !strings.Contains(errOut, `unknown command "frobnicate"`) {
		t.Fatalf("unknown: %d %q", code, errOut)
	}
}

func TestVersionAndHelp(t *testing.T) {
	if code, out, _ := runCmd(t, "version"); code != 0 || !strings.HasPrefix(out, "nutwake ") {
		t.Fatalf("version: %d %q", code, out)
	}
	if code, out, _ := runCmd(t, "--help"); code != 0 || !strings.Contains(out, "nutwake serve") {
		t.Fatalf("help: %d %q", code, out)
	}
}

func TestMissingAndBrokenConfig(t *testing.T) {
	dir := t.TempDir()
	for _, cmd := range []string{"serve", "check", "wake", "shutdown"} {
		args := []string{cmd, "-config", filepath.Join(dir, "nope.yaml")}
		if cmd == "shutdown" {
			args = append(args, "--dry-run") // without it, a missing config still halts
		}
		code, _, errOut := runCmd(t, args...)
		if code != 1 || !strings.Contains(errOut, "no configuration at") && !strings.Contains(errOut, "no such file") {
			t.Errorf("%s with a missing config: %d %q", cmd, code, errOut)
		}
	}
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("nodes: []\nlistne: 127.0.0.1:1\n"), 0o600)
	code, _, errOut := runCmd(t, "check", "-config", bad)
	if code != 1 || !strings.Contains(errOut, "not valid") {
		t.Fatalf("broken config: %d %q", code, errOut)
	}
}

func TestCheck(t *testing.T) {
	nutd := startFakeNUT(t)
	r := newRig(t, nutd.Addr(), "a", "b")
	os.MkdirAll(filepath.Join(r.dir, "data"), 0o755)
	r.nodes["b"].down()

	code, out, errOut := runCmd(t, "check", "-config", r.cfgPath)
	if code != 0 {
		t.Fatalf("check = %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"configuration", "UPS", "is online", "node a", "answering now", "node b", "not answering", "halt command", "wake"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// without a UPS to talk to, check fails and says why
	nutd.SetDown(true)
	code, out, _ = runCmd(t, "check", "-config", r.cfgPath)
	if code != 1 || !strings.Contains(out, "FAIL") || !strings.Contains(out, "cannot read") {
		t.Fatalf("check without a UPS = %d\n%s", code, out)
	}
}

func TestShutdownDryRunChangesNothing(t *testing.T) {
	nutd := startFakeNUT(t)
	r := newRig(t, nutd.Addr(), "a", "b")
	data := filepath.Join(r.dir, "data")
	os.MkdirAll(data, 0o755)

	code, out, errOut := runCmd(t, "shutdown", "--dry-run", "-config", r.cfgPath)
	if code != 0 {
		t.Fatalf("dry run = %d\n%s\n%s", code, out, errOut)
	}
	for _, want := range []string{"wait up to 20s for a, b to power off", "write down a, b", "Nothing was changed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if fileExists(r.halted) {
		t.Fatal("the dry run halted the machine")
	}
	if fileExists(filepath.Join(data, "wake.json")) {
		t.Fatal("the dry run wrote the wake list")
	}
	if fileExists(filepath.Join(data, "events.jsonl")) {
		t.Fatal("the dry run wrote to the history")
	}
}

func TestShutdownHaltsWhateverTheConfigSays(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "default-halt")
	old := haltFallback
	haltFallback = []string{"sh", "-c", "touch " + marker}
	t.Cleanup(func() { haltFallback = old })

	// a configuration that cannot be read
	code, _, errOut := runCmd(t, "shutdown", "-config", filepath.Join(dir, "missing.yaml"))
	if code != 0 || !fileExists(marker) {
		t.Fatalf("missing config: code %d, halted %v\n%s", code, fileExists(marker), errOut)
	}
	if !strings.Contains(errOut, "Halting with the default command anyway") {
		t.Errorf("stderr = %q", errOut)
	}

	// a halt command that fails falls back to the default
	os.Remove(marker)
	nutd := startFakeNUT(t)
	r := newRig(t, nutd.Addr(), "a")
	r.nodes["a"].down()
	os.MkdirAll(filepath.Join(r.dir, "data"), 0o755)
	data, _ := os.ReadFile(r.cfgPath)
	os.WriteFile(r.cfgPath, []byte(strings.Replace(string(data), `command: [sh, -c, "touch `+r.halted+`"]`, "command: [/nonexistent/halt]", 1)), 0o600)
	code, _, errOut = runCmd(t, "shutdown", "-config", r.cfgPath)
	if code != 0 || !fileExists(marker) {
		t.Fatalf("failing command: code %d, fallback ran %v\n%s", code, fileExists(marker), errOut)
	}
}

func TestShutdownWaitsForTheNodesThenHalts(t *testing.T) {
	nutd := startFakeNUT(t)
	r := newRig(t, nutd.Addr(), "a")
	os.MkdirAll(filepath.Join(r.dir, "data"), 0o755)

	done := make(chan int, 1)
	var out, errOut syncBuf
	go func() { done <- run(context.Background(), []string{"shutdown", "-config", r.cfgPath}, &out, &errOut) }()

	// the node is still up, so the helper must still be waiting
	time.Sleep(2500 * time.Millisecond)
	if fileExists(r.halted) {
		t.Fatalf("halted while the node was still up\n%s", errOut.String())
	}
	r.nodes["a"].down()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("shutdown = %d\n%s", code, errOut.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the helper did not finish after the node went off")
	}
	if !fileExists(r.halted) {
		t.Fatal("the halt command did not run")
	}
	if !fileExists(filepath.Join(r.dir, "data", "wake.json")) {
		t.Fatal("the wake list was not written")
	}
}

func TestWake(t *testing.T) {
	nutd := startFakeNUT(t)
	r := newRig(t, nutd.Addr(), "a", "b")
	os.MkdirAll(filepath.Join(r.dir, "data"), 0o755)

	got := make(chan []byte, 4)
	go func() {
		buf := make([]byte, 256)
		for {
			n, _, err := r.udp.ReadFrom(buf)
			if err != nil {
				return
			}
			got <- append([]byte(nil), buf[:n]...)
		}
	}()

	code, out, errOut := runCmd(t, "wake", "-config", r.cfgPath, "b")
	if code != 0 || !strings.Contains(out, "Sent a wake packet to b") {
		t.Fatalf("wake = %d\n%s\n%s", code, out, errOut)
	}
	select {
	case pkt := <-got:
		if len(pkt) != 102 {
			t.Fatalf("packet is %d bytes, want 102", len(pkt))
		}
		for i := 0; i < 6; i++ {
			if pkt[i] != 0xff {
				t.Fatalf("packet does not start with six 0xff bytes: % x", pkt[:6])
			}
		}
		if pkt[len(pkt)-1] != 0x02 { // the MAC of b ends in :02
			t.Fatalf("packet is for the wrong node: % x", pkt[6:12])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no packet arrived")
	}

	if code, _, errOut := runCmd(t, "wake", "-config", r.cfgPath, "nope"); code != 1 || !strings.Contains(errOut, `no node called "nope"`) {
		t.Fatalf("unknown node = %d %q", code, errOut)
	}
	if code, _, errOut := runCmd(t, "wake", "-config", r.cfgPath); code != 2 || !strings.Contains(errOut, "a\n") {
		t.Fatalf("no node named = %d %q", code, errOut)
	}
	if code, out, _ := runCmd(t, "wake", "-config", r.cfgPath, "-all"); code != 0 || !strings.Contains(out, "to a") || !strings.Contains(out, "to b") {
		t.Fatalf("wake -all = %d %q", code, out)
	}
	events, _ := os.ReadFile(filepath.Join(r.dir, "data", "events.jsonl"))
	if !strings.Contains(string(events), "wake_manual") {
		t.Fatalf("the history lacks wake_manual:\n%s", events)
	}
}

func TestDemo(t *testing.T) {
	if code, _, errOut := runCmd(t, "demo", "-scene", "midnight"); code != 2 || !strings.Contains(errOut, "unknown scene") {
		t.Fatalf("bad scene = %d %q", code, errOut)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var out, errOut syncBuf
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"demo", "-scene", "battery", "-addr", "127.0.0.1:0"}, &out, &errOut) }()
	var url string
	eventually(t, 10*time.Second, "the demo to say where it is", func() bool {
		s := out.String()
		i := strings.Index(s, "http://")
		if i < 0 {
			return false
		}
		url = strings.Fields(s[i:])[0]
		return true
	})
	body := fetch(t, url+"api/status")
	if !strings.Contains(body, "Running on battery") {
		t.Fatalf("the battery scene says: %.300s", body)
	}
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("demo = %d %s", code, errOut.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("demo did not stop")
	}
}
