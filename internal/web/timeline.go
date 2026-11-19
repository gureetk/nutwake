package web

import (
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/humanize"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/samples"
)

const (
	// gapAfter is how long a hole in the recorder file must be before it is
	// taken to mean nothing was recording. A sample is written at least every
	// five minutes.
	gapAfter = 12 * time.Minute
	// restartGap is the same after a clean stop of the daemon, which says
	// exactly when recording ended: a restart of the service is quick, a
	// Pi that was off is not.
	restartGap = 3 * time.Minute
	// maxPoints bounds the battery line sent to the page.
	maxPoints = 1200
)

// Ranges the page can show.
const (
	RangeOutage = "outage"
	Range24h    = "24h"
	Range7d     = "7d"
)

// Span is a stretch of time.
type Span struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Segment is a stretch of time in one state. Kinds:
//
//	mains lane:  mains, battery, other, unknown, gap
//	node lanes:  up, off, unknown, gap
//	Pi lane:     up, gap
//
// "gap" is hatched on the page: nothing was recording.
type Segment struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	Kind string    `json:"kind"`
}

// Point is one battery reading.
type Point struct {
	T      time.Time `json:"t"`
	Charge float64   `json:"c"`
}

// NodeLane is one node's row.
type NodeLane struct {
	Name     string    `json:"name"`
	Host     string    `json:"host"`
	Segments []Segment `json:"segments"`
}

// Flag marks an event above the lanes.
type Flag struct {
	T      time.Time `json:"t"`
	Label  string    `json:"label"`
	Approx bool      `json:"approx,omitempty"`
}

// Projection is where the battery is heading on the current outage.
type Projection struct {
	From Point `json:"from"`
	To   Point `json:"to"`
}

// BatteryLane is the charge over time. Runs are separated by gaps.
type BatteryLane struct {
	Runs       [][]Point   `json:"runs"`
	WakeAt     float64     `json:"wake_at,omitempty"`
	ShutdownAt float64     `json:"shutdown_at,omitempty"`
	Projection *Projection `json:"projection,omitempty"`
}

// Timeline is everything the recorder section draws.
type Timeline struct {
	Range   string      `json:"range"`
	Ranges  []string    `json:"ranges"`
	From    time.Time   `json:"from"`
	To      time.Time   `json:"to"`
	Title   string      `json:"title"`
	Sub     string      `json:"sub"`
	HasData bool        `json:"has_data"`
	Mains   []Segment   `json:"mains"`
	Battery BatteryLane `json:"battery"`
	Nodes   []NodeLane  `json:"nodes"`
	Pi      []Segment   `json:"pi"`
	Bands   []Span      `json:"bands"`
	Flags   []Flag      `json:"flags"`
}

type nodeInfo struct{ Name, Host string }

type timelineIn struct {
	Range    string
	From, To time.Time
	Now      time.Time
	// Samples are those in [From, To], oldest first. Prior is the last one
	// before From, which says what state was in force at From.
	Samples []samples.Sample
	Prior   *samples.Sample
	// Live is the state right now, so the lanes reach the present moment.
	Live   *samples.Sample
	Events []events.Event // oldest first
	Nodes  []nodeInfo
	Rows   []OutageRow

	WakeAt, ShutdownAt float64
	Projection         *Projection
	ExpectedShutdown   *time.Time
}

type interval struct {
	from, to time.Time
	s        samples.Sample
	gap      bool
	prev     samples.Sample // the sample before a gap
}

