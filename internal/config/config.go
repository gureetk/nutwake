// Package config loads and validates the nutwake YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/gureetk/nutwake/internal/probe"
)

// DefaultPath is where nutwake looks for its config when -config is not given.
const DefaultPath = "/etc/nutwake/config.yaml"

// Config is the full nutwake configuration.
type Config struct {
	Listen       string        `yaml:"listen"`
	DataDir      string        `yaml:"data_dir"`
	PollInterval time.Duration `yaml:"poll_interval"`

	NUT      NUT      `yaml:"nut"`
	Probe    Probe    `yaml:"probe"`
	Nodes    []Node   `yaml:"nodes"`
	Wake     Wake     `yaml:"wake"`
	Shutdown Shutdown `yaml:"shutdown"`
	History  History  `yaml:"history"`
}

// NUT describes how to reach the upsd server.
type NUT struct {
	Host    string        `yaml:"host"`
	Port    int           `yaml:"port"`
	UPS     string        `yaml:"ups"`
	Timeout time.Duration `yaml:"timeout"`
}

// Addr returns host:port for dialing.
func (n NUT) Addr() string { return net.JoinHostPort(n.Host, fmt.Sprint(n.Port)) }

// Probe controls how nodes are checked for liveness.
type Probe struct {
	// Default is the probe spec used by nodes that do not set their own:
	// "icmp" or "tcp:<port>".
	Default string        `yaml:"default"`
	Timeout time.Duration `yaml:"timeout"`
	// DownAfter is how many consecutive failed probes mark a node as down.
	DownAfter int `yaml:"down_after"`
}

// Node is one machine that nutwake keeps track of and can wake.
type Node struct {
	Name  string `yaml:"name"`
	Host  string `yaml:"host"`
	MAC   string `yaml:"mac"`
	Probe string `yaml:"probe"` // optional override of probe.default
}

// Wake controls how and when nodes are woken after power returns.
type Wake struct {
	// MinBattery is the UPS charge (percent) required before waking nodes.
	// Keep it clearly above the UPS's own low-battery level (10% on most
	// units), or a second outage right after waking shuts the nodes down again
	// before they have finished booting. 0 disables the check.
	MinBattery float64 `yaml:"min_battery"`
	// Delay is how long power must have been stable before the first packet.
	Delay time.Duration `yaml:"delay"`
	// MaxWait wakes the nodes anyway once power has been stable this long,
	// even if the battery has not reached MinBattery. It stops a worn battery
	// that never charges from keeping the nodes off for good. 0 disables it.
	MaxWait time.Duration `yaml:"max_wait"`
	// Stagger spaces out the first packet of each node, so two servers do not
	// power on in the same instant.
	Stagger time.Duration `yaml:"stagger"`
	// RetryInterval is how long to wait for a node before sending another packet.
	RetryInterval time.Duration `yaml:"retry_interval"`
	// GiveUpAfter stops trying for a node this long after its first packet.
	GiveUpAfter time.Duration `yaml:"give_up_after"`
	// Broadcast is the host:port the magic packet is sent to.
	Broadcast string `yaml:"broadcast"`
}

// Shutdown configures the helper that upsmon runs as its SHUTDOWNCMD.
type Shutdown struct {
	// WaitForNodes is the longest the helper waits for the nodes to power off
	// before halting this machine anyway.
	WaitForNodes time.Duration `yaml:"wait_for_nodes"`
	// Command halts this machine.
	Command []string `yaml:"command"`
}

// History controls how much is remembered.
type History struct {
	MaxEvents int `yaml:"max_events"`
	// ClockSyncTimeout is how long, after start-up, events are held back
	// waiting for the system clock to be set (a Pi has no battery-backed
	// clock). After this they are written with the clock as it is.
	ClockSyncTimeout time.Duration `yaml:"clock_sync_timeout"`
	// SampleKeep is how long the battery and node history behind the chart
	// is kept.
	SampleKeep time.Duration `yaml:"sample_keep"`
}

// Default returns a Config holding the built-in defaults.
func Default() Config {
	return Config{
		Listen:       "127.0.0.1:8080",
		DataDir:      "/var/lib/nutwake",
		PollInterval: 5 * time.Second,
		NUT:          NUT{Host: "127.0.0.1", Port: 3493, UPS: "ups", Timeout: 3 * time.Second},
		Probe:        Probe{Default: "icmp", Timeout: time.Second, DownAfter: 2},
		Wake: Wake{
			MinBattery:    15,
			Delay:         time.Minute,
			MaxWait:       2 * time.Hour,
			Stagger:       5 * time.Second,
			RetryInterval: 90 * time.Second,
			GiveUpAfter:   20 * time.Minute,
			Broadcast:     "255.255.255.255:9",
		},
		Shutdown: Shutdown{
			WaitForNodes: 5 * time.Minute,
			Command:      []string{"/sbin/shutdown", "-h", "+0"},
		},
		History: History{MaxEvents: 5000, ClockSyncTimeout: 5 * time.Minute, SampleKeep: 7 * 24 * time.Hour},
	}
}

