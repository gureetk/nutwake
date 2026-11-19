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

// Ago says how long ago something was: "just now", "12 min ago", "5 h ago",
// "5 days ago".
func Ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

// List joins names for a sentence: "a", "a and b", "a, b and c".
func List(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	out := ""
	for i, n := range names[:len(names)-1] {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out + " and " + names[len(names)-1]
}
