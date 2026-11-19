//go:build !linux

package web

import "time"

func uptimeSeconds(daemonStart time.Time) int64 { return int64(time.Since(daemonStart).Seconds()) }
