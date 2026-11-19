// Package fakenut is a tiny in-process imitation of upsd. It answers
// LIST VAR for one UPS from a variable map that tests (or the fakenut demo
// command) can change while it runs.
package fakenut

import (
	"bufio"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
)

// Server is a fake upsd.
type Server struct {
	ln  net.Listener
	ups string

	mu   sync.Mutex
	vars map[string]string
	down bool // when true, new connections are dropped immediately
}

// Start listens on addr ("127.0.0.1:0" for a free port) and serves the UPS
// named ups with a healthy, online, fully charged set of variables.
func Start(addr, ups string) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{
		ln:  ln,
		ups: ups,
		vars: map[string]string{
			"battery.charge":  "100",
			"battery.runtime": "2997",
			"device.mfr":      "CPS",
			"device.model":    "ST425",
			"input.voltage":   "121.0",
			"output.voltage":  "121.0",
			"ups.load":        "14",
			"ups.status":      "OL",
		},
	}
	go s.serve()
	return s, nil
}

// Addr is the address the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Close stops the server.
func (s *Server) Close() error { return s.ln.Close() }

// Set changes (or adds) variables.
func (s *Server) Set(kv map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range kv {
		s.vars[k] = v
	}
}

// SetStatus sets ups.status.
func (s *Server) SetStatus(status string) { s.Set(map[string]string{"ups.status": status}) }

// SetDown makes the server drop connections, as if upsd were not running.
func (s *Server) SetDown(down bool) {
	s.mu.Lock()
	s.down = down
	s.mu.Unlock()
}

func (s *Server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	s.mu.Lock()
	down := s.down
	s.mu.Unlock()
	if down {
		return
	}
	sc := bufio.NewScanner(conn)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		switch {
		case len(f) == 3 && f[0] == "LIST" && f[1] == "VAR":
			if f[2] != s.ups {
				fmt.Fprint(conn, "ERR UNKNOWN-UPS\n")
				continue
			}
			s.mu.Lock()
			keys := make([]string, 0, len(s.vars))
			for k := range s.vars {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			fmt.Fprintf(conn, "BEGIN LIST VAR %s\n", s.ups)
			for _, k := range keys {
				v := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s.vars[k])
				fmt.Fprintf(conn, "VAR %s %s \"%s\"\n", s.ups, k, v)
			}
			fmt.Fprintf(conn, "END LIST VAR %s\n", s.ups)
			s.mu.Unlock()
		case len(f) == 1 && f[0] == "LOGOUT":
			fmt.Fprint(conn, "OK Goodbye\n")
			return
		default:
			fmt.Fprint(conn, "ERR UNKNOWN-COMMAND\n")
		}
	}
}
