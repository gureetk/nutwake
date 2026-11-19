package waker

import (
	"time"

	"github.com/gureetk/nutwake/internal/state"
)

// Status is what the status page shows about waking.
type Status struct {
	// Active is true while some node is waiting to be woken.
	Active bool `json:"active"`
	// Since is when the nodes were shut down.
	Since time.Time `json:"since,omitempty"`
	// Waiting is why nothing is being sent yet (one of the Wait constants),
	// empty once sending has begun.
	Waiting string `json:"waiting,omitempty"`
	Message string `json:"message,omitempty"`
	// MinBattery is the charge needed before waking, for the battery chart.
	MinBattery float64        `json:"min_battery"`
	Nodes      []NodeProgress `json:"nodes,omitempty"`
}

// NodeProgress is where one waiting node is.
type NodeProgress struct {
	Name string `json:"name"`
	// State is "waiting" (not time yet), "queued" (next in line), or
	// "sent" (a packet was sent and the node has not answered).
	State     string    `json:"state"`
	Attempts  int       `json:"attempts,omitempty"`
	FirstSent time.Time `json:"first_sent,omitempty"`
	LastSent  time.Time `json:"last_sent,omitempty"`
}

// Status returns what the waker is doing now.
func (w *Waker) Status() Status {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.status
	s.MinBattery = w.cfg.Wake.MinBattery
	s.Nodes = append([]NodeProgress(nil), s.Nodes...)
	return s
}

func (w *Waker) fillNodes(p *state.Pending) {
	w.status.Nodes = w.status.Nodes[:0]
	for _, name := range p.Nodes {
		np := NodeProgress{Name: name, State: "waiting"}
		if t := w.tracks[name]; t != nil && t.attempts > 0 {
			np.State, np.Attempts, np.FirstSent, np.LastSent = "sent", t.attempts, t.firstSent, t.lastSent
		} else if w.begun {
			np.State = "queued"
		}
		w.status.Nodes = append(w.status.Nodes, np)
	}
}
