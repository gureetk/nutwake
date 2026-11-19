package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOutageRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	if o, err := s.LoadOutage(); o != nil || err != nil {
		t.Fatalf("empty store: %v, %v", o, err)
	}
	want := &Outage{Started: time.Date(2026, 11, 13, 19, 52, 8, 0, time.UTC), StartCharge: 100, MinCharge: 43, NodesUp: []string{"kaios", "node-b"}}
	if err := s.SaveOutage(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadOutage()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if err := s.ClearOutage(); err != nil {
		t.Fatal(err)
	}
	if o, _ := s.LoadOutage(); o != nil {
		t.Error("outage should be gone")
	}
	if err := s.ClearOutage(); err != nil {
		t.Errorf("clearing twice should be fine: %v", err)
	}
}

func TestPendingRoundTripAndEmptyRemovesFile(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)
	p := &Pending{Since: time.Now().UTC().Round(time.Second), Nodes: []string{"a", "b"}, Reason: "power cut"}
	if err := s.SavePending(p); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadPending()
	if err != nil || got == nil || !reflect.DeepEqual(got.Nodes, p.Nodes) || got.Reason != "power cut" {
		t.Fatalf("got %+v, %v", got, err)
	}
	got.Remove("a")
	got.Remove("b")
	if err := s.SavePending(got); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingFile)); !os.IsNotExist(err) {
		t.Error("an empty wake list should remove the file")
	}
	if p, _ := s.LoadPending(); p != nil {
		t.Error("nothing should be pending")
	}
}

func TestCorruptFileIsReportedNotIgnored(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, pendingFile), []byte("{oops"), 0o644)
	_, err := New(dir).LoadPending()
	if err == nil || !strings.Contains(err.Error(), pendingFile) {
		t.Errorf("want an error naming the file, got %v", err)
	}
}

func TestPendingSetOperations(t *testing.T) {
	p := &Pending{}
	p.Add("kaios", "node-b", "kaios")
	if !reflect.DeepEqual(p.Nodes, []string{"kaios", "node-b"}) {
		t.Errorf("Add did not de-duplicate: %v", p.Nodes)
	}
	if !p.Has("node-b") || p.Has("nope") {
		t.Error("Has wrong")
	}
	p.Remove("kaios")
	p.Remove("missing")
	if !reflect.DeepEqual(p.Nodes, []string{"node-b"}) {
		t.Errorf("Remove: %v", p.Nodes)
	}
}

func TestSaveInMissingDirFails(t *testing.T) {
	if err := New(filepath.Join(t.TempDir(), "nope")).SaveOutage(&Outage{}); err == nil {
		t.Error("expected an error")
	}
}
