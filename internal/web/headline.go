package web

import (
	"fmt"
	"strings"
	"time"

	"github.com/gureetk/nutwake/internal/humanize"
	"github.com/gureetk/nutwake/internal/monitor"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/samples"
	"github.com/gureetk/nutwake/internal/waker"
)

// Headline is the big line at the top: what is going on, in words.
type Headline struct {
	Tone  string `json:"tone"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

type headlineIn struct {
	Snap             monitor.Snapshot
	Wake             waker.Status
	Rows             []OutageRow
	ExpectedShutdown *time.Time
	ShutdownCharge   float64
	NUTAddr          string
	Now              time.Time
}

func buildHeadline(in headlineIn) Headline {
	u := in.Snap.UPS
	switch {
	case !u.Reachable:
		return Headline{ToneCrit, "Can't reach the UPS", fmt.Sprintf(
			"nutwake cannot read the UPS from the NUT server at %s. Power cuts are not recorded until it is back. %s",
			in.NUTAddr, nodesSentence(in.Snap.Nodes))}

	case u.State == nut.StateLowBattery:
		return Headline{ToneCrit, "Battery is low", "The UPS says the battery is nearly empty. The nodes and this Pi are shutting down. " +
			nodesSentence(in.Snap.Nodes)}

	case u.State == nut.StateOnBattery:
		return Headline{ToneWarn, "Running on battery", onBatteryText(in)}

	case u.State != nut.StateOnline:
		return Headline{ToneWarn, "The UPS is not on mains", fmt.Sprintf(
			"It reports status %q. nutwake only tracks mains and battery power.", u.Status)}
	}

	if in.Wake.Active {
		return Headline{ToneWait, "Power is back", wakeText(in.Wake)}
	}
	text := nodesSentence(in.Snap.Nodes)
	tone := ToneOK
	title := "Power is on"
	if down := nodesIn(in.Snap.Nodes, monitor.NodeDown); len(down) > 0 {
		tone = ToneWarn
	}
	if len(in.Rows) > 0 && !in.Rows[0].InProgress && in.Rows[0].End != nil {
		text = strings.TrimSpace(text + " The last outage was " + humanize.Ago(in.Now.Sub(*in.Rows[0].End)) + ".")
	} else if len(in.Rows) == 0 {
		text = strings.TrimSpace(text + " No power cuts recorded yet.")
	}
	return Headline{tone, title, text}
}

func onBatteryText(in headlineIn) string {
	var sb strings.Builder
	if o := in.Snap.Outage; o != nil {
		fmt.Fprintf(&sb, "On battery for %s.", humanize.Duration(in.Now.Sub(o.Started)))
	} else {
		sb.WriteString("On battery.")
	}
	up := nodesIn(in.Snap.Nodes, monitor.NodeUp)
	switch {
	case len(up) > 0 && in.ExpectedShutdown != nil:
		fmt.Fprintf(&sb, " %s shut down at %s%%, around %s.", humanize.List(up), humanize.Percent(in.ShutdownCharge), tokTime(*in.ExpectedShutdown))
	case len(up) > 0:
		fmt.Fprintf(&sb, " %s shut down at %s%%.", humanize.List(up), humanize.Percent(in.ShutdownCharge))
	default:
		sb.WriteString(" " + nodesSentence(in.Snap.Nodes))
	}
	return sb.String()
}

func wakeText(w waker.Status) string {
	var names []string
	for _, n := range w.Nodes {
		names = append(names, n.Name)
	}
	list := humanize.List(names)
	if w.Waiting != "" {
		return fmt.Sprintf("%s %s will be woken then.", w.Message, capitalize(list))
	}
	return "Sending wake packets. Waiting for " + list + " to come up."
}

// nodesSentence summarises the nodes in one sentence, or "" if there are none.
func nodesSentence(ns []monitor.NodeStatus) string {
	if len(ns) == 0 {
		return ""
	}
	up := nodesIn(ns, monitor.NodeUp)
	down := nodesIn(ns, monitor.NodeDown)
	switch {
	case len(down) > 0:
		verb := "is"
		if len(down) > 1 {
			verb = "are"
		}
		return fmt.Sprintf("%s %s down.", capitalize(humanize.List(down)), verb)
	case len(up) == len(ns) && len(ns) == 1:
		return fmt.Sprintf("%s is up.", capitalize(up[0]))
	case len(up) == len(ns) && len(ns) == 2:
		return "Both nodes are up."
	case len(up) == len(ns):
		return fmt.Sprintf("All %d nodes are up.", len(ns))
	}
	return "Checking the nodes."
}

func nodesIn(ns []monitor.NodeStatus, st monitor.NodeState) []string {
	var out []string
	for _, n := range ns {
		if n.State == st {
			out = append(out, n.Name)
		}
	}
	return out
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	// node names are lower case by habit; keep an intentional capital as is
	return strings.ToUpper(s[:1]) + s[1:]
}

// expectedShutdown estimates when the battery will reach the shutdown level,
// from how fast it has been falling, or failing that from the runtime the UPS
// reports. It returns nil when not on battery or when there is nothing to go on.
func expectedShutdown(u monitor.UPSStatus, recent []samples.Sample, low float64, now time.Time) *time.Time {
	if u.State != nut.StateOnBattery || u.Charge == nil || *u.Charge <= low {
		return nil
	}
	var first, last *samples.Sample
	for i := range recent {
		s := &recent[i]
		if s.Charge == nil || s.UPS != nut.StateOnBattery || now.Sub(s.T) > 10*time.Minute {
			continue
		}
		if first == nil {
			first = s
		}
		last = s
	}
	if first != nil && last != nil && last.T.Sub(first.T) >= time.Minute {
		rate := (*first.Charge - *last.Charge) / last.T.Sub(first.T).Minutes() // percent per minute
		if rate > 0.05 {
			mins := (*u.Charge - low) / rate
			return capTime(now, mins)
		}
	}
	if u.RuntimeSeconds != nil && *u.RuntimeSeconds > 0 {
		mins := *u.RuntimeSeconds / 60 * (*u.Charge - low) / *u.Charge
		return capTime(now, mins)
	}
	return nil
}

func capTime(now time.Time, minutes float64) *time.Time {
	if minutes <= 0 || minutes > 24*60 {
		return nil
	}
	t := now.Add(time.Duration(minutes * float64(time.Minute)))
	return &t
}
