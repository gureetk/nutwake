package web

import (
	"strings"

	"github.com/gureetk/nutwake/internal/config"
)

// Settings is the configuration as the page shows it. It is read only: the
// shutdown command runs as root, so the page must never be able to change it.
type Settings struct {
	NUT      string         `json:"nut"`
	UPS      string         `json:"ups"`
	Poll     string         `json:"poll_interval"`
	Nodes    []SettingsNode `json:"nodes"`
	Probe    string         `json:"probe"`
	Wake     []Setting      `json:"wake"`
	Shutdown []Setting      `json:"shutdown"`
	History  []Setting      `json:"history"`
}

// SettingsNode is a configured node. The MAC address is left out.
type SettingsNode struct {
	Name  string `json:"name"`
	Host  string `json:"host"`
	Probe string `json:"probe"`
}

// Setting is one labelled value.
type Setting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Hint  string `json:"hint,omitempty"`
}

func settingsOf(c *config.Config) Settings {
	s := Settings{
		NUT: c.NUT.Addr(), UPS: c.NUT.UPS, Poll: dur(c.PollInterval),
		Probe: c.Probe.Default,
	}
	for _, n := range c.Nodes {
		s.Nodes = append(s.Nodes, SettingsNode{n.Name, n.Host, c.ProbeSpec(n)})
	}
	w := c.Wake
	s.Wake = []Setting{
		{"Wake nodes at battery", pct(w.MinBattery), "Charge needed before nodes are woken"},
		{"Power steady for", dur(w.Delay), "How long power must stay on first"},
		{"Wake anyway after", orOff(w.MaxWait), "Wakes even if the battery has not reached the level above"},
		{"Space between nodes", dur(w.Stagger), ""},
		{"Resend after", dur(w.RetryInterval), ""},
		{"Give up after", dur(w.GiveUpAfter), ""},
	}
	s.Shutdown = []Setting{
		{"Wait for nodes to power off", dur(c.Shutdown.WaitForNodes), "Longest wait before this Pi halts anyway"},
		{"Halt command", strings.Join(c.Shutdown.Command, " "), ""},
	}
	s.History = []Setting{
		{"Events kept", itoa(c.History.MaxEvents), ""},
		{"Battery history kept", dur(c.History.SampleKeep), ""},
	}
	return s
}
