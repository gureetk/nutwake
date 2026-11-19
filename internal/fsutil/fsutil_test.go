package fsutil

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteFileAtomicReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	for _, content := range []string{"one", "two, longer"} {
		if err := WriteFileAtomic(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(p)
		if string(got) != content {
			t.Errorf("got %q, want %q", got, content)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only state.json, found %d entries", len(entries))
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
}

func TestWriteFileAtomicFailsInMissingDir(t *testing.T) {
	if err := WriteFileAtomic(filepath.Join(t.TempDir(), "nope", "x"), []byte("x"), 0o644); err == nil {
		t.Error("expected an error")
	}
}

func TestAppendFileKeepsLinesWhole(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := AppendFile(p, []byte("a line of text\n"), 0o644); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, _ := os.ReadFile(p)
	if len(got) != 20*len("a line of text\n") {
		t.Errorf("got %d bytes", len(got))
	}
}