func buildTimeline(in timelineIn) Timeline {
	end := in.To
	if in.Now.Before(end) {
		end = in.Now
	}
	tl := Timeline{
		Range: in.Range, From: in.From, To: in.To,
		Mains: []Segment{}, Pi: []Segment{}, Nodes: []NodeLane{}, Bands: []Span{}, Flags: []Flag{},
		Battery: BatteryLane{Runs: [][]Point{}, WakeAt: in.WakeAt, ShutdownAt: in.ShutdownAt, Projection: in.Projection},
	}

	pts := make([]samples.Sample, 0, len(in.Samples)+2)
	if in.Prior != nil {
		p := *in.Prior
		p.T = in.From
		pts = append(pts, p)
	}
	pts = append(pts, in.Samples...)
	if in.Live != nil && !in.Live.T.After(in.To) && in.Live.T.After(in.From) {
		if n := len(pts); n == 0 || in.Live.T.After(pts[n-1].T) {
			pts = append(pts, *in.Live)
		}
	}
	tl.HasData = len(pts) > 0

	var stops []time.Time
	for _, e := range in.Events {
		if e.Kind == events.KindStop {
			stops = append(stops, e.Time)
		}
	}
	ivs := intervals(pts, stops)

	for _, iv := range ivs {
		a, b := clip(iv.from, iv.to, in.From, end)
		if !b.After(a) {
			continue
		}
		if iv.gap {
			tl.Mains = append(tl.Mains, Segment{a, b, "gap"})
			tl.Pi = append(tl.Pi, Segment{a, b, "gap"})
			continue
		}
		tl.Mains = append(tl.Mains, Segment{a, b, mainsKind(iv.s.UPS)})
		tl.Pi = append(tl.Pi, Segment{a, b, "up"})
	}
	tl.Mains, tl.Pi = merge(tl.Mains), merge(tl.Pi)

	for _, n := range in.Nodes {
		lane := NodeLane{Name: n.Name, Host: n.Host, Segments: []Segment{}}
		for _, iv := range ivs {
			a, b := clip(iv.from, iv.to, in.From, end)
			if !b.After(a) {
				continue
			}
			k := ""
			if iv.gap {
				// a node that was off when recording stopped is still off
				// when it resumes unless it says otherwise; one that was up
				// is simply unknown
				k = "gap"
				if nodeKind(iv.prev, n.Name) == "off" {
					k = "off"
				}
			} else {
				k = nodeKind(iv.s, n.Name)
			}
			lane.Segments = append(lane.Segments, Segment{a, b, k})
		}
		lane.Segments = merge(lane.Segments)
		tl.Nodes = append(tl.Nodes, lane)
	}

	tl.Battery.Runs = batteryRuns(pts, ivs, in.From, end)

	for _, r := range in.Rows {
		to := in.Now
		if r.End != nil {
			to = *r.End
		}
		if a, b := clip(r.Start, to, in.From, in.To); b.After(a) {
			tl.Bands = append(tl.Bands, Span{a, b})
		}
	}
	tl.Flags = flags(in)
	return tl
}

// intervals splits the recorded samples into stretches that were recorded and
// stretches that were not.
func intervals(pts []samples.Sample, stops []time.Time) []interval {
	var out []interval
	for i := 0; i+1 < len(pts); i++ {
		a, b := pts[i], pts[i+1]
		stop, stopped := lastBetween(stops, a.T, b.T)
		start, long := a.T, b.T.Sub(a.T) > gapAfter
		if stopped {
			start, long = stop, b.T.Sub(stop) > restartGap
		}
		if !long {
			out = append(out, interval{from: a.T, to: b.T, s: a})
			continue
		}
		if start.After(a.T) {
			out = append(out, interval{from: a.T, to: start, s: a})
		}
		out = append(out, interval{from: start, to: b.T, gap: true, prev: a})
	}
	return out
}

func lastBetween(ts []time.Time, a, b time.Time) (time.Time, bool) {
	var found time.Time
	ok := false
	for _, t := range ts {
		if t.After(a) && !t.After(b) && (!ok || t.After(found)) {
			found, ok = t, true
		}
	}
	return found, ok
}

func clip(a, b, lo, hi time.Time) (time.Time, time.Time) {
	if a.Before(lo) {
		a = lo
	}
	if b.After(hi) {
		b = hi
	}
	return a, b
}

