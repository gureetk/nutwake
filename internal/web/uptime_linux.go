//go:build linux

package web

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// uptimeSeconds is how long this machine has been up. If /proc is not
// readable it falls back to how long the daemon has been running.
func uptimeSeconds(daemonStart time.Time) int64 {
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				return int64(v)
			}
		}
	}
	return int64(time.Since(daemonStart).Seconds())
}
