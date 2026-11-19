package probe

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestParseSpec(t *testing.T) {
	good := map[string]Spec{
		"icmp":      {Kind: "icmp"},
		"tcp:8006":  {Kind: "tcp", Port: 8006},
		"tcp:22":    {Kind: "tcp", Port: 22},
		"tcp:1":     {Kind: "tcp", Port: 1},
		"tcp:65535": {Kind: "tcp", Port: 65535},
	}
	for in, want := range good {
		got, err := ParseSpec(in)
		if err != nil || got != want {
			t.Errorf("ParseSpec(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ping", "tcp", "tcp:", "tcp:0", "tcp:65536", "tcp:99999", "tcp:abc", "tcp:-1", "tcp:+80", "tcp:80 ", "ICMP", "http:80"} {
		if _, err := ParseSpec(bad); err == nil {
			t.Errorf("ParseSpec(%q) should fail", bad)
		}
	}
}

func TestParseBuildsTheRightProber(t *testing.T) {
	p, err := Parse("tcp:8006", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if tp, ok := p.(TCP); !ok || tp.Port != 8006 {
		t.Errorf("got %#v", p)
	}
	p, _ = Parse("icmp", time.Second)
	if _, ok := p.(ICMP); !ok {
		t.Errorf("got %#v", p)
	}
	if _, err := Parse("nope", time.Second); err == nil {
		t.Error("expected an error")
	}
}

func TestTCPProbe(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	p := TCP{Port: port, Timeout: time.Second}
	if !p.Up(context.Background(), "127.0.0.1") {
		t.Error("should be up while the port is open")
	}
	ln.Close()
	if p.Up(context.Background(), "127.0.0.1") {
		t.Error("a refused connection should count as down")
	}
}

func TestTCPProbeHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if (TCP{Port: 1, Timeout: time.Second}).Up(ctx, "127.0.0.1") {
		t.Error("a cancelled context should not report up")
	}
}

func TestICMPBuildsThePingCommand(t *testing.T) {
	var gotName string
	var gotArgs []string
	p := ICMP{Timeout: 1500 * time.Millisecond, Run: func(_ context.Context, name string, args ...string) error {
		gotName, gotArgs = name, args
		return nil
	}}
	if !p.Up(context.Background(), "10.10.0.10") {
		t.Fatal("a successful ping should be up")
	}
	want := []string{"-c", "1", "-n", "-W", "2", "10.10.0.10"}
	if gotName != "ping" || !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("ran %s %v, want ping %v", gotName, gotArgs, want)
	}
}

func TestICMPFailureIsDown(t *testing.T) {
	p := ICMP{Timeout: time.Second, Run: func(context.Context, string, ...string) error { return errors.New("exit status 1") }}
	if p.Up(context.Background(), "10.10.0.10") {
		t.Error("a failed ping should be down")
	}
}

func TestICMPRefusesHostsThatLookLikeFlags(t *testing.T) {
	called := false
	p := ICMP{Timeout: time.Second, Run: func(context.Context, string, ...string) error { called = true; return nil }}
	for _, h := range []string{"", "-f", "--help", "-c5"} {
		if p.Up(context.Background(), h) {
			t.Errorf("host %q should never be up", h)
		}
	}
	if called {
		t.Error("ping must not run for a host that looks like a flag")
	}
}

func TestICMPTimeoutRoundsUpToWholeSeconds(t *testing.T) {
	for in, want := range map[time.Duration]string{time.Millisecond: "1", time.Second: "1", 1001 * time.Millisecond: "2", 3 * time.Second: "3"} {
		var got string
		p := ICMP{Timeout: in, Run: func(_ context.Context, _ string, a ...string) error { got = a[4]; return nil }}
		p.Up(context.Background(), "h")
		if got != want {
			t.Errorf("timeout %v -> -W %s, want %s", in, got, want)
		}
	}
}
