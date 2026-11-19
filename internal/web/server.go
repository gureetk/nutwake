// Package web serves the read-only status page and its JSON.
//
// The page can see everything and change nothing: the only methods are GET and
// HEAD, and nothing it receives is written anywhere or run. That is deliberate.
// The shutdown command runs as root, so a page that could change settings
// would be a way to run commands on the Pi.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gureetk/nutwake/internal/config"
	"github.com/gureetk/nutwake/internal/events"
	"github.com/gureetk/nutwake/internal/monitor"
	"github.com/gureetk/nutwake/internal/samples"
	"github.com/gureetk/nutwake/internal/waker"
)

//go:embed assets
var assetsFS embed.FS

// MonitorSource is what the page needs from the monitor.
type MonitorSource interface {
	Snapshot() monitor.Snapshot
}

// WakeSource is what the page needs from the waker.
type WakeSource interface {
	Status() waker.Status
}

// Deps are the things a Server reads. Waker may be nil.
type Deps struct {
	Monitor MonitorSource
	Waker   WakeSource
	Events  *events.Store
	Samples *samples.Store
	// Version is shown in the footer.
	Version string
	// ConfigPath is where the settings can be changed, shown in the settings.
	ConfigPath string
	Now        func() time.Time
}

// Server answers requests for the status page.
type Server struct {
	cfg        *config.Config
	mon        MonitorSource
	wake       WakeSource
	events     *events.Store
	samples    *samples.Store
	version    string
	configPath string
	now        func() time.Time
	started    time.Time
}

// New builds a Server.
func New(cfg *config.Config, d Deps) (*Server, error) {
	if d.Monitor == nil || d.Events == nil || d.Samples == nil {
		return nil, errors.New("web: Monitor, Events and Samples are required")
	}
	s := &Server{
		cfg: cfg, mon: d.Monitor, events: d.Events, samples: d.Samples,
		version: d.Version, configPath: d.ConfigPath, now: d.Now, started: time.Now(),
	}
	if d.Waker != nil {
		s.wake = d.Waker
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

const csp = "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; " +
	"img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	static, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, static, "index.html")
	})
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	files := http.StripPrefix("/static/", http.FileServerFS(static))
	mux.HandleFunc("GET /static/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		files.ServeHTTP(w, r)
	})
	return secure(mux)
}

// secure adds the headers that keep the page to itself.
func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

var validRanges = map[string]bool{"": true, "auto": true, RangeOutage: true, Range24h: true, Range7d: true}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	rng := r.URL.Query().Get("range")
	if !validRanges[rng] {
		http.Error(w, "range must be auto, outage, 24h or 7d", http.StatusBadRequest)
		return
	}
	body, err := json.Marshal(s.Status(rng))
	if err != nil {
		slog.Error("web: cannot encode the status", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(append(body, '\n'))
}

// Run listens on the configured address until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		return err
	}
	slog.Info("status page listening", "addr", ln.Addr().String())
	return s.Serve(ctx, ln)
}

// Serve is Run on a listener you already have.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		<-done
		return nil
	}
	return err
}