func mainsKind(st nut.State) string {
	switch st {
	case nut.StateOnline:
		return "mains"
	case nut.StateOnBattery, nut.StateLowBattery:
		return "battery"
	case nut.StateOther:
		return "other"
	}
	return "unknown"
}

func nodeKind(s samples.Sample, name string) string {
	for _, n := range s.Up {
		if n == name {
			return "up"
		}
	}
	for _, n := range s.Down {
		if n == name {
			return "off"
		}
	}
	return "unknown"
}

// merge joins neighbouring segments of the same kind.
func merge(in []Segment) []Segment {
	out := make([]Segment, 0, len(in))
	for _, s := range in {
		if n := len(out); n > 0 && out[n-1].Kind == s.Kind && !s.From.After(out[n-1].To) {
			out[n-1].To = s.To
			continue
		}
		out = append(out, s)
	}
	return out
}

// batteryRuns collects the charge readings into lines, one per recorded
// stretch, thinned to a size the page can draw.
func batteryRuns(pts []samples.Sample, ivs []interval, from, end time.Time) [][]Point {
	gapBefore := map[time.Time]bool{}
	for _, iv := range ivs {
		if iv.gap {
			gapBefore[iv.to] = true
		}
	}
	runs := [][]Point{}
	var run []Point
	flush := func() {
		if len(run) > 0 {
			runs = append(runs, decimate(run, maxPoints))
		}
		run = nil
	}
	for _, p := range pts {
		if gapBefore[p.T] {
			flush()
		}
		if p.Charge == nil || p.T.Before(from) || p.T.After(end) {
			continue
		}
		run = append(run, Point{T: p.T, Charge: *p.Charge})
	}
	flush()
	return runs
}

// decimate keeps at most max points, always the first and last.
func decimate(in []Point, max int) []Point {
	if len(in) <= max {
		return in
	}
	k := int(math.Ceil(float64(len(in)) / float64(max)))
	out := make([]Point, 0, max+1)
	for i := 0; i < len(in); i += k {
		out = append(out, in[i])
	}
	if last := in[len(in)-1]; out[len(out)-1] != last {
		out = append(out, last)
	}
	return out
}

