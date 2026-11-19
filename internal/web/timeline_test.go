package web

import (
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/nut"
	"github.com/gureetk/nutwake/internal/samples"
)

var t0 = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

func at(min float64) time.Time { return t0.Add(time.Duration(min * float64(time.Minute))) }

func chg(v float64) *float64 { return &v }

func smp(min float64, st nut.State, charge float64, up, down []string) samples.Sample {
	return samples.Sample{T: at(min), UPS: st, Charge: chg(charge), Up: up, Down: down}
}

var both = []string{"a", "b"}

func kinds(segs []Segment) []string {
	var out []string
	for _, s := range segs {
		out = append(out, s.Kind)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func laneKinds(tl Timeline, name string) []string {
	for _, n := range tl.Nodes {
		if n.Name == name {
			return kinds(n.Segments)
		}
	}
	return nil
}

func baseInput(pts []samples.Sample, evs []events.Event) timelineIn {
	return timelineIn{
		Range: RangeOutage, From: at(0), To: at(120), Now: at(120),
		Samples: pts, Events: evs,
		Nodes: []nodeInfo{{"a", "10.0.0.1"}, {"b", "10.0.0.2"}},
	}
}

func TestTimelineContinuousRecordingHasNoGap(t *testing.T) {
	var pts []samples.Sample
	for m := 0; m <= 30; m += 5 {
		pts = append(pts, smp(float64(m), nut.StateOnline, 100, both, nil))
	}
	in := baseInput(pts, nil)
	in.To, in.Now = at(30), at(30)
	tl := buildTimeline(in)
	if got := kinds(tl.Mains); !eq(got, []string{"mains"}) {
		t.Fatalf("mains = %v, want one segment", got)
	}
	if got := kinds(tl.Pi); !eq(got, []string{"up"}) {
		t.Fatalf("pi = %v", got)
	}
	if len(tl.Battery.Runs) != 1 {
		t.Fatalf("battery runs = %d, want 1", len(tl.Battery.Runs))
	}
	if !tl.HasData {
		t.Fatal("HasData is false")
	}
}

func TestTimelineLongHoleWithoutStopEventIsAGap(t *testing.T) {
	// a power cut took the Pi down with no clean stop recorded: the hole is
	// only noticed once it is longer than gapAfter
	pts := []samples.Sample{
		smp(0, nut.StateOnline, 100, both, nil),
		smp(5, nut.StateOnBattery, 98, both, nil),
		smp(60, nut.StateOnline, 40, nil, nil),
	}
	tl := buildTimeline(baseInput(pts, nil))
	if got := kinds(tl.Pi); !eq(got, []string{"up", "gap"}) {
		t.Fatalf("pi = %v, want up then gap", got)
	}
	if !tl.Pi[1].From.Equal(at(5)) || !tl.Pi[1].To.Equal(at(60)) {
		t.Fatalf("gap = %v to %v", tl.Pi[1].From, tl.Pi[1].To)
	}
	if got := kinds(tl.Mains); !eq(got, []string{"mains", "gap"}) {
		t.Fatalf("mains = %v, want mains then gap", got)
	}
	if len(tl.Battery.Runs) != 2 {
		t.Fatalf("battery runs = %d, want 2 (split by the gap)", len(tl.Battery.Runs))
	}
}

func TestTimelineShortHoleIsNotAGap(t *testing.T) {
	// a busy poll can leave a few minutes between samples; that is not an outage
	pts := []samples.Sample{
		smp(0, nut.StateOnline, 100, both, nil),
		smp(10, nut.StateOnline, 100, both, nil),
	}
	in := baseInput(pts, nil)
	in.To, in.Now = at(10), at(10)
	tl := buildTimeline(in)
	if got := kinds(tl.Pi); !eq(got, []string{"up"}) {
		t.Fatalf("pi = %v, want no gap for a 10 minute hole", got)
	}
}

func TestTimelineStopEventShortensTheGap(t *testing.T) {
	// the daemon said when it stopped, so a 4 minute reboot shows as a gap
	// even though it is shorter than gapAfter
	pts := []samples.Sample{
		smp(0, nut.StateOnline, 100, both, nil),
		smp(9, nut.StateOnline, 100, both, nil),
	}
	evs := []events.Event{{Time: at(4), Kind: events.KindStop}}
	in := baseInput(pts, evs)
	in.To, in.Now = at(9), at(9)
	tl := buildTimeline(in)
	if got := kinds(tl.Pi); !eq(got, []string{"up", "gap"}) {
		t.Fatalf("pi = %v, want up then gap", got)
	}
	if !tl.Pi[1].From.Equal(at(4)) {
		t.Fatalf("the gap should start at the stop event, got %v", tl.Pi[1].From)
	}
}

func TestTimelineQuickServiceRestartIsNotAGap(t *testing.T) {
	pts := []samples.Sample{
		smp(0, nut.StateOnline, 100, both, nil),
		smp(2, nut.StateOnline, 100, both, nil),
	}
	evs := []events.Event{{Time: at(1), Kind: events.KindStop}}
	in := baseInput(pts, evs)
	in.To, in.Now = at(2), at(2)
	tl := buildTimeline(in)
	if got := kinds(tl.Pi); !eq(got, []string{"up"}) {
		t.Fatalf("pi = %v, want no gap for a restart of a minute", got)
	}
}

func TestTimelineNodeLanesHoldOffThroughAGap(t *testing.T) {
	// both nodes were shut down before the Pi halted. When recording
	// resumes, a node that was off is still off; one that was up is unknown
	pts := []samples.Sample{
		smp(0, nut.StateOnline, 100, both, nil),
		smp(20, nut.StateOnBattery, 20, []string{"b"}, []string{"a"}),
		smp(80, nut.StateOnline, 30, nil, nil),
	}
	tl := buildTimeline(baseInput(pts, nil))
	a := laneKinds(tl, "a")
	b := laneKinds(tl, "b")
	if len(a) == 0 || a[len(a)-2] != "off" && a[len(a)-1] != "off" {
		t.Fatalf("node a (off before the gap) lane = %v, want it to stay off", a)
	}
	foundGap := false
	for _, k := range b {
		if k == "gap" {
			foundGap = true
		}
	}
	if !foundGap {
		t.Fatalf("node b (up before the gap) lane = %v, want a gap", b)
	}
}

func TestTimelineClipsToTheWindow(t *testing.T) {
	pts := []samples.Sample{
		smp(10, nut.StateOnBattery, 90, both, nil),
		smp(20, nut.StateOnBattery, 80, both, nil),
	}
	prior := smp(-30, nut.StateOnline, 100, both, nil)
	in := baseInput(pts, nil)
	in.Prior = &prior
	in.From, in.To, in.Now = at(0), at(20), at(20)
	tl := buildTimeline(in)
	if len(tl.Mains) == 0 || !tl.Mains[0].From.Equal(at(0)) {
		t.Fatalf("mains = %+v, want it to start at the window start", tl.Mains)
	}
	if got := tl.Mains[0].Kind; got != "mains" {
		t.Fatalf("the prior sample says mains at the start, got %s", got)
	}
	for _, s := range tl.Mains {
		if s.To.After(at(20)) {
			t.Fatalf("segment runs past the window: %+v", s)
		}
	}
}

func TestTimelineLiveSampleReachesNow(t *testing.T) {
	pts := []samples.Sample{smp(0, nut.StateOnline, 100, both, nil)}
	live := smp(8, nut.StateOnline, 100, both, nil)
	in := baseInput(pts, nil)
	in.Live = &live
	in.To, in.Now = at(30), at(8)
	tl := buildTimeline(in)
	last := tl.Mains[len(tl.Mains)-1]
	if !last.To.Equal(at(8)) {
		t.Fatalf("lane ends at %v, want now (%v)", last.To, at(8))
	}
}

func TestTimelineEmpty(t *testing.T) {
	tl := buildTimeline(baseInput(nil, nil))
	if tl.HasData {
		t.Fatal("HasData with no samples")
	}
	if tl.Mains == nil || tl.Pi == nil || tl.Nodes == nil || tl.Flags == nil || tl.Bands == nil || tl.Battery.Runs == nil {
		t.Fatal("empty slices must be non-nil so the JSON has [] and not null")
	}
}

func TestMerge(t *testing.T) {
	in := []Segment{
		{at(0), at(5), "mains"}, {at(5), at(10), "mains"},
		{at(10), at(15), "battery"}, {at(20), at(25), "battery"}, // not touching
	}
	got := merge(in)
	if len(got) != 3 {
		t.Fatalf("merge = %+v, want 3 segments", got)
	}
	if !got[0].To.Equal(at(10)) {
		t.Fatalf("first segment should absorb the second: %+v", got[0])
	}
}

func TestDecimateKeepsEndsAndBound(t *testing.T) {
	var pts []Point
	for i := 0; i < 10000; i++ {
		pts = append(pts, Point{T: at(float64(i)), Charge: float64(i % 100)})
	}
	got := decimate(pts, 1200)
	if len(got) > 1201 {
		t.Fatalf("decimate kept %d points", len(got))
	}
	if got[0] != pts[0] || got[len(got)-1] != pts[len(pts)-1] {
		t.Fatal("decimate dropped an end point")
	}
	small := pts[:10]
	if len(decimate(small, 1200)) != 10 {
		t.Fatal("decimate touched a short series")
	}
}

func TestMainsKind(t *testing.T) {
	cases := map[nut.State]string{
		nut.StateOnline: "mains", nut.StateOnBattery: "battery", nut.StateLowBattery: "battery",
		nut.StateOther: "other", nut.StateUnreachable: "unknown", "": "unknown",
	}
	for st, want := range cases {
		if got := mainsKind(st); got != want {
			t.Errorf("mainsKind(%q) = %q, want %q", st, got, want)
		}
	}
}
