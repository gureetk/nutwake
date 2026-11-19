package web

import (
	"fmt"
	"sort"
	"time"

	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/humanize"
	"github.com/gureetk/nutwake/internal/monitor"
)

// Tones, shared by the headline, the outage table and the log.
const (
	ToneOK   = "ok"
	ToneWarn = "warn"
	ToneCrit = "crit"
	ToneWait = "wait"
)

// OutageRow is one power cut, as the table shows it.
type OutageRow struct {
	Start       time.Time  `json:"start"`
	End         *time.Time `json:"end,omitempty"`
	DurationS   int        `json:"duration_s"`
	Approximate bool       `json:"approximate,omitempty"`
	InProgress  bool       `json:"in_progress,omitempty"`
	StartCharge *float64   `json:"start_charge,omitempty"`
	MinCharge   *float64   `json:"min_charge,omitempty"`
	Tone        string     `json:"tone"`
	Result      string     `json:"result"`

	shutdownAt *time.Time
	wokeAt     *time.Time
}

// outageInput is what the table is built from.
type outageInput struct {
	Events []events.Event // oldest first
	Active *monitor.OutageStatus
	// ExpectedShutdown is when the nodes will be shut down if power stays
	// out, if known.
	ExpectedShutdown *time.Time
	ShutdownCharge   float64
	WakeActive       bool
	Now              time.Time
}

// buildOutages turns the history into one row per power cut, newest first.
func buildOutages(in outageInput) []OutageRow {
	evs := in.Events
	var rows []OutageRow
	for i, e := range evs {
		if e.Kind != events.KindPowerLost {
			continue
		}
		row := OutageRow{Start: e.Time}
		row.StartCharge = dataNum(e.Data, "charge")

		// An outage runs until the first power_restored, and nothing after the
		// next power_lost belongs to it.
		next, lastLost := len(evs), true
		for j := i + 1; j < len(evs); j++ {
			if evs[j].Kind == events.KindPowerLost {
				next, lastLost = j, false
				break
			}
		}
		until := in.Now // events up to here belong to this outage and its wake-up
		if next < len(evs) {
			until = evs[next].Time
		}
		var restored *events.Event
		for j := i + 1; j < next; j++ {
			if evs[j].Kind == events.KindPowerRestored {
				restored = &evs[j]
				break
			}
		}
		hi := until // the power cut itself, for finding the shutdown
		if restored != nil {
			end := restored.Time
			row.End, hi = &end, end
			row.DurationS = int(end.Sub(e.Time).Seconds())
			if d := dataNum(restored.Data, "duration_s"); d != nil {
				row.DurationS = int(*d)
			}
			row.Approximate, _ = restored.Data["approximate"].(bool)
			if v := dataNum(restored.Data, "start_charge"); v != nil && *v > 0 {
				row.StartCharge = v
			}
			row.MinCharge = dataNum(restored.Data, "min_charge")
		} else if lastLost && in.Active != nil {
			row.InProgress = true
			row.DurationS = int(in.Now.Sub(e.Time).Seconds())
			c, m := in.Active.StartCharge, in.Active.MinCharge
			if row.StartCharge == nil && c > 0 {
				row.StartCharge = &c
			}
			row.MinCharge = &m
		}

		var failed []string
		for _, e2 := range evs[i+1:] {
			if e2.Time.After(until) {
				break
			}
			switch {
			case e2.Kind == events.KindShutdownBegan && row.shutdownAt == nil && !e2.Time.After(hi):
				t := e2.Time
				row.shutdownAt = &t
			case e2.Kind == events.KindWakeOK && row.shutdownAt != nil:
				t := e2.Time
				row.wokeAt = &t
			case e2.Kind == events.KindWakeFailed && row.shutdownAt != nil && e2.Node != "":
				failed = append(failed, e2.Node)
			}
		}
		describe(&row, failed, in, lastLost)
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Start.After(rows[j].Start) })
	return rows
}

func describe(row *OutageRow, failed []string, in outageInput, last bool) {
	switch {
	case row.InProgress && row.shutdownAt != nil:
		row.Tone = ToneWarn
		row.Result = fmt.Sprintf("In progress. The nodes shut down at %s.", tokTime(*row.shutdownAt))
	case row.InProgress:
		row.Tone = ToneWarn
		row.Result = "In progress."
		if in.ShutdownCharge > 0 {
			row.Result = fmt.Sprintf("In progress. The nodes shut down at %s%%.", humanize.Percent(in.ShutdownCharge))
		}
	case row.End == nil:
		row.Tone = ToneWarn
		row.Result = "The end of this outage was not recorded."
	case row.shutdownAt == nil:
		row.Tone = ToneOK
		row.Result = "Rode through. The nodes stayed up."
	case len(failed) > 0:
		row.Tone = ToneCrit
		row.Result = fmt.Sprintf("The nodes shut down. %s did not wake.", humanize.List(failed))
	case row.wokeAt != nil:
		row.Tone = ToneWarn
		row.Result = fmt.Sprintf("The nodes shut down, then woke again by %s.", tokTime(*row.wokeAt))
	case last && in.WakeActive:
		row.Tone = ToneWarn
		row.Result = "The nodes shut down. Waiting to wake them."
	default:
		row.Tone = ToneWarn
		row.Result = "The nodes shut down."
	}
}

func dataNum(m map[string]any, key string) *float64 {
	if v, ok := m[key].(float64); ok {
		return &v
	}
	return nil
}
