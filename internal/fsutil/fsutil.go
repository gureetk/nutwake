// Package fsutil has the few file helpers nutwake needs: atomic replacement
// and appending, keeping ownership sensible when root writes into a directory
// owned by the service user.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data. Readers see the old file or the new
// one, never a partial write, even if the machine loses power mid-write.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	inheritOwner(name, dir)
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// AppendFile appends data to path in a single write, creating it if needed.
// A single O_APPEND write keeps lines from different processes from mixing.
func AppendFile(path string, data []byte, perm os.FileMode) error {
	_, statErr := os.Stat(path)
	created := os.IsNotExist(statErr)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if created {
		inheritOwner(path, filepath.Dir(path))
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
