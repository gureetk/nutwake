//go:build linux

package clocksync

import "syscall"

// staUnsync is the kernel's STA_UNSYNC status bit. NTP clients (systemd-timesyncd,
// chrony, ntpd) clear it once they have set the clock.
const staUnsync = 0x40

func systemSynced() bool {
	var tx syscall.Timex // Modes == 0 only reads the state
	if _, err := syscall.Adjtimex(&tx); err != nil {
		return true // cannot tell, so do not block anything forever
	}
	return tx.Status&staUnsync == 0
}
