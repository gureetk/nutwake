// Package probe checks whether a machine is up.
package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Prober reports whether a machine currently looks up.
type Prober interface {
	Up(ctx context.Context, host string) bool
}

// Spec is a parsed probe setting: "icmp" or "tcp:<port>".
type Spec struct {
	Kind string // "icmp" or "tcp"
	Port int    // only for "tcp"
}

var portRe = regexp.MustCompile(`^[0-9]{1,5}$`)

// ParseSpec parses "icmp" or "tcp:<port>".
func ParseSpec(s string) (Spec, error) {
	if s == "icmp" {
		return Spec{Kind: "icmp"}, nil
	}
	if port, ok := strings.CutPrefix(s, "tcp:"); ok && portRe.MatchString(port) {
		if n, _ := strconv.Atoi(port); n >= 1 && n <= 65535 {
			return Spec{Kind: "tcp", Port: n}, nil
		}
	}
	return Spec{}, fmt.Errorf("%q is not \"icmp\" or \"tcp:<port>\" (port 1-65535)", s)
}

// Parse builds the Prober a spec describes.
func Parse(spec string, timeout time.Duration) (Prober, error) {
	s, err := ParseSpec(spec)
	if err != nil {
		return nil, err
	}
	if s.Kind == "tcp" {
		return TCP{Port: s.Port, Timeout: timeout}, nil
	}
	return ICMP{Timeout: timeout, Run: defaultRun}, nil
}

// TCP counts a machine as up when a connection to Port succeeds. A refused
// connection means the service is not ready, so it counts as down. That makes
// tcp:8006 a good "Proxmox has finished booting" check.
type TCP struct {
	Port    int
	Timeout time.Duration
}

// Up implements Prober.
func (p TCP) Up(ctx context.Context, host string) bool {
	d := net.Dialer{Timeout: p.Timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(p.Port)))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ICMP counts a machine as up when it answers one ping. It runs the system
// ping command so nutwake itself needs no special privileges.
type ICMP struct {
	Timeout time.Duration
	// Run executes a command; it is replaceable for tests.
	Run func(ctx context.Context, name string, args ...string) error
}

// Up implements Prober.
func (p ICMP) Up(ctx context.Context, host string) bool {
	// A host that looks like a flag must never reach ping's argument list.
	if host == "" || strings.HasPrefix(host, "-") {
		return false
	}
	secs := int((p.Timeout + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout+2*time.Second)
	defer cancel()
	run := p.Run
	if run == nil {
		run = defaultRun
	}
	return run(ctx, "ping", "-c", "1", "-n", "-W", strconv.Itoa(secs), host) == nil
}

// Check reports whether the ping command is installed.
func (p ICMP) Check() error {
	if _, err := exec.LookPath("ping"); err != nil {
		return errors.New("the ping command was not found: install iputils-ping, or use a tcp:<port> probe")
	}
	return nil
}

func defaultRun(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}