// Load reads, parses and validates the file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Parse parses and validates YAML config data. Unknown keys are an error so
// that typos do not silently fall back to defaults.
func Parse(data []byte) (*Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Validate checks the configuration and reports every problem found.
func (c *Config) Validate() error {
	var errs []error
	bad := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		bad("listen %q: %v", c.Listen, err)
	}
	if c.DataDir == "" {
		bad("data_dir must not be empty")
	}
	if c.PollInterval < time.Second {
		bad("poll_interval must be at least 1s")
	}
	if c.NUT.Host == "" || c.NUT.UPS == "" {
		bad("nut.host and nut.ups must not be empty")
	}
	if c.NUT.Port < 1 || c.NUT.Port > 65535 {
		bad("nut.port %d out of range", c.NUT.Port)
	}
	if c.NUT.Timeout <= 0 {
		bad("nut.timeout must be positive")
	}
	if c.Probe.Timeout <= 0 {
		bad("probe.timeout must be positive")
	}
	if c.Probe.DownAfter < 1 {
		bad("probe.down_after must be at least 1")
	}
	if err := ValidateProbeSpec(c.Probe.Default); err != nil {
		bad("probe.default: %v", err)
	}

	if len(c.Nodes) == 0 {
		bad("at least one node is required")
	}
	seen := map[string]bool{}
	for i, n := range c.Nodes {
		where := fmt.Sprintf("nodes[%d]", i)
		if !nameRe.MatchString(n.Name) {
			bad("%s: name %q must be letters, digits, '.', '_' or '-'", where, n.Name)
		} else if seen[strings.ToLower(n.Name)] {
			bad("%s: duplicate name %q", where, n.Name)
		}
		seen[strings.ToLower(n.Name)] = true
		if n.Host == "" {
			bad("%s (%s): host must not be empty", where, n.Name)
		}
		if _, err := ParseMAC(n.MAC); err != nil {
			bad("%s (%s): mac: %v", where, n.Name, err)
		}
		if n.Probe != "" {
			if err := ValidateProbeSpec(n.Probe); err != nil {
				bad("%s (%s): probe: %v", where, n.Name, err)
			}
		}
	}

	w := c.Wake
	if w.MinBattery < 0 || w.MinBattery > 100 {
		bad("wake.min_battery must be between 0 and 100")
	}
	if w.Delay < 0 || w.Stagger < 0 || w.MaxWait < 0 {
		bad("wake.delay, wake.stagger and wake.max_wait must not be negative")
	}
	if w.MaxWait > 0 && w.MaxWait < w.Delay {
		bad("wake.max_wait (%v) must not be shorter than wake.delay (%v)", w.MaxWait, w.Delay)
	}
	if w.RetryInterval <= 0 || w.GiveUpAfter <= 0 {
		bad("wake.retry_interval and wake.give_up_after must be positive")
	}
	if _, _, err := net.SplitHostPort(w.Broadcast); err != nil {
		bad("wake.broadcast %q: %v", w.Broadcast, err)
	}

	if c.Shutdown.WaitForNodes < 0 {
		bad("shutdown.wait_for_nodes must not be negative")
	}
	if len(c.Shutdown.Command) == 0 || c.Shutdown.Command[0] == "" {
		bad("shutdown.command must not be empty")
	}
	if c.History.MaxEvents < 100 {
		bad("history.max_events must be at least 100")
	}
	if c.History.ClockSyncTimeout < 0 {
		bad("history.clock_sync_timeout must not be negative")
	}
	if c.History.SampleKeep < time.Hour {
		bad("history.sample_keep must be at least 1h")
	}
	return errors.Join(errs...)
}

// ProbeSpec returns the probe spec to use for n.
func (c *Config) ProbeSpec(n Node) string {
	if n.Probe != "" {
		return n.Probe
	}
	return c.Probe.Default
}

// ValidateProbeSpec accepts "icmp" or "tcp:<port>".
func ValidateProbeSpec(spec string) error {
	_, err := probe.ParseSpec(spec)
	return err
}

// ParseMAC parses a 6-byte MAC address in colon, dash or dot notation.
func ParseMAC(s string) (net.HardwareAddr, error) {
	hw, err := net.ParseMAC(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	if len(hw) != 6 {
		return nil, fmt.Errorf("%q is not a 6-byte MAC address", s)
	}
	return hw, nil
}
