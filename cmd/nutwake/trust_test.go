//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUntrustedConfig(t *testing.T) {
	// the file belongs to whoever runs the test, so say that user is the one to trust
	me := uint32(os.Getuid())
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("x"), 0o644)
	os.Chmod(dir, 0o755)

	if why := untrustedBy(path, me); why != "" {
		t.Fatalf("a tidy file was not trusted: %q", why)
	}

	os.Chmod(path, 0o666)
	if why := untrustedBy(path, me); !strings.Contains(why, "can be changed by users other than root") {
		t.Fatalf("a world-writable file was trusted: %q", why)
	}
	os.Chmod(path, 0o664)
	if untrustedBy(path, me) == "" {
		t.Fatal("a group-writable file was trusted")
	}

	// a file in a directory that others can write to can be replaced
	os.Chmod(path, 0o644)
	os.Chmod(dir, 0o777)
	if untrustedBy(path, me) == "" {
		t.Fatal("a file in a world-writable directory was trusted")
	}
	os.Chmod(dir, 0o755)

	// the owner counts: this file does not belong to the user asked for
	if why := untrustedBy(path, me+1); !strings.Contains(why, "belongs to user") {
		t.Fatalf("a file with the wrong owner was trusted: %q", why)
	}
	if untrustedBy(filepath.Join(dir, "missing"), me) == "" {
		t.Fatal("a missing file was trusted")
	}

	// and for root, a file owned by anyone else is not trusted
	if os.Geteuid() != 0 {
		if why := untrusted(path); !strings.Contains(why, "not root") {
			t.Fatalf("a file owned by an ordinary user was trusted by root: %q", why)
		}
	} else if why := untrusted(path); why != "" {
		t.Fatalf("a root-owned, tidy file was not trusted: %q", why)
	}
}

func TestRootHelperIgnoresACommandFromAnUntrustedConfig(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "default-halt")
	old, oldRoot := haltFallback, isRoot
	haltFallback = []string{"sh", "-c", "touch " + marker}
	isRoot = func() bool { return true }
	t.Cleanup(func() { haltFallback, isRoot = old, oldRoot })

	nutd := startFakeNUT(t)
	r := newRig(t, nutd.Addr(), "a")
	r.nodes["a"].down()
	os.MkdirAll(filepath.Join(r.dir, "data"), 0o755)
	os.Chmod(r.cfgPath, 0o666) // anyone could have written the command in it

	code, _, errOut := runCmd(t, "shutdown", "-config", r.cfgPath)
	if code != 0 {
		t.Fatalf("shutdown = %d\n%s", code, errOut)
	}
	if fileExists(r.halted) {
		t.Fatal("the command from an untrusted config was run")
	}
	if !fileExists(marker) {
		t.Fatalf("the default halt did not run\n%s", errOut)
	}
	if !strings.Contains(errOut, "not using shutdown.command") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestCheckWarnsAboutAnUntrustedConfig(t *testing.T) {
	nutd := startFakeNUT(t)
	r := newRig(t, nutd.Addr(), "a")
	os.MkdirAll(filepath.Join(r.dir, "data"), 0o755)
	os.Chmod(r.cfgPath, 0o666)
	code, out, _ := runCmd(t, "check", "-config", r.cfgPath)
	if code != 0 || !strings.Contains(out, "permissions") || !strings.Contains(out, "warn") {
		t.Fatalf("check = %d\n%s", code, out)
	}
}