// flags marks the events worth a label above the lanes.
func flags(in timelineIn) []Flag {
	out := []Flag{} // not nil, so the JSON has [] and not null
	add := func(t time.Time, label string) {
		if !t.Before(in.From) && !t.After(in.To) {
			out = append(out, Flag{T: t, Label: label})
		}
	}
	for i, e := range in.Events {
		switch e.Kind {
		case events.KindPowerLost:
			add(e.Time, "Power lost")
		case events.KindShutdownBegan:
			add(e.Time, "Shutdown starts")
		case events.KindPowerRestored:
			add(e.Time, "Power back")
			// the first wake packet after power came back
			for _, e2 := range in.Events[i+1:] {
				if e2.Kind == events.KindPowerLost {
					break
				}
				if e2.Kind == events.KindWakeSent {
					add(e2.Time, "Nodes woken")
					break
				}
			}
		}
	}
	if in.ExpectedShutdown != nil {
		if !in.ExpectedShutdown.Before(in.From) && !in.ExpectedShutdown.After(in.To) {
			out = append(out, Flag{T: *in.ExpectedShutdown, Label: "Shutdown expected", Approx: true})
		}
	}
	if in.Range == RangeOutage && in.Live != nil && in.Live.UPS != nut.StateOnline && !in.Now.After(in.To) {
		out = append(out, Flag{T: in.Now, Label: "Now"})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	return out
}

// chooseWindow picks the range to show and the time it covers.
func chooseWindow(rng string, rows []OutageRow, evs []events.Event, now time.Time, expected *time.Time) (string, time.Time, time.Time) {
	if rng == "" || rng == "auto" {
		rng = Range24h
		if len(rows) > 0 && (rows[0].InProgress || rows[0].End == nil || now.Sub(*rows[0].End) < 48*time.Hour) {
			rng = RangeOutage
		}
	}
	switch rng {
	case Range7d:
		return Range7d, now.Add(-7 * 24 * time.Hour), now
	case RangeOutage:
		if len(rows) == 0 {
			break
		}
		r := rows[0]
		if r.InProgress {
			to := now
			if expected != nil && expected.After(to) {
				to = *expected
			}
			from := r.Start.Add(-15 * time.Minute)
			to = to.Add(10 * time.Minute)
			if to.Sub(from) < 30*time.Minute {
				to = from.Add(30 * time.Minute)
			}
			return RangeOutage, floor(from, 5*time.Minute), ceil(to, 5*time.Minute)
		}
		last := r.Start
		if r.End != nil {
			last = *r.End
		}
		// include the nodes coming back, which happens after power does
		for _, e := range evs {
			if e.Kind == events.KindWakeOK && e.Time.After(last) && e.Time.Sub(last) < 6*time.Hour {
				last = e.Time
			}
		}
		pad := last.Sub(r.Start) * 15 / 100
		pad = max(10*time.Minute, min(pad, 45*time.Minute))
		return RangeOutage, floor(r.Start.Add(-pad), 5*time.Minute), ceil(last.Add(pad), 5*time.Minute)
	}
	return Range24h, now.Add(-24 * time.Hour), now
}

func floor(t time.Time, d time.Duration) time.Time { return t.Truncate(d) }

func ceil(t time.Time, d time.Duration) time.Time {
	if f := t.Truncate(d); f.Equal(t) {
		return t
	} else {
		return f.Add(d)
	}
}

// titles writes the heading and the sentence under it.
func titles(tl *Timeline, rows []OutageRow, now time.Time, projected bool) {
	switch tl.Range {
	case RangeOutage:
		if len(rows) == 0 {
			return
		}
		r := rows[0]
		if r.InProgress {
			tl.Title = "This outage"
			tl.Sub = fmt.Sprintf("Started at %s.", tokTime(r.Start))
			if projected {
				tl.Sub += " The dotted line shows where the battery is heading at the current rate."
			}
			return
		}
		tl.Title = "Outage on " + tokDate(r.Start)
		sub := fmt.Sprintf("Power was out for %s.", humanize.Duration(time.Duration(r.DurationS)*time.Second))
		if r.Approximate {
			sub = fmt.Sprintf("Power was out for about %s.", humanize.Duration(time.Duration(r.DurationS)*time.Second))
		}
		switch {
		case r.shutdownAt != nil && r.wokeAt != nil:
			sub += fmt.Sprintf(" The nodes shut down at %s and were woken again by %s.", tokTime(*r.shutdownAt), tokTime(*r.wokeAt))
		case r.shutdownAt != nil:
			sub += fmt.Sprintf(" The nodes shut down at %s.", tokTime(*r.shutdownAt))
		default:
			sub += " The nodes stayed up."
		}
		for _, s := range tl.Pi {
			if s.Kind == "gap" && s.To.Sub(s.From) > 5*time.Minute {
				sub += fmt.Sprintf(" The Pi was off from %s to %s, so nothing was recorded then.", tokTime(s.From), tokTime(s.To))
				break
			}
		}
		tl.Sub = sub
	default:
		span := "24 hours"
		if tl.Range == Range7d {
			span = "7 days"
		}
		tl.Title = "Last " + span
		n, total := 0, time.Duration(0)
		for _, r := range rows {
			end := now
			if r.End != nil {
				end = *r.End
			}
			if end.After(tl.From) {
				n++
				total += time.Duration(r.DurationS) * time.Second
			}
		}
		switch n {
		case 0:
			tl.Sub = "No power cuts."
		case 1:
			tl.Sub = fmt.Sprintf("1 power cut, %s on battery.", humanize.Duration(total))
		default:
			tl.Sub = fmt.Sprintf("%d power cuts, %s on battery in total.", n, humanize.Duration(total))
		}
	}
}
