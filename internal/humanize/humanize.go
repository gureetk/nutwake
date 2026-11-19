// Package humanize formats values for the messages people read.
package humanize

import (
	"fmt"
	"time"
)

// Duration formats d for people: "45 s", "12 min", "1 h 05 min".
// Seconds are shown only below a minute.
func Duration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	h := int(d.Hours())
	return fmt.Sprintf("%d h %02d min", h, int(d.Minutes())-h*60)
}

// Percent formats a percentage without decimals: 96.4 becomes "96".
func Percent(v float64) string { return fmt.Sprintf("%.0f", v) }
