package web

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/demo"
)

func testServer(t *testing.T, scene demo.Scene) (*Server, *demo.Demo) {
	t.Helper()
	cfg := config.Default()
	cfg.Nodes = []config.Node{
		{Name: "kaios", Host: "10.10.0.10", MAC: "aa:bb:cc:dd:ee:01"},
		{Name: "node-b", Host: "10.10.0.11", MAC: "aa:bb:cc:dd:ee:02"},
	}
	now := time.Date(2026, 11, 14, 11, 32, 7, 0, time.UTC)
	d, err := demo.Build(scene, now, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(&cfg, Deps{
		Monitor: d, Waker: d, Events: d.Events, Samples: d.Samples,
		Version: "test", ConfigPath: "/etc/nutwake/config.yaml", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, d
}

func get(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestNewNeedsItsStores(t *testing.T) {
	cfg := config.Default()
	if _, err := New(&cfg, Deps{}); err == nil {
		t.Fatal("New accepted empty Deps")
	}
}

func TestIndexAndHeaders(t *testing.T) {
	s, _ := testServer(t, demo.Online)
	rec := get(t, s.Handler(), "GET", "/")
	if rec.Code != 200 {
		t.Fatalf("GET / = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type = %q", ct)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'self'", "style-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if strings.Contains(csp, "unsafe") {
		t.Errorf("CSP allows unsafe sources: %q", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("missing hardening headers: %v", rec.Header())
	}
	body := rec.Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, " style=") {
		t.Error("the page has inline script or style, which the CSP would block")
	}
	if !strings.Contains(body, `/static/app.js`) || !strings.Contains(body, `/static/app.css`) {
		t.Error("the page does not load its assets")
	}
}

func TestStaticAssets(t *testing.T) {
	s, _ := testServer(t, demo.Online)
	for path, want := range map[string]string{
		"/static/app.js":  "text/javascript",
		"/static/app.css": "text/css",
		"/static/fonts/instrument-sans-latin-wght.woff2": "font/woff2",
	} {
		rec := get(t, s.Handler(), "GET", path)
		if rec.Code != 200 {
			t.Errorf("GET %s = %d", path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, want) {
			t.Errorf("GET %s content type = %q, want %q", path, ct, want)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s is empty", path)
		}
	}
	if rec := get(t, s.Handler(), "GET", "/static/nope.js"); rec.Code != 404 {
		t.Errorf("missing asset = %d", rec.Code)
	}
}

func TestOnlyReadMethods(t *testing.T) {
	s, _ := testServer(t, demo.Online)
	h := s.Handler()
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		for _, p := range []string{"/", "/api/status", "/static/app.js", "/healthz"} {
			rec := get(t, h, m, p)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405", m, p, rec.Code)
			}
		}
	}
	if rec := get(t, h, "HEAD", "/api/status"); rec.Code != 200 {
		t.Errorf("HEAD /api/status = %d", rec.Code)
	}
}

func TestNotFound(t *testing.T) {
	s, _ := testServer(t, demo.Online)
	for _, p := range []string{"/nope", "/api", "/api/status/x", "/index.html"} {
		if rec := get(t, s.Handler(), "GET", p); rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404", p, rec.Code)
		}
	}
}

func TestHealthz(t *testing.T) {
	s, _ := testServer(t, demo.Online)
	rec := get(t, s.Handler(), "GET", "/healthz")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}

func TestStatusJSONForEveryScene(t *testing.T) {
	for _, sc := range demo.Scenes {
		t.Run(string(sc), func(t *testing.T) {
			s, _ := testServer(t, sc)
			rec := get(t, s.Handler(), "GET", "/api/status")
			if rec.Code != 200 {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
			}
			var raw map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
				t.Fatalf("not JSON: %v", err)
			}
			// the page calls forEach on these, so they must be arrays
			for _, k := range []string{"nodes", "outages", "events"} {
				if _, ok := raw[k].([]any); !ok {
					t.Errorf("%s is %T, want an array", k, raw[k])
				}
			}
			tl, _ := raw["timeline"].(map[string]any)
			for _, k := range []string{"mains", "nodes", "pi", "bands", "flags", "ranges"} {
				if _, ok := tl[k].([]any); !ok {
					t.Errorf("timeline.%s is %T, want an array", k, tl[k])
				}
			}
			if b, _ := tl["battery"].(map[string]any); b == nil {
				t.Error("no battery lane")
			} else if _, ok := b["runs"].([]any); !ok {
				t.Errorf("battery.runs is %T, want an array", b["runs"])
			}
			if strings.Contains(rec.Body.String(), "null") {
				// nothing in a scene should be null: omitempty covers the optional ones
				i := strings.Index(rec.Body.String(), "null")
				t.Errorf("the JSON has a null near %q", rec.Body.String()[max(0, i-60):i+20])
			}
		})
	}
}

func TestStatusScenesTellTheirStory(t *testing.T) {
	cases := []struct {
		scene     demo.Scene
		tone      string
		title     string
		wakeBlock bool
		inProg    bool
	}{
		{demo.Online, ToneOK, "Power is on", false, false},
		{demo.Battery, ToneWarn, "Running on battery", false, true},
		{demo.Waiting, ToneWait, "Power is back", true, false},
	}
	for _, c := range cases {
		t.Run(string(c.scene), func(t *testing.T) {
			s, _ := testServer(t, c.scene)
			st := s.Status("")
			if st.Headline.Tone != c.tone || st.Headline.Title != c.title {
				t.Fatalf("headline = %+v", st.Headline)
			}
			if st.Wake.Active != c.wakeBlock {
				t.Fatalf("wake active = %v", st.Wake.Active)
			}
			if len(st.Outages) == 0 || st.Outages[0].InProgress != c.inProg {
				t.Fatalf("outages = %+v", st.Outages)
			}
			if !st.Timeline.HasData || len(st.Timeline.Flags) == 0 {
				t.Fatalf("timeline = %+v", st.Timeline)
			}
			if st.Timeline.Battery.WakeAt != 15 {
				t.Fatalf("wake_at = %v, want the 15%% from the config default", st.Timeline.Battery.WakeAt)
			}
		})
	}
}

func TestBatterySceneProjectsTheShutdown(t *testing.T) {
	s, _ := testServer(t, demo.Battery)
	st := s.Status("")
	if st.Timeline.Battery.Projection == nil {
		t.Fatal("no projection while on battery")
	}
	found := false
	for _, f := range st.Timeline.Flags {
		if f.Label == "Shutdown expected" && f.Approx {
			found = true
		}
	}
	if !found {
		t.Fatalf("flags = %+v, want one for the expected shutdown", st.Timeline.Flags)
	}
}

func TestOnlineSceneShowsTheGap(t *testing.T) {
	s, _ := testServer(t, demo.Online)
	st := s.Status("outage")
	gap := false
	for _, seg := range st.Timeline.Pi {
		if seg.Kind == "gap" {
			gap = true
		}
	}
	if !gap {
		t.Fatalf("pi lane = %+v, want a gap where the Pi was off", st.Timeline.Pi)
	}
	if len(st.Timeline.Battery.Runs) < 2 {
		t.Fatalf("battery runs = %d, want the gap to split the line", len(st.Timeline.Battery.Runs))
	}
	for _, n := range st.Timeline.Nodes {
		offSeen := false
		for _, seg := range n.Segments {
			if seg.Kind == "off" {
				offSeen = true
			}
		}
		if !offSeen {
			t.Errorf("node %s lane = %+v, want an off stretch", n.Name, n.Segments)
		}
	}
}

func TestRanges(t *testing.T) {
	s, _ := testServer(t, demo.Online)
	h := s.Handler()
	for _, rng := range []string{"", "auto", "outage", "24h", "7d"} {
		rec := get(t, h, "GET", "/api/status?range="+rng)
		if rec.Code != 200 {
			t.Errorf("range %q = %d", rng, rec.Code)
		}
	}
	for _, rng := range []string{"1h", "all", "../x", "24H"} {
		if rec := get(t, h, "GET", "/api/status?range="+rng); rec.Code != 400 {
			t.Errorf("range %q = %d, want 400", rng, rec.Code)
		}
	}
	for _, rng := range []string{Range24h, Range7d} {
		st := s.Status(rng)
		if st.Timeline.Range != rng {
			t.Errorf("asked for %s, got %s", rng, st.Timeline.Range)
		}
	}
	if st := s.Status("24h"); st.Timeline.To.Sub(st.Timeline.From) < 20*time.Hour {
		t.Errorf("24h window is %v", st.Timeline.To.Sub(st.Timeline.From))
	}
}

func TestSettingsListNodesWithoutMACs(t *testing.T) {
	s, _ := testServer(t, demo.Online)
	st := s.Status("")
	if st.ConfigPath != "/etc/nutwake/config.yaml" {
		t.Fatalf("config path = %q", st.ConfigPath)
	}
	b, _ := json.Marshal(st)
	if !strings.Contains(string(b), "kaios") || !strings.Contains(string(b), "10.10.0.10") {
		t.Fatalf("settings do not list the nodes: %s", b)
	}
	if strings.Contains(strings.ToLower(string(b)), "aa:bb:cc:dd:ee") {
		t.Fatalf("the MAC addresses are in the page data: %s", b)
	}
}

func TestServeAndShutDown(t *testing.T) {
	s, _ := testServer(t, demo.Online)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener:", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("healthz = %d %q", resp.StatusCode, body)
	}
	head, err := http.Head("http://" + ln.Addr().String() + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := io.Copy(io.Discard, head.Body); n != 0 || head.StatusCode != 200 {
		t.Fatalf("HEAD /api/status = %d with %d body bytes", head.StatusCode, n)
	}
	head.Body.Close()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v after cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after the context was cancelled")
	}
}
