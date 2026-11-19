// Package nut is a minimal client for the NUT (Network UPS Tools) network
// protocol. It only implements what nutwake needs: reading every variable of a
// UPS with LIST VAR, which requires no authentication.
package nut

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Client reads variables from a upsd server.
type Client struct {
	Addr    string        // host:port of upsd
	UPS     string        // name of the UPS as configured in ups.conf
	Timeout time.Duration // applies to the whole exchange
}

// Reading is one snapshot of every variable a UPS reported.
type Reading struct {
	At   time.Time
	Vars map[string]string
}

// Read connects to upsd, lists the UPS variables and returns them.
func (c *Client) Read(ctx context.Context) (*Reading, error) {
	d := net.Dialer{Timeout: c.Timeout}
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return nil, fmt.Errorf("connect to upsd: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.Timeout))

	// Closing the connection when ctx is cancelled unblocks the scanner below.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if _, err := fmt.Fprintf(conn, "LIST VAR %s\n", c.UPS); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	vars := map[string]string{}
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "ERR "):
			return nil, fmt.Errorf("upsd: %s", strings.TrimPrefix(line, "ERR "))
		case strings.HasPrefix(line, "END LIST VAR"):
			_, _ = fmt.Fprint(conn, "LOGOUT\n") // be polite; failure is irrelevant
			return &Reading{At: time.Now(), Vars: vars}, nil
		case strings.HasPrefix(line, "VAR "):
			name, value, err := parseVar(line)
			if err != nil {
				return nil, err
			}
			vars[name] = value
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return nil, errors.New("upsd closed the connection before the list ended")
}

// parseVar parses `VAR <ups> <name> "<value>"`.
func parseVar(line string) (name, value string, err error) {
	parts := strings.SplitN(line, " ", 4)
	if len(parts) != 4 {
		return "", "", fmt.Errorf("malformed VAR line %q", line)
	}
	value, err = unquote(parts[3])
	if err != nil {
		return "", "", fmt.Errorf("malformed VAR line %q: %w", line, err)
	}
	return parts[2], value, nil
}

// unquote strips the surrounding quotes and resolves the \" and \\ escapes.
func unquote(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", errors.New("value is not quoted")
	}
	s = s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String(), nil
}

// State is a coarse classification of what the UPS is doing.
type State string

const (
	StateOnline      State = "online"
	StateOnBattery   State = "on_battery"
	StateLowBattery  State = "low_battery"
	StateOther       State = "other" // e.g. OFF, or no status reported
	StateUnreachable State = "unreachable"
)

// Flags returns the words of ups.status, e.g. ["OL", "CHRG"].
func (r *Reading) Flags() []string { return strings.Fields(r.Vars["ups.status"]) }

// Has reports whether ups.status contains flag.
func (r *Reading) Has(flag string) bool {
	for _, f := range r.Flags() {
		if f == flag {
			return true
		}
	}
	return false
}

// State classifies the reading. On-battery wins over online if a UPS ever
// reports both.
func (r *Reading) State() State {
	switch {
	case r.Has("OB") && r.Has("LB"):
		return StateLowBattery
	case r.Has("OB"):
		return StateOnBattery
	case r.Has("OL"):
		return StateOnline
	}
	return StateOther
}

// Num returns a numeric variable.
func (r *Reading) Num(name string) (float64, bool) {
	s, ok := r.Vars[name]
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// First returns the first of the named variables that is present.
func (r *Reading) First(names ...string) string {
	for _, n := range names {
		if v, ok := r.Vars[n]; ok && v != "" {
			return v
		}
	}
	return ""
}

// Charge is battery.charge in percent.
func (r *Reading) Charge() (float64, bool) { return r.Num("battery.charge") }
