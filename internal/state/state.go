// Package state holds the small pieces of information that must survive a
// reboot: the outage in progress, and which nodes are waiting to be woken.
//
// Each file has one writer at a time. outage.json belongs to the daemon.
// wake.json is written by the shutdown helper just before the Pi halts and
// cleared by the daemon once the nodes are back, and the two never run
// together across a reboot.
package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/gureetk/nutwake/internal/fsutil"
)

// Outage is a power cut that has started and not yet been closed.
type Outage struct {
	Started     time.Time `json:"started"`
	StartCharge float64   `json:"start_charge"`
	MinCharge   float64   `json:"min_charge"`
	// NodesUp lists the nodes that were up when power was lost.
	NodesUp []string `json:"nodes_up"`
}

// Pending lists nodes that were shut down by a power cut and must be woken.
type Pending struct {
	Since  time.Time `json:"since"`
	Nodes  []string  `json:"nodes"`
	Reason string    `json:"reason,omitempty"`
}

// Has reports whether name is waiting to be woken.
func (p *Pending) Has(name string) bool { return slices.Contains(p.Nodes, name) }

// Add puts names on the list, keeping what is already there.
func (p *Pending) Add(names ...string) {
	for _, n := range names {
		if !p.Has(n) {
			p.Nodes = append(p.Nodes, n)
		}
	}
}

// Remove takes name off the list.
func (p *Pending) Remove(name string) {
	p.Nodes = slices.DeleteFunc(p.Nodes, func(n string) bool { return n == name })
}

// Store reads and writes the state files in one directory.
type Store struct{ dir string }

// New returns a Store for dir.
func New(dir string) *Store { return &Store{dir: dir} }

const (
	outageFile  = "outage.json"
	pendingFile = "wake.json"
)

// LoadOutage returns the open outage, or nil if there is none.
func (s *Store) LoadOutage() (*Outage, error) { return load[Outage](filepath.Join(s.dir, outageFile)) }

// SaveOutage records o as the open outage.
func (s *Store) SaveOutage(o *Outage) error { return save(filepath.Join(s.dir, outageFile), o) }

// ClearOutage forgets the open outage.
func (s *Store) ClearOutage() error { return remove(filepath.Join(s.dir, outageFile)) }

// LoadPending returns the nodes waiting to be woken, or nil if none.
func (s *Store) LoadPending() (*Pending, error) {
	p, err := load[Pending](filepath.Join(s.dir, pendingFile))
	if p != nil && len(p.Nodes) == 0 {
		return nil, err
	}
	return p, err
}

// SavePending records p. An empty list removes the file.
func (s *Store) SavePending(p *Pending) error {
	if p == nil || len(p.Nodes) == 0 {
		return s.ClearPending()
	}
	return save(filepath.Join(s.dir, pendingFile), p)
}

// ClearPending forgets the wake list.
func (s *Store) ClearPending() error { return remove(filepath.Join(s.dir, pendingFile)) }

func load[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, errors.New(filepath.Base(path) + " is not valid: " + err.Error())
	}
	return &v, nil
}

func save(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, append(data, '\n'), 0o644)
}

func remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
