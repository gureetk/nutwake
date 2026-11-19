package config

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// The example file doubles as documentation, so it must always load, and its
// values must match the built-in defaults it claims to show.
func TestExampleConfigParses(t *testing.T) {
	cfg, err := Load("../../config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	def := Default()
	if cfg.Wake != def.Wake {
		t.Errorf("example wake settings differ from the defaults:\n got %+v\nwant %+v", cfg.Wake, def.Wake)
	}
	if cfg.Probe != def.Probe || cfg.NUT != def.NUT || cfg.History != def.History {
		t.Errorf("example differs from the defaults: %+v", cfg)
	}
	if len(cfg.Nodes) != 2 {
		t.Errorf("got %d nodes, want 2", len(cfg.Nodes))
	}
}

func TestMaxWaitZeroTurnsItOff(t *testing.T) {
	cfg, err := Parse([]byte(minimal + "\nwake: {max_wait: 0s}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Wake.MaxWait != 0 {
		t.Errorf("MaxWait = %v, want 0", cfg.Wake.MaxWait)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/nutwake.yaml")
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("want a not-exist error, got %v", err)
	}
}

func TestLoadErrorNamesTheFile(t *testing.T) {
	path := t.TempDir() + "/c.yaml"
	if err := os.WriteFile(path, []byte("nodes: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("error %v should mention %s", err, path)
	}
}

func TestEmptyFileIsInvalidBecauseNoNodes(t *testing.T) {
	if _, err := Parse(nil); err == nil || !strings.Contains(err.Error(), "node") {
		t.Errorf("want a 'no nodes' error, got %v", err)
	}
}
