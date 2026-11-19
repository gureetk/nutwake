package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a buffer that a test can read while a command writes to it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// eventually polls cond until it is true or the time is up.
func eventually(t *testing.T, within time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// rig is a configuration whose UPS, nodes and wake target are all on loopback.
type rig struct {
	dir     string
	cfgPath string
	nutAddr string
	nodes   map[string]*node
	udp     net.PacketConn
	halted  string // a file the halt command creates
}

// node is a fake machine: it is up while its listener is open.
type node struct {
	name string
	port int
	ln   net.Listener
}

func (n *node) up(t *testing.T) {
	t.Helper()
	if n.ln != nil {
		return
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", n.port))
	if err != nil {
		t.Fatalf("node %s cannot come up: %v", n.name, err)
	}
	n.ln = ln
}

func (n *node) down() {
	if n.ln != nil {
		n.ln.Close()
		n.ln = nil
	}
}

func newRig(t *testing.T, nutAddr string, names ...string) *rig {
	t.Helper()
	dir := t.TempDir()
	udp, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback UDP:", err)
	}
	t.Cleanup(func() { udp.Close() })

	r := &rig{dir: dir, nutAddr: nutAddr, nodes: map[string]*node{}, udp: udp, halted: filepath.Join(dir, "halted")}
	host, port, _ := net.SplitHostPort(nutAddr)
	var nodesYAML strings.Builder
	for i, name := range names {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		n := &node{name: name, port: ln.Addr().(*net.TCPAddr).Port, ln: ln}
		r.nodes[name] = n
		t.Cleanup(n.down)
		fmt.Fprintf(&nodesYAML, "  - {name: %s, host: 127.0.0.1, mac: \"aa:bb:cc:dd:ee:%02x\", probe: \"tcp:%d\"}\n", name, i+1, n.port)
	}
	cfg := fmt.Sprintf(`
listen: 127.0.0.1:0
data_dir: %s/data
poll_interval: 1s
nut: {host: %s, port: %s, ups: ups, timeout: 1s}
probe: {default: icmp, timeout: 300ms, down_after: 1}
nodes:
%s
wake:
  min_battery: 15
  delay: 1s
  max_wait: 0s
  stagger: 0s
  retry_interval: 30s
  give_up_after: 5m
  broadcast: %s
shutdown:
  wait_for_nodes: 20s
  command: [sh, -c, "touch %s"]
history:
  clock_sync_timeout: 0s
`, dir, host, port, nodesYAML.String(), udp.LocalAddr(), r.halted)
	r.cfgPath = filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(r.cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return r
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fetch(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("GET %s = %d: %s", url, resp.StatusCode, b)
	}
	return string(b)
}
