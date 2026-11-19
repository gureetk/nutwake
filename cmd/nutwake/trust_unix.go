//go:build unix

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// untrusted says why the file at path cannot be trusted to name a command that
// root will run, or "" if it can: it and the directory it is in must belong to
// root and not be writable by anyone else.
func untrusted(path string) string { return untrustedBy(path, 0) }

// untrustedBy is untrusted for a file that should belong to the user uid.
func untrustedBy(path string, uid uint32) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	for _, p := range []string{abs, filepath.Dir(abs)} {
		fi, err := os.Stat(p)
		if err != nil {
			return fmt.Sprintf("%s cannot be examined: %v", p, err)
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return ""
		}
		if st.Uid != uid {
			if uid == 0 {
				return fmt.Sprintf("%s belongs to user %d, not root", p, st.Uid)
			}
			return fmt.Sprintf("%s belongs to user %d, not %d", p, st.Uid, uid)
		}
		if fi.Mode().Perm()&0o022 != 0 {
			return fmt.Sprintf("%s can be changed by users other than root", p)
		}
	}
	return ""
}
