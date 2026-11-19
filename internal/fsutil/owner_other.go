//go:build !unix

package fsutil

func inheritOwner(path, dir string) {}
