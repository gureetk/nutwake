package config

import (
	"strings"
	"testing"
	"time"
)

const minimal = `
nodes:
  - name: pve1
    host: 10.10.0.10
    mac: "aa:bb:cc:dd:ee:01"
`

func TestParseMinimalUsesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(minimal))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NUT.Addr() != "127.0.0.1:3493" {
		t.Errorf("NUT addr = %q", cfg.NUT.Addr())
	}
	if cfg.PollInterval != 5*time.Second || cfg.Wake.MinBattery != 15 || cfg.Wake.MaxWait != 2*time.Hour {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	if got := cfg.ProbeSpec(cfg.Nodes[0]); got != "icmp" {
		t.Errorf("ProbeSpec = %q", got)
	}
}

func TestParseOverridesAndDurations(t *testing.T) {
	cfg, err := Parse([]byte(`
listen: 0.0.0.0:9000
poll_interval: 10s
wake:
  min_battery: 40
  delay: 2m
  give_up_after: 1h
shutdown:
  wait_for_nodes: 90s
  command: [/usr/bin/systemctl, poweroff]
nodes:
  - name: a
    host: a.lan
    mac: AA-BB-CC-DD-EE-01
    probe: tcp:8006
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "0.0.0.0:9000" || cfg.PollInterval != 10*time.Second {
		t.Errorf("listen/poll wrong: %+v", cfg)
	}
	if cfg.Wake.Delay != 2*time.Minute || cfg.Wake.GiveUpAfter != time.Hour || cfg.Wake.MinBattery != 40 {
		t.Errorf("wake wrong: %+v", cfg.Wake)
	}
	if cfg.Shutdown.WaitForNodes != 90*time.Second || cfg.Shutdown.Command[1] != "poweroff" {
		t.Errorf("shutdown wrong: %+v", cfg.Shutdown)
	}
	if got := cfg.ProbeSpec(cfg.Nodes[0]); got != "tcp:8006" {
		t.Errorf("per-node probe = %q", got)
	}
}

func TestParseRejectsProblems(t *testing.T) {
	cases := map[string]string{
		"unknown key":    minimal + "\nbogus: 1\n",
		"no nodes":       "listen: 127.0.0.1:1\n",
		"bad mac":        "nodes:\n  - {name: a, host: h, mac: nope}\n",
		"short mac":      "nodes:\n  - {name: a, host: h, mac: \"00:11:22:33:44:55:66:77\"}\n",
		"duplicate name": "nodes:\n  - {name: a, host: h, mac: \"aa:bb:cc:dd:ee:01\"}\n  - {name: A, host: h2, mac: \"aa:bb:cc:dd:ee:02\"}\n",
		"bad name":       "nodes:\n  - {name: \"a b\", host: h, mac: \"aa:bb:cc:dd:ee:01\"}\n",
		"bad probe":      "nodes:\n  - {name: a, host: h, mac: \"aa:bb:cc:dd:ee:01\", probe: http}\n",
		"bad tcp port":   "nodes:\n  - {name: a, host: h, mac: \"aa:bb:cc:dd:ee:01\", probe: \"tcp:99999\"}\n",
		"battery range":  minimal + "\nwake: {min_battery: 150}\n",
		"max_wait short": minimal + "\nwake: {delay: 5m, max_wait: 1m}\n",
		"negative wait":  minimal + "\nwake: {max_wait: -1h}\n",
		"bad duration":   minimal + "\npoll_interval: soon\n",
		"poll too fast":  minimal + "\npoll_interval: 100ms\n",
		"bad listen":     minimal + "\nlisten: nonsense\n",
		"bad broadcast":  minimal + "\nwake: {broadcast: \"10.0.0.255\"}\n",
		"short keep":     minimal + "\nhistory: {sample_keep: 10m}\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(in)); err == nil {
				t.Fatalf("expected an error for %q", name)
			}
		})
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	_, err := Parse([]byte("nodes:\n  - {name: \"a b\", host: \"\", mac: nope}\n"))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"name", "host", "mac"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestParseMAC(t *testing.T) {
	for _, ok := range []string{"aa:bb:cc:dd:ee:ff", "AA-BB-CC-DD-EE-FF", "aabb.ccdd.eeff", " aa:bb:cc:dd:ee:ff "} {
		if _, err := ParseMAC(ok); err != nil {
			t.Errorf("ParseMAC(%q): %v", ok, err)
		}
	}
	if _, err := ParseMAC("aa:bb:cc"); err == nil {
		t.Error("expected error for short MAC")
	}
}

func TestDataFiles(t *testing.T) {
	c := Default()
	c.DataDir = "/srv/nw"
	if got := c.EventsFile(); got != "/srv/nw/events.jsonl" {
		t.Errorf("EventsFile = %q", got)
	}
	if got := c.SamplesFile(); got != "/srv/nw/samples.jsonl" {
		t.Errorf("SamplesFile = %q", got)
	}
}
