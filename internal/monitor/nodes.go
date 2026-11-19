package monitor

import (
	"fmt"
	"time"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/humanize"
	"github.com/gureetk/nutwake/internal/probe"
)

// NodeState is what is known about a node.
type NodeState string

const (
	NodeUnknown NodeState = "unknown" // not enough probes yet
	NodeUp      NodeState = "up"
	NodeDown    NodeState = "down"
)

type node struct {
	cfg    config.Node
	probe  string // probe spec, for display
	prober probe.Prober

	state NodeState
	fails int
	since time.Time
}

// applyNode records the result of one probe. A node is called down only after
// probe.down_after failures in a row, and is called up on the first success.
// The first answer for a node is recorded without an event: nothing changed,
// the daemon just learned how things are.
func (m *Monitor) applyNode(n *node, ok bool, now time.Time) {
	if n.state == "" {
		n.state = NodeUnknown
	}
	if ok {
		n.fails = 0
		switch n.state {
		case NodeUnknown:
			n.state, n.since = NodeUp, now
		case NodeDown:
			downFor := now.Sub(n.since)
			n.state, n.since = NodeUp, now
			m.emit(events.LevelOK, events.KindNodeUp, n.cfg.Name,
				fmt.Sprintf("%s is up again after %s.", n.cfg.Name, humanize.Duration(downFor)),
				map[string]any{"down_for_s": int(downFor.Seconds())}, now)
		}
		return
	}

	n.fails++
	if n.fails < m.cfg.Probe.DownAfter {
		return
	}
	switch n.state {
	case NodeUnknown:
		n.state, n.since = NodeDown, now
	case NodeUp:
		n.state, n.since = NodeDown, now
		data := map[string]any{}
		if m.outage != nil {
			data["during_outage"] = true
		}
		m.emit(events.LevelWarn, events.KindNodeDown, n.cfg.Name,
			fmt.Sprintf("%s stopped responding.", n.cfg.Name), data, now)
	}
}

// upNodes lists the nodes currently seen up.
func (m *Monitor) upNodes() []string {
	var out []string
	for _, n := range m.nodes {
		if n.state == NodeUp {
			out = append(out, n.cfg.Name)
		}
	}
	return out
}
