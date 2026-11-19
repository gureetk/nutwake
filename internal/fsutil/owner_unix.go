//go:build unix

package fsutil

import (
	"os"
	"syscall"
)

// inheritOwner gives path the owner of dir when running as root. The shutdown
// helper runs as root but the daemon does not, and the daemon must still be
// able to read and replace the files the helper creates.
func inheritOwner(path, dir string) {
	if os.Geteuid() != 0 {
		return
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(path, int(st.Uid), int(st.Gid))
	}
}
