package nut

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/fakenut"
)

func newClient(t *testing.T) (*Client, *fakenut.Server) {
	t.Helper()
	srv, err := fakenut.Start("127.0.0.1:0", "ups")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return &Client{Addr: srv.Addr(), UPS: "ups", Timeout: time.Second}, srv
}

func TestReadReturnsVariables(t *testing.T) {
	c, _ := newClient(t)
	r, err := c.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Vars["device.model"]; got != "ST425" {
		t.Errorf("device.model = %q", got)
	}
	if v, ok := r.Charge(); !ok || v != 100 {
		t.Errorf("charge = %v, %v", v, ok)
	}
	if r.State() != StateOnline {
		t.Errorf("state = %v", r.State())
	}
}

func TestReadSeesChanges(t *testing.T) {
	c, srv := newClient(t)
	srv.Set(map[string]string{"ups.status": "OB DISCHRG", "battery.charge": "43"})
	r, err := c.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.State() != StateOnBattery {
		t.Errorf("state = %v, want on_battery", r.State())
	}
	if v, _ := r.Charge(); v != 43 {
		t.Errorf("charge = %v", v)
	}
}

func TestValuesWithQuotesAndBackslashesSurvive(t *testing.T) {
	c, srv := newClient(t)
	want := `say "hi" \ there`
	srv.Set(map[string]string{"ups.test": want})
	r, err := c.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Vars["ups.test"]; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUnknownUPS(t *testing.T) {
	c, _ := newClient(t)
	c.UPS = "nope"
	_, err := c.Read(context.Background())
	if err == nil || !strings.Contains(err.Error(), "UNKNOWN-UPS") {
		t.Errorf("want UNKNOWN-UPS, got %v", err)
	}
}

func TestServerDownIsAnError(t *testing.T) {
	c, srv := newClient(t)
	srv.SetDown(true)
	if _, err := c.Read(context.Background()); err == nil {
		t.Error("expected an error while the server drops connections")
	}
	srv.Close()
	if _, err := c.Read(context.Background()); err == nil {
		t.Error("expected an error after the server is closed")
	}
}

func TestSilentServerTimesOut(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close() // accept and say nothing
		}
	}()
	c := &Client{Addr: ln.Addr().String(), UPS: "ups", Timeout: 150 * time.Millisecond}
	start := time.Now()
	if _, err := c.Read(context.Background()); err == nil {
		t.Fatal("expected a timeout")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v to give up", d)
	}
}

func TestCancelledContextStopsRead(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{Addr: ln.Addr().String(), UPS: "ups", Timeout: 10 * time.Second}
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	if _, err := c.Read(ctx); err == nil {
		t.Fatal("expected an error after cancel")
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("cancel took %v to take effect", d)
	}
}

func TestStateClassification(t *testing.T) {
	cases := map[string]State{
		"OL":            StateOnline,
		"OL CHRG":       StateOnline,
		"OL BOOST":      StateOnline,
		"OL LB":         StateOnline, // low charge on mains is not an emergency
		"OB DISCHRG":    StateOnBattery,
		"OB":            StateOnBattery,
		"OB DISCHRG LB": StateLowBattery,
		"LB OB":         StateLowBattery,
		"OB LB FSD":     StateLowBattery,
		"OB OL":         StateOnBattery, // on battery wins if a UPS ever says both
		"OFF":           StateOther,
		"":              StateOther,
	}
	for status, want := range cases {
		r := &Reading{Vars: map[string]string{"ups.status": status}}
		if got := r.State(); got != want {
			t.Errorf("State(%q) = %v, want %v", status, got, want)
		}
	}
	if (&Reading{Vars: map[string]string{}}).State() != StateOther {
		t.Error("a reading with no status should be StateOther")
	}
}

func TestFlagsAndHas(t *testing.T) {
	r := &Reading{Vars: map[string]string{"ups.status": "OL CHRG"}}
	if !r.Has("CHRG") || r.Has("OB") || len(r.Flags()) != 2 {
		t.Errorf("Flags/Has wrong: %v", r.Flags())
	}
}

func TestNumAndFirst(t *testing.T) {
	r := &Reading{Vars: map[string]string{"a": " 12.5 ", "b": "x", "model": "ST425", "empty": ""}}
	if v, ok := r.Num("a"); !ok || v != 12.5 {
		t.Errorf("Num(a) = %v, %v", v, ok)
	}
	if _, ok := r.Num("b"); ok {
		t.Error("Num(b) should fail on non-numbers")
	}
	if _, ok := r.Num("missing"); ok {
		t.Error("Num(missing) should fail")
	}
	if got := r.First("empty", "nope", "model"); got != "ST425" {
		t.Errorf("First = %q", got)
	}
	if got := r.First("nope"); got != "" {
		t.Errorf("First(nope) = %q", got)
	}
}

func TestParseVarAndUnquote(t *testing.T) {
	name, val, err := parseVar(`VAR ups battery.charge "100"`)
	if err != nil || name != "battery.charge" || val != "100" {
		t.Errorf("parseVar = %q %q %v", name, val, err)
	}
	if _, val, _ := parseVar(`VAR ups ups.mfr "Cyber Power Systems"`); val != "Cyber Power Systems" {
		t.Errorf("value with spaces = %q", val)
	}
	if _, _, err := parseVar(`VAR ups only`); err == nil {
		t.Error("expected an error for a short line")
	}
	if _, _, err := parseVar(`VAR ups x unquoted`); err == nil {
		t.Error("expected an error for an unquoted value")
	}
	if got, _ := unquote(`"a\"b\\c"`); got != `a"b\c` {
		t.Errorf("unquote = %q", got)
	}
}
