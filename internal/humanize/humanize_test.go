package humanize

import (
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                                     "0 s",
		4 * time.Second:                       "4 s",
		12*time.Minute + 20*time.Second:       "12 min",
		time.Hour + 5*time.Minute:             "1 h 05 min",
		26*time.Hour + 3*time.Minute:          "26 h 03 min",
		59*time.Second + 600*time.Millisecond: "1 min",
	}
	for d, want := range cases {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestPercent(t *testing.T) {
	if got := Percent(96.4); got != "96" {
		t.Errorf("got %q", got)
	}
}

func TestAgo(t *testing.T) {
	cases := map[time.Duration]string{
		10 * time.Second:   "just now",
		5 * time.Minute:    "5 min ago",
		3 * time.Hour:      "3 h ago",
		47 * time.Hour:     "47 h ago",
		5 * 24 * time.Hour: "5 days ago",
	}
	for d, want := range cases {
		if got := Ago(d); got != want {
			t.Errorf("Ago(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestList(t *testing.T) {
	cases := map[string][]string{
		"":              {},
		"a":             {"a"},
		"a and b":       {"a", "b"},
		"a, b and c":    {"a", "b", "c"},
		"a, b, c and d": {"a", "b", "c", "d"},
	}
	for want, in := range cases {
		if got := List(in); got != want {
			t.Errorf("List(%v) = %q, want %q", in, got, want)
		}
	}
}
