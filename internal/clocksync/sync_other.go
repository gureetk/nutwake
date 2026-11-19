//go:build !linux

package clocksync

func systemSynced() bool { return true }
