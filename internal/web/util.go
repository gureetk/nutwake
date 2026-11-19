package web

import (
	"strconv"
	"time"

	"github.com/gureetk/nutwake/internal/humanize"
)

func itoa(n int) string { return strconv.Itoa(n) }

func pct(v float64) string {
	if v <= 0 {
		return "off"
	}
	return humanize.Percent(v) + "%"
}

func dur(d time.Duration) string { return humanize.Duration(d) }

func orOff(d time.Duration) string {
	if d <= 0 {
		return "never"
	}
	return humanize.Duration(d)
}
